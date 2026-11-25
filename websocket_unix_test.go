//go:build unix

package main

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newWebSocketServer(t *testing.T, appConfig Config, cmd string, allowOrigin string) (*httptest.Server, chan struct{}) {
	t.Helper()

	shell, params, err := getShellAndParams(cmd, appConfig)
	if err != nil {
		t.Fatal(err)
	}

	handlerDone := make(chan struct{}, 1)
	handler := mwLogging(getWebSocketHandler(appConfig, shell, params, allowOrigin)) // hijack must work through the middleware
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		handler(rw, req)
		// don't block a test that makes several requests and doesn't wait for the handlers
		select {
		case handlerDone <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(server.Close)

	return server, handlerDone
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func dialWebSocket(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()

	conn, res, err := websocket.DefaultDialer.Dial(wsURL(server), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

type wsMessage struct {
	messageType int
	data        string
}

// readUntilClose - read all messages until the server has closed the connection, returns the messages and the close frame
func readUntilClose(t *testing.T, conn *websocket.Conn, timeout time.Duration) ([]wsMessage, *websocket.CloseError) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}

	var messages []wsMessage
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) {
				t.Fatalf("got %v, want a close frame, messages: %v", err, messages)
			}
			return messages, closeErr
		}
		messages = append(messages, wsMessage{messageType: messageType, data: string(data)})
	}
}

// readPid - read the first message with the pid of a background process
func readPid(t *testing.T, conn *websocket.Conn) int {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("bad pid %q: %v", data, err)
	}

	return pid
}

func waitHandlerDone(t *testing.T, handlerDone chan struct{}, what string) {
	t.Helper()

	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("handler did not return %s", what)
	}
}

func Test_getWebSocketHandler_stdinAndExitCode(t *testing.T) {
	server, _ := newWebSocketServer(t, testConfig(0), `echo ready; read a; echo "a=$a"; read b; echo "b=$b"; printf last; exit 3`, "")
	conn := dialWebSocket(t, server)

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// the output is sent before the command has finished
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "ready" {
		t.Fatalf("got %q, %v, want ready", data, err)
	}

	for _, msg := range []string{"one", "two"} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}

	messages, closeErr := readUntilClose(t, conn, 3*time.Second)
	want := []wsMessage{{websocket.TextMessage, "a=one"}, {websocket.TextMessage, "b=two"}, {websocket.TextMessage, "last"}}
	if !equalMessages(messages, want) {
		t.Errorf("messages: got %v, want %v", messages, want)
	}
	if closeErr.Code != websocket.CloseNormalClosure || closeErr.Text != "3" {
		t.Errorf("close: got %d %q, want %d \"3\"", closeErr.Code, closeErr.Text, websocket.CloseNormalClosure)
	}
}

func equalMessages(got, want []wsMessage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func Test_getWebSocketHandler_outputLines(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.includeStderr = true
	server, _ := newWebSocketServer(t, appConfig, `echo out; echo err >&2; printf 'x\r\n\ny\n'; printf '\377\n'`, "")
	conn := dialWebSocket(t, server)

	messages, closeErr := readUntilClose(t, conn, 3*time.Second)
	want := []wsMessage{
		{websocket.TextMessage, "out"},
		{websocket.TextMessage, "err"},
		{websocket.TextMessage, "x"},
		{websocket.TextMessage, ""},
		{websocket.TextMessage, "y"},
		{websocket.BinaryMessage, "\xff"}, // not valid UTF-8
	}
	if !equalMessages(messages, want) {
		t.Errorf("messages: got %q, want %q", messages, want)
	}
	if closeErr.Text != "0" {
		t.Errorf("exit code: got %q, want 0", closeErr.Text)
	}
}

func Test_getWebSocketHandler_inputAfterExit(t *testing.T) {
	server, handlerDone := newWebSocketServer(t, testConfig(0), "echo done", "")
	conn := dialWebSocket(t, server)

	// the command doesn't read stdin, the input must not block the handler
	for range 100 {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 1024))); err != nil {
			break
		}
	}

	messages, closeErr := readUntilClose(t, conn, 3*time.Second)
	if !equalMessages(messages, []wsMessage{{websocket.TextMessage, "done"}}) || closeErr.Text != "0" {
		t.Errorf("got %q, close %q", messages, closeErr.Text)
	}
	waitHandlerDone(t, handlerDone, "after the command has exited")
}

func Test_getWebSocketHandler_clientDisconnectKillsProcessGroup(t *testing.T) {
	for _, withCloseFrame := range []bool{false, true} {
		server, handlerDone := newWebSocketServer(t, testConfig(0), "sleep 10 & echo $!; wait", "")
		conn := dialWebSocket(t, server)
		pid := readPid(t, conn)

		if withCloseFrame {
			closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			if err := conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = conn.Close() // the browser tab is closed
		}

		waitHandlerDone(t, handlerDone, "after client disconnect")
		if !waitProcessGone(pid) {
			t.Errorf("close frame=%v: child process %d is still running after client disconnect", withCloseFrame, pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// sendBigMessages - send messages that the command doesn't read, the pipe buffer of stdin is 64KB
func sendBigMessages(t *testing.T, conn *websocket.Conn, count int) {
	t.Helper()

	message := []byte(strings.Repeat("x", 60*1024))
	for range count {
		if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
			break // the server has closed the connection
		}
	}
}

func Test_getWebSocketHandler_clientDisconnectWithFullStdin(t *testing.T) {
	server, handlerDone := newWebSocketServer(t, testConfig(0), "sleep 10 & echo $!; wait", "")
	conn := dialWebSocket(t, server)
	pid := readPid(t, conn)

	// more than the pipe buffer, but less than the queue: the command keeps running
	sendBigMessages(t, conn, 10)
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	if err := conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	waitHandlerDone(t, handlerDone, "after client disconnect with a full stdin")
	if !waitProcessGone(pid) {
		t.Errorf("child process %d is still running after client disconnect", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func Test_getWebSocketHandler_stdinQueueOverflow(t *testing.T) {
	server, handlerDone := newWebSocketServer(t, testConfig(0), "sleep 10 & echo $!; wait", "")
	conn := dialWebSocket(t, server)
	pid := readPid(t, conn)

	sendBigMessages(t, conn, stdinQueueSize+20)

	_, closeErr := readUntilClose(t, conn, 3*time.Second)
	if closeErr.Text != "-1" {
		t.Errorf("exit code: got %q, want -1 (killed)", closeErr.Text)
	}
	waitHandlerDone(t, handlerDone, "on stdin queue overflow")
	if !waitProcessGone(pid) {
		t.Errorf("child process %d is still running after stdin queue overflow", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func Test_getWebSocketHandler_shutdownKillsCommand(t *testing.T) {
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	appConfig := testConfig(0)
	appConfig.shutdownCtx = shutdownCtx
	server, handlerDone := newWebSocketServer(t, appConfig, "sleep 10 & echo $!; wait", "")
	conn := dialWebSocket(t, server)
	pid := readPid(t, conn)

	shutdown() // Ctrl-C

	// the connection is closed properly, with the exit code of the killed command
	_, closeErr := readUntilClose(t, conn, 3*time.Second)
	if closeErr.Text != "-1" {
		t.Errorf("exit code: got %q, want -1 (killed)", closeErr.Text)
	}
	waitHandlerDone(t, handlerDone, "on shutdown")
	if !waitProcessGone(pid) {
		t.Errorf("child process %d is still running after shutdown", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func Test_getWebSocketHandler_timeout(t *testing.T) {
	t.Parallel()
	server, _ := newWebSocketServer(t, testConfig(1), "echo started; sleep 10", "")
	conn := dialWebSocket(t, server)

	start := time.Now()
	messages, closeErr := readUntilClose(t, conn, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("command was not stopped by timeout, took %s", elapsed)
	}
	if !equalMessages(messages, []wsMessage{{websocket.TextMessage, "started"}}) || closeErr.Text != "-1" {
		t.Errorf("got %q, close %q", messages, closeErr.Text)
	}
}

func Test_getWebSocketHandler_stalledReaderIsClosedOnTimeout(t *testing.T) {
	t.Parallel()
	server, handlerDone := newWebSocketServer(t, testConfig(1), "yes", "")
	// the client doesn't read the output
	_ = dialWebSocket(t, server)

	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return for a client that doesn't read")
	}
}

func Test_getWebSocketHandler_handshake(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.setForm = true
	appConfig.maxUploadSize = 10
	server, _ := newWebSocketServer(t, appConfig, "echo should not run", "")

	handshake := func(extra http.Header) http.Header {
		header := http.Header{
			"Connection": {"Upgrade"}, "Upgrade": {"websocket"},
			"Sec-Websocket-Version": {"13"}, "Sec-Websocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="},
		}
		maps.Copy(header, extra)
		return header
	}

	tests := []struct {
		name       string
		method     string
		header     http.Header
		body       string
		wantStatus int
	}{
		{
			// with -form the body would be parsed before the upgrade, bypassing SH_MAX_UPLOAD_SIZE
			name:       "handshake with a body",
			method:     "GET",
			header:     handshake(http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}),
			body:       "a=" + strings.Repeat("x", 100),
			wantStatus: http.StatusBadRequest,
		},
		{name: "not a websocket", method: "GET", wantStatus: http.StatusBadRequest},
		{name: "POST", method: "POST", wantStatus: http.StatusMethodNotAllowed},
		{
			name:       "cross-origin",
			method:     "GET",
			header:     handshake(http.Header{"Origin": {"http://evil.example.com"}}),
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req, err := http.NewRequest(tt.method, server.URL, body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header = tt.header
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != tt.wantStatus {
				t.Errorf("status: got %d, want %d", res.StatusCode, tt.wantStatus)
			}
		})
	}

	// the same origin is allowed
	conn, res, err := websocket.DefaultDialer.Dial(wsURL(server), http.Header{"Origin": {server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	defer conn.Close()
	if messages, _ := readUntilClose(t, conn, 3*time.Second); len(messages) != 1 {
		t.Errorf("got %q", messages)
	}
}

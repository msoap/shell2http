//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/msoap/raphanus"
)

func newStreamServer(t *testing.T, appConfig Config, cmd string, mode outputMode) (*httptest.Server, chan struct{}) {
	t.Helper()

	shell, params, err := getShellAndParams(cmd, appConfig)
	if err != nil {
		t.Fatal(err)
	}

	handlerDone := make(chan struct{}, 1)
	handler := mwLogging(getStreamHandler(appConfig, shell, params, mode)) // flush must work through the middleware
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		handler(rw, req)
		handlerDone <- struct{}{}
	}))
	t.Cleanup(server.Close)

	return server, handlerDone
}

func Test_getStreamHandler_outputBeforeExit(t *testing.T) {
	t.Parallel()
	server, _ := newStreamServer(t, testConfig(0), "echo first; sleep 1; echo second; exit 3", outputStream)

	start := time.Now()
	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if got := res.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type: got %q", got)
	}

	reader := bufio.NewReader(res.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); line != "first\n" || elapsed > 700*time.Millisecond {
		t.Errorf("first line: got %q after %s, want it before the command exits", line, elapsed)
	}

	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "second\n" {
		t.Errorf("rest of output: got %q", rest)
	}
	if got := res.Trailer.Get(exitCodeHeader); got != "3" {
		t.Errorf("exit code trailer: got %q, want 3", got)
	}
}

func Test_getStreamHandler_includeStderr(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.includeStderr = true
	server, _ := newStreamServer(t, appConfig, "echo out; echo err >&2", outputStream)

	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "out\nerr\n" {
		t.Errorf("got %q", body)
	}
}

func Test_getStreamHandler_SSE(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.includeStderr = true
	server, _ := newStreamServer(t, appConfig, `echo a; sleep 0.1; echo err >&2; sleep 0.1; printf 'x\ry\r\n\n'; printf last; exit 2`, outputSSE)

	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	if got := res.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type: got %q", got)
	}
	want := "data: a\n\n" +
		"event: stderr\ndata: err\n\n" +
		"data: x\ndata: y\n\n" +
		"data: \n\n" +
		"data: last\n\n" +
		"event: exit\ndata: 2\n\n"
	if string(body) != want {
		t.Errorf("got:\n%q\nwant:\n%q", body, want)
	}
}

func Test_getStreamHandler_clientDisconnectKillsProcessGroup(t *testing.T) {
	for _, mode := range []outputMode{outputStream, outputSSE} {
		server, handlerDone := newStreamServer(t, testConfig(0), "sleep 10 & echo $!; wait", mode)

		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		line, err := bufio.NewReader(res.Body).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), "data: "))
		if err != nil {
			t.Fatalf("mode=%d: bad pid %q: %v", mode, line, err)
		}

		cancel() // client has gone
		res.Body.Close()

		select {
		case <-handlerDone:
		case <-time.After(3 * time.Second):
			t.Fatalf("mode=%d: handler did not return after client disconnect", mode)
		}

		if !waitProcessGone(pid) {
			t.Errorf("mode=%d: child process %d is still running after client disconnect", mode, pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// waitProcessGone - wait until the process has exited
func waitProcessGone(pid int) bool {
	for range 30 {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func Test_getStreamHandler_shutdownKillsCommand(t *testing.T) {
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	appConfig := testConfig(0)
	appConfig.shutdownCtx = shutdownCtx
	server, handlerDone := newStreamServer(t, appConfig, "sleep 10 & echo $!; wait", outputStream)

	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	reader := bufio.NewReader(res.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("bad pid %q: %v", line, err)
	}

	shutdown() // Ctrl-C

	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return on shutdown")
	}
	// the response is finished properly, with the exit code of the killed command
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	if got := res.Trailer.Get(exitCodeHeader); got != "-1" {
		t.Errorf("exit code trailer: got %q, want -1 (killed)", got)
	}
	if !waitProcessGone(pid) {
		t.Errorf("child process %d is still running after shutdown", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func Test_execShellCommand_shutdownKillsCommandWithTimeout(t *testing.T) {
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	appConfig := testConfig(10)
	appConfig.shutdownCtx = shutdownCtx
	shell, params, err := getShellAndParams("sleep 10; echo done", appConfig)
	if err != nil {
		t.Fatal(err)
	}

	time.AfterFunc(200*time.Millisecond, shutdown)
	start := time.Now()
	out, _, err := execShellCommand(appConfig, shell, params, httptest.NewRequest("GET", "/", nil), raphanus.New())
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("command was not killed on shutdown, took %s", elapsed)
	}
	if err == nil || strings.Contains(string(out), "done") {
		t.Errorf("expected killed command, got out=%q, err=%v", out, err)
	}
}

func Test_getStreamHandler_CGIBody(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.setCGI = true
	server, _ := newStreamServer(t, appConfig, "echo started; wc -c", outputStream)

	// bigger than net/http discards after the response headers are flushed (without full duplex)
	body := bytes.Repeat([]byte("x"), 1<<20)
	res, err := http.Post(server.URL, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Fields(string(out))
	if len(lines) != 2 || lines[1] != strconv.Itoa(len(body)) {
		t.Errorf("got %q, want the whole body (%d bytes) on stdin", out, len(body))
	}
}

// dialStalled - send the raw request and don't read the response
func dialStalled(t *testing.T, server *httptest.Server, rawRequest string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := io.WriteString(conn, rawRequest); err != nil {
		t.Fatal(err)
	}

	return conn
}

func Test_getStreamHandler_stalledUpload(t *testing.T) {
	t.Parallel()
	appConfig := testConfig(0)
	appConfig.setCGI = true
	server, handlerDone := newStreamServer(t, appConfig, "echo started", outputStream)

	// the body is announced, but never sent, the client stays connected
	conn := dialStalled(t, server, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n")

	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler is stuck on reading the request body after the command has exited")
	}

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "started\n" {
		t.Errorf("got body=%q, err=%v", body, err)
	}
}

func Test_getStreamHandler_stalledReaderTimeout(t *testing.T) {
	t.Parallel()
	for _, mode := range []outputMode{outputStream, outputSSE} {
		server, handlerDone := newStreamServer(t, testConfig(1), "yes", mode)

		// the client stays connected, but doesn't read the endless output
		_ = dialStalled(t, server, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")

		select {
		case <-handlerDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("mode=%d: handler is stuck on writing the output after the command has been killed by timeout", mode)
		}
	}
}

func Test_shellCommand_launchFailureExitCode(t *testing.T) {
	appConfig := testConfig(0)
	out, exitCode, err := execShellCommand(appConfig, "/nonexistent/shell", nil, httptest.NewRequest("GET", "/", nil), raphanus.New())
	if err == nil || exitCode != -1 || len(out) != 0 {
		t.Errorf("got out=%q, exit code=%d, err=%v, want exit code -1 and an error", out, exitCode, err)
	}
}

func Test_getStreamHandler_timeout(t *testing.T) {
	t.Parallel()
	server, _ := newStreamServer(t, testConfig(1), "echo started; sleep 10", outputStream)

	start := time.Now()
	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("stream was not stopped by timeout, took %s", elapsed)
	}
	if string(body) != "started\n" {
		t.Errorf("got %q", body)
	}
	if got := res.Trailer.Get(exitCodeHeader); got != "-1" {
		t.Errorf("exit code trailer: got %q, want -1 (killed)", got)
	}
}

func Test_lineWriter(t *testing.T) {
	var lines []string
	lw := &lineWriter{fn: func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	}}

	long := bytes.Repeat([]byte("x"), maxLineSize+10)
	// a long line with "\n" in the same write, "я" (2 bytes) crosses the size limit
	longWithNewline := []byte(strings.Repeat("y", maxLineSize-11) + "я" + strings.Repeat("y", 5) + "\n")
	for _, chunk := range [][]byte{[]byte("a\r\nb"), []byte("c\n\n"), long, longWithNewline, []byte("tail")} {
		if n, err := lw.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("write: n=%d, err=%v", n, err)
		}
	}
	if err := lw.flush(); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"a", "bc", "",
		strings.Repeat("x", maxLineSize),
		strings.Repeat("x", 10) + strings.Repeat("y", maxLineSize-11), // split before "я", not in the middle of it
		"я" + strings.Repeat("y", 5),
		"tail",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want[i])
		}
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"golang.org/x/net/publicsuffix"
)

// maxMessageSize - max size of a WebSocket message from the client, a bigger message closes the connection
const maxMessageSize = maxLineSize

// getWebSocketHandler - get handler function for a command connected to the client by WebSocket (endpoint option websocket=1):
// each message from the client is written to stdin with "\n" appended, each line of the output is sent as a message,
// when the command has finished the connection is closed with the exit code as the close reason
func getWebSocketHandler(appConfig Config, shell string, params []string, allowOrigin string) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: waitDelay,
		CheckOrigin:      originChecker(allowOrigin),
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		// check the handshake before the command is prepared,
		// a request body is not expected (with -cgi it would be written to stdin)
		if req.Method != http.MethodGet {
			rw.Header().Set("Allow", http.MethodGet)
			http.Error(rw, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !websocket.IsWebSocketUpgrade(req) {
			http.Error(rw, "WebSocket handshake expected", http.StatusBadRequest)
			return
		}
		if !upgrader.CheckOrigin(req) {
			log.Printf("websocket: origin %q is not allowed", req.Header.Get("Origin"))
			http.Error(rw, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		if appConfig.hijacked != nil {
			appConfig.hijacked.Add(1)
			defer appConfig.hijacked.Done()
		}

		// the command is killed when the client has gone, on timeout and on shutdown of the server,
		// a hijacked connection doesn't cancel the request context, the client is watched by reading messages
		ctx, cancelFn := context.WithCancel(req.Context())
		defer cancelFn()
		stopOnShutdown := context.AfterFunc(appConfig.commandContext(), cancelFn)
		defer stopOnShutdown()

		if appConfig.timeout > 0 {
			var cancelTimeout context.CancelFunc
			ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(appConfig.timeout)*time.Second)
			defer cancelTimeout()
		}

		shellCmd, err := newShellCommand(ctx, appConfig, shell, params, req)
		if err != nil {
			if !writeFormError(rw, err) {
				log.Printf("prepare command failed: %s", err)
				http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
			return
		}

		conn, err := upgrader.Upgrade(rw, req, nil)
		if err != nil {
			// the error response is already sent
			log.Printf("websocket: handshake failed: %s", err)
			shellCmd.finalizer()
			return
		}
		defer func() { _ = conn.Close() }()
		conn.SetReadLimit(maxMessageSize)

		stdin, err := shellCmd.cmd.StdinPipe()
		if err != nil {
			log.Printf("websocket: stdin of the command failed: %s", err)
			shellCmd.finalizer()
			closeMsg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "")
			_ = conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(waitDelay))
			return
		}

		// when the command is killed, a write to a client that doesn't read must not block forever,
		// the deadline of gorilla/websocket can't be changed concurrently with a write, so the connection is closed
		stopCloseOnKill := context.AfterFunc(ctx, func() {
			time.AfterFunc(waitDelay, func() { _ = conn.NetConn().Close() })
		})
		defer stopCloseOnKill()

		// the client has closed the connection or doesn't accept the output
		var clientGone atomic.Bool

		// messages from the client to stdin, until the client has closed the connection
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			writeStdin := true
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					if errors.Is(err, websocket.ErrReadLimit) {
						log.Printf("websocket: a message from the client is bigger than %d bytes", maxMessageSize)
					}
					break
				}
				if writeStdin {
					// the command has exited or closed stdin: the rest of the input is discarded
					_, err = stdin.Write(append(message, '\n'))
					writeStdin = err == nil
				}
			}
			clientGone.Store(true)
			cancelFn()
		}()

		stdout := &lineWriter{fn: func(line []byte) error {
			messageType := websocket.TextMessage
			if !utf8.Valid(line) {
				// a text message must be valid UTF-8
				messageType = websocket.BinaryMessage
			}
			if err := conn.WriteMessage(messageType, line); err != nil {
				clientGone.Store(true)
				cancelFn()
				return err
			}
			return nil
		}}
		stderrLog := &lineWriter{fn: func(line []byte) error {
			log.Printf("stderr: %s", line)
			return nil
		}}
		var stderr io.Writer = stderrLog
		if appConfig.includeStderr {
			// the same writer for stdout and stderr: exec.Cmd uses one pipe for both
			// and writes from one goroutine, so messages are not written concurrently
			stderr = stdout
		}

		exitCode, err := shellCmd.run(stdout, stderr)
		// the last line without a line break
		_ = stdout.flush()
		_ = stderrLog.flush()

		// a client disconnect is the usual end of an endless command, don't log it as an error
		if err != nil && !clientGone.Load() {
			log.Printf("exec error: %s", err)
		}

		// the exit code is sent as the close reason, then wait for the client to confirm the close
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, strconv.Itoa(exitCode))
		if conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(waitDelay)) == nil {
			select {
			case <-readerDone:
			case <-time.After(waitDelay):
			}
		}

		_ = conn.Close()
		<-readerDone
	}
}

// checkAllowOrigin - check the value of the allow-origin endpoint option
func checkAllowOrigin(value string) error {
	switch value {
	case allowOriginSameOrigin, allowOriginSameSite, allowOriginAll:
		return nil
	}

	for origin := range strings.SplitSeq(value, ",") {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%q is not an origin (scheme://host[:port]), supported: %s, %s, %s or a comma-separated list of origins",
				origin, allowOriginSameOrigin, allowOriginSameSite, allowOriginAll)
		}
	}

	return nil
}

// originChecker - get the function that checks the Origin header of a WebSocket handshake (the allow-origin option),
// a request without Origin is not from a browser and is always allowed
func originChecker(allowOrigin string) func(*http.Request) bool {
	var isAllowed func(origin *url.URL, req *http.Request) bool

	switch allowOrigin {
	case allowOriginAll:
		return func(*http.Request) bool { return true }
	case "", allowOriginSameOrigin:
		isAllowed = func(origin *url.URL, req *http.Request) bool {
			return strings.EqualFold(origin.Host, req.Host)
		}
	case allowOriginSameSite:
		isAllowed = func(origin *url.URL, req *http.Request) bool {
			return siteOf(origin.Host) == siteOf(req.Host)
		}
	default:
		origins := strings.Split(allowOrigin, ",")
		isAllowed = func(origin *url.URL, _ *http.Request) bool {
			return slices.ContainsFunc(origins, func(allowed string) bool {
				return strings.EqualFold(allowed, origin.Scheme+"://"+origin.Host)
			})
		}
	}

	return func(req *http.Request) bool {
		values := req.Header["Origin"]
		if len(values) == 0 {
			return true
		}

		origin, err := url.Parse(values[0])
		if err != nil || origin.Host == "" {
			// eg: "null" from a sandboxed page or a local file
			return false
		}

		return isAllowed(origin, req)
	}
}

// siteOf - get the site (registrable domain, eg: example.com for api.example.com) of the host with optional port,
// an IP address, localhost or a public suffix is the site itself
func siteOf(hostPort string) string {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))

	if net.ParseIP(host) != nil {
		return host
	}
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}

	return host
}

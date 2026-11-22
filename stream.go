package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// maxLineSize - in the SSE mode a longer line without a line break is split into several events
const maxLineSize = 64 * 1024

// getStreamHandler - get handler function for a command with the output sent to the client as it is written
// (endpoint options stream=1 or sse=1)
func getStreamHandler(appConfig Config, shell string, params []string, mode outputMode) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		limitFormBody(rw, req, appConfig)

		// unlike the buffered mode, the command is killed when the client has gone,
		// the output may be endless (eg: "tail -f"), and on shutdown of the server
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

		rc := http.NewResponseController(rw)
		header := rw.Header()
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Accel-Buffering", "no") // don't buffer the response in nginx
		switch mode {
		case outputSSE:
			header.Set("Content-Type", "text/event-stream")
		default:
			header.Set("Content-Type", "text/plain; charset=utf-8")
			// browsers buffer the beginning of the response to sniff the content type
			header.Set("X-Content-Type-Options", "nosniff")
			// the exit code is known only after the output, so it is sent as a trailer
			header.Set("Trailer", exitCodeHeader)
		}

		// send the headers at once, the command may not print anything for a long time
		rw.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil {
			log.Printf("flush response failed: %s", err)
		}

		stderrLog := &lineWriter{fn: func(line []byte) error {
			log.Printf("stderr: %s", line)
			return nil
		}}

		var exitCode int
		switch mode {
		case outputSSE:
			events := &sseWriter{w: rw, rc: rc}
			stdout, stderr := events.lines(""), stderrLog
			if appConfig.includeStderr {
				stderr = events.lines("stderr")
			}

			exitCode, err = shellCmd.run(stdout, stderr)
			// the last line without a line break
			_ = stdout.flush()
			_ = stderr.flush()

			_ = events.send("exit", []byte(strconv.Itoa(exitCode)))
		default:
			stdout := &flushWriter{w: rw, rc: rc}
			var stderr io.Writer = stderrLog
			if appConfig.includeStderr {
				stderr = stdout
			}

			exitCode, err = shellCmd.run(stdout, stderr)
			_ = stderrLog.flush()

			rw.Header().Set(exitCodeHeader, strconv.Itoa(exitCode))
		}

		// a client disconnect is the usual end of an endless stream, don't log it as an error
		if err != nil && req.Context().Err() == nil {
			log.Printf("exec error: %s", err)
		}
	}
}

// flushWriter - write to the response and send it to the client at once
type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (fw *flushWriter) Write(data []byte) (int, error) {
	n, err := fw.w.Write(data)
	if err != nil {
		return n, err
	}

	return n, fw.rc.Flush()
}

// sseWriter - send Server-Sent Events to the client, safe for concurrent use (stdout and stderr are written concurrently)
type sseWriter struct {
	mu sync.Mutex
	w  io.Writer
	rc *http.ResponseController
}

// send - send one event, "" - default event type ("message"), each line of data is sent in its own "data:" field
func (sw *sseWriter) send(event string, data []byte) error {
	var msg bytes.Buffer
	if event != "" {
		msg.WriteString("event: ")
		msg.WriteString(event)
		msg.WriteByte('\n')
	}
	// CR is a line break in the event stream too
	for line := range bytes.SplitSeq(bytes.ReplaceAll(data, []byte("\r"), []byte("\n")), []byte("\n")) {
		msg.WriteString("data: ")
		msg.Write(line)
		msg.WriteByte('\n')
	}
	msg.WriteByte('\n')

	sw.mu.Lock()
	defer sw.mu.Unlock()

	if _, err := sw.w.Write(msg.Bytes()); err != nil {
		return err
	}
	return sw.rc.Flush()
}

// lines - get a writer that sends each line of the output as an event
func (sw *sseWriter) lines(event string) *lineWriter {
	return &lineWriter{fn: func(line []byte) error {
		return sw.send(event, line)
	}}
}

// lineWriter - split the output into lines (without "\n" or "\r\n") and call fn for each line,
// a line longer than maxLineSize is split, call flush after the last write for the rest of the output
type lineWriter struct {
	buf []byte
	fn  func(line []byte) error
}

func (lw *lineWriter) Write(data []byte) (int, error) {
	lw.buf = append(lw.buf, data...)

	for {
		end := bytes.IndexByte(lw.buf, '\n')
		next := end + 1
		if end < 0 {
			if len(lw.buf) < maxLineSize {
				return len(data), nil
			}
			end, next = maxLineSize, maxLineSize
		}

		line := bytes.TrimSuffix(lw.buf[:end], []byte("\r"))
		lw.buf = lw.buf[next:]
		if err := lw.fn(line); err != nil {
			return 0, err
		}
	}
}

// flush - pass the rest of the output (the last line without a line break) to fn
func (lw *lineWriter) flush() error {
	if len(lw.buf) == 0 {
		return nil
	}

	line := lw.buf
	lw.buf = nil
	return lw.fn(line)
}

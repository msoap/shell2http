package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// mwMultiMethod - produce handler for several http methods
func mwMultiMethod(in map[string]http.HandlerFunc) (http.HandlerFunc, error) {
	switch len(in) {
	case 0:
		return nil, fmt.Errorf("requires at least one handler")
	case 1:
		for method, handler := range in {
			return mwMethodOnly(handler, method), nil
		}
	}

	for method := range in {
		if method == "" {
			return nil, fmt.Errorf("mixing predetermined HTTP method with empty is not allowed")
		}
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		for method, handler := range in {
			if req.Method == method {
				handler.ServeHTTP(rw, req)
				return
			}
		}

		// not matched http method
		http.Error(rw, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}, nil
}

// mwMethodOnly - allow one HTTP method only
func mwMethodOnly(handler http.HandlerFunc, method string) http.HandlerFunc {
	if method == "" {
		return handler
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		if req.Method == method {
			handler.ServeHTTP(rw, req)
		} else {
			http.Error(rw, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	}
}

// mwBasicAuth - add HTTP Basic Authentication
func mwBasicAuth(handler http.HandlerFunc, users authUsers) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		reqUser, reqPass, ok := req.BasicAuth()
		if !ok || !users.isAllow(reqUser, reqPass) {
			rw.Header().Set("WWW-Authenticate", `Basic realm="Please enter user and password"`)
			http.Error(rw, "name/password is required", http.StatusUnauthorized)
			return
		}

		handler.ServeHTTP(rw, req)
	}
}

// mwLogging - add logging for handler
func mwLogging(handler http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		remoteAddr := req.RemoteAddr
		if realIP, ok := req.Header["X-Real-Ip"]; ok && len(realIP) > 0 {
			remoteAddr = realIP[0] + ", " + remoteAddr
		}
		rwLogger := &responseWriterLogger{srcRW: rw}
		start := time.Now()
		handler.ServeHTTP(rwLogger, req)
		// the user name comes from the client (base64-decoded, may contain newlines), quote it
		userPrefix := ""
		if reqUser, _, ok := req.BasicAuth(); ok {
			userPrefix = fmt.Sprintf("%q ", reqUser)
		}
		log.Printf(`%s%s %s "%s %s" %d %d "%s" %s`,
			userPrefix,
			req.Host, remoteAddr,
			req.Method, req.RequestURI,
			rwLogger.StatusCode(), rwLogger.Size(),
			req.UserAgent(),
			time.Since(start).Round(time.Millisecond),
		)
	}
}

// mwCommonHeaders - set common headers
func mwCommonHeaders(handler http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Server", fmt.Sprintf("shell2http %s", version))
		handler.ServeHTTP(rw, req)
	}
}

// mwOneThread - run handler in one thread
func mwOneThread(handler http.HandlerFunc) http.HandlerFunc {
	mutex := sync.Mutex{}

	return func(rw http.ResponseWriter, req *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		handler.ServeHTTP(rw, req)
	}
}

// responseWriterLogger - wrapper around http.ResponseWriter
type responseWriterLogger struct {
	srcRW      http.ResponseWriter
	statusCode int
	size       int
}

func (rwl *responseWriterLogger) Header() http.Header {
	return rwl.srcRW.Header()
}

func (rwl *responseWriterLogger) Write(data []byte) (int, error) {
	rwl.size += len(data)
	return rwl.srcRW.Write(data)
}

func (rwl *responseWriterLogger) WriteHeader(statusCode int) {
	rwl.statusCode = statusCode
	rwl.srcRW.WriteHeader(statusCode)
}

// Unwrap - get the original http.ResponseWriter, used by http.ResponseController (eg: Flush in the stream mode)
func (rwl *responseWriterLogger) Unwrap() http.ResponseWriter {
	return rwl.srcRW
}

// Hijack - take over the connection (the websocket mode), gorilla/websocket requires http.Hijacker, not Unwrap
func (rwl *responseWriterLogger) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(rwl.srcRW).Hijack()
	if err == nil {
		rwl.statusCode = http.StatusSwitchingProtocols
	}
	return conn, brw, err
}

func (rwl *responseWriterLogger) StatusCode() int {
	if rwl.statusCode == 0 {
		return http.StatusOK
	}
	return rwl.statusCode
}

func (rwl *responseWriterLogger) Size() int {
	return rwl.size
}

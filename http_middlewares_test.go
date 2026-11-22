package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func Test_mwLogging_noInjection(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	handler := mwLogging(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/ok", nil)
	req.SetBasicAuth("x\nFORGED LINE", "pw")
	handler(httptest.NewRecorder(), req)

	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("log must be one line, got: %q", out)
	}
	if !strings.Contains(out, `"x\nFORGED LINE" `) {
		t.Fatalf("user name must be quoted, got: %q", out)
	}
}

func Test_mwOneThread_unlockOnPanic(t *testing.T) {
	calls := 0
	handler := mwOneThread(func(rw http.ResponseWriter, req *http.Request) {
		calls++
		if calls == 1 {
			panic("handler failed")
		}
		rw.WriteHeader(http.StatusOK)
	})

	func() {
		defer func() { _ = recover() }()
		handler(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()

	done := make(chan struct{})
	go func() {
		handler(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("endpoint is locked after handler panic")
	}
}

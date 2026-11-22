package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

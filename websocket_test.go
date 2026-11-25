package main

import (
	"net/http/httptest"
	"testing"
)

func Test_originChecker(t *testing.T) {
	tests := []struct {
		allowOrigin string
		host        string
		origin      string // "" - no Origin header
		want        bool
	}{
		{allowOrigin: "", host: "localhost:8080", origin: "", want: true},
		{allowOrigin: "", host: "localhost:8080", origin: "http://localhost:8080", want: true},
		{allowOrigin: "", host: "localhost:8080", origin: "https://LOCALHOST:8080", want: true},
		{allowOrigin: "", host: "localhost:8080", origin: "http://localhost:8081", want: false},
		{allowOrigin: "", host: "localhost:8080", origin: "http://evil.com", want: false},
		{allowOrigin: "", host: "localhost:8080", origin: "null", want: false},
		{allowOrigin: "same-origin", host: "api.example.com", origin: "http://www.example.com", want: false},

		{allowOrigin: "same-site", host: "api.example.com:8080", origin: "https://www.example.com", want: true},
		{allowOrigin: "same-site", host: "example.com", origin: "https://a.b.example.com", want: true},
		{allowOrigin: "same-site", host: "api.example.com", origin: "https://example.org", want: false},
		{allowOrigin: "same-site", host: "api.example.co.uk", origin: "https://www.example.co.uk", want: true},
		{allowOrigin: "same-site", host: "example.co.uk", origin: "https://other.co.uk", want: false},
		{allowOrigin: "same-site", host: "me.github.io", origin: "https://evil.github.io", want: false}, // a private public suffix
		{allowOrigin: "same-site", host: "127.0.0.1:8080", origin: "http://127.0.0.1:3000", want: true},
		{allowOrigin: "same-site", host: "127.0.0.1:8080", origin: "http://127.0.0.2:8080", want: false},
		{allowOrigin: "same-site", host: "[::1]:8080", origin: "http://[::1]:3000", want: true},
		{allowOrigin: "same-site", host: "localhost:8080", origin: "http://localhost:3000", want: true},
		{allowOrigin: "same-site", host: "localhost:8080", origin: "null", want: false},

		{allowOrigin: "*", host: "localhost", origin: "http://evil.com", want: true},
		{allowOrigin: "*", host: "localhost", origin: "null", want: true},

		{allowOrigin: "https://app.example.com,http://localhost:3000", host: "api.example.com", origin: "https://app.example.com", want: true},
		{allowOrigin: "https://app.example.com,http://localhost:3000", host: "api.example.com", origin: "http://localhost:3000", want: true},
		{allowOrigin: "https://app.example.com,http://localhost:3000", host: "api.example.com", origin: "http://app.example.com", want: false},
		{allowOrigin: "https://app.example.com,http://localhost:3000", host: "api.example.com", origin: "https://api.example.com", want: false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tt.host
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		if got := originChecker(tt.allowOrigin)(req); got != tt.want {
			t.Errorf("allow-origin=%q, Host: %q, Origin: %q: got %v, want %v", tt.allowOrigin, tt.host, tt.origin, got, tt.want)
		}
	}
}

func Test_checkAllowOrigin(t *testing.T) {
	for _, value := range []string{"same-origin", "same-site", "*", "https://example.com", "http://localhost:3000,https://a.example.com"} {
		if err := checkAllowOrigin(value); err != nil {
			t.Errorf("%q: unexpected error: %v", value, err)
		}
	}

	for _, value := range []string{"", "example.com", "https://example.com/", "https://example.com/path", "https://example.com?a=1", "https://u@example.com", "https://a.com,", "null", "same-host"} {
		if err := checkAllowOrigin(value); err == nil {
			t.Errorf("%q: expected an error", value)
		}
	}
}

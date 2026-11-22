//go:build unix

package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/msoap/raphanus"
)

func Test_formValue(t *testing.T) {
	digits := regexp.MustCompile(`^[0-9]+$`)
	digitsList := regexp.MustCompile(`^[0-9,]+$`)

	tests := []struct {
		name    string
		values  []string
		re      *regexp.Regexp
		want    string
		wantSet bool
	}{
		{"no check", []string{"a", "b"}, nil, "a,b", true},
		{"one valid", []string{"12"}, digits, "12", true},
		{"one invalid", []string{"1x"}, digits, "", false},
		{"invalid dropped", []string{"12", "1x"}, digits, "12", true},
		{"joined value forbidden by regexp", []string{"1", "2"}, digits, "", false},
		{"joined value allowed by regexp", []string{"1", "2"}, digitsList, "1,2", true},
		{"empty", nil, digits, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotSet := formValue(tt.values, tt.re)
			if got != tt.want || gotSet != tt.wantSet {
				t.Errorf("formValue(%q) = %q, %v, want %q, %v", tt.values, got, gotSet, tt.want, tt.wantSet)
			}
		})
	}
}

func uploadRequest(t *testing.T, fileName string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("f", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	return req
}

func formTestConfig(formCheck string) Config {
	appConfig := testConfig(0)
	appConfig.setForm = true
	if formCheck != "" {
		appConfig.formCheckRe = regexp.MustCompile(formCheck)
	}

	return appConfig
}

func Test_execShellCommand_formCheckFileName(t *testing.T) {
	cmd := `echo "name=$filename_f"; test -n "$filepath_f" && echo has_path; true`

	tests := []struct {
		name      string
		formCheck string
		fileName  string
		want      string
	}{
		{"no check", "", "a;b.txt", "name=a;b.txt\nhas_path\n"},
		{"allowed name", `^[\w.]+$`, "ab.txt", "name=ab.txt\nhas_path\n"},
		{"forbidden name skips the file", `^[\w.]+$`, "a;b.txt", "name=\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appConfig := formTestConfig(tt.formCheck)
			shell, params, err := getShellAndParams(cmd, appConfig)
			if err != nil {
				t.Fatal(err)
			}

			out, _, err := execShellCommand(appConfig, shell, params, uploadRequest(t, tt.fileName), raphanus.New())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(out) != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
		})
	}
}

func Test_getShellHandler_formParseErrorFailsClosed(t *testing.T) {
	marker := t.TempDir() + "/ran"
	appConfig := formTestConfig("")
	shell, params, err := getShellAndParams("touch "+marker+"; echo ran", appConfig)
	if err != nil {
		t.Fatal(err)
	}
	handler := getShellHandler(appConfig, shell, params, raphanus.New())

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{"bad urlencoded", "application/x-www-form-urlencoded", "v=%zz", http.StatusBadRequest},
		{"bad multipart", "multipart/form-data; boundary=xxx", "not multipart", http.StatusBadRequest},
		{"body too large", "application/x-www-form-urlencoded", "v=" + strings.Repeat("a", 1024), http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rw := httptest.NewRecorder()
			if tt.wantStatus == http.StatusRequestEntityTooLarge {
				req.Body = http.MaxBytesReader(rw, req.Body, 16)
			}

			handler(rw, req)

			if rw.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rw.Code, tt.wantStatus)
			}
			if strings.Contains(rw.Body.String(), "ran") {
				t.Errorf("command output in response: %q", rw.Body.String())
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("command was run")
			}
		})
	}
}

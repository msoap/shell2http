//go:build unix

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/msoap/raphanus"
)

func testConfig(timeout int) Config {
	return Config{timeout: timeout, defaultShell: "sh", defaultShOpt: "-c", shell: "sh"}
}

func Test_execShellCommand_chunkedBodyContentLength(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.setCGI = true
	shell, params, err := getShellAndParams(`echo "len=$CONTENT_LENGTH"; cat`, appConfig)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/", strings.NewReader("chunked body"))
	req.ContentLength = -1 // as for "Transfer-Encoding: chunked"

	out, _, err := execShellCommand(appConfig, shell, params, req, raphanus.New())
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "len=12\nchunked body" {
		t.Errorf("unexpected out: %q", out)
	}
}

func Test_execShellCommand_formBodyNoContentLength(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.setCGI = true
	appConfig.setForm = true
	shell, params, err := getShellAndParams(`echo "len=[$CONTENT_LENGTH] a=[$v_a]"; cat`, appConfig)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/", strings.NewReader("a=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	out, _, err := execShellCommand(appConfig, shell, params, req, raphanus.New())
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "len=[] a=[1]\n" {
		t.Errorf("unexpected out: %q", out)
	}
}

func Test_getShellHandler_CGIPathAndServer(t *testing.T) {
	appConfig := testConfig(0)
	appConfig.setCGI = true
	shell, params, err := getShellAndParams(`echo "Content-Type: text/plain"; echo; echo "$SCRIPT_NAME|$PATH_INFO|$SERVER_NAME|$SERVER_PORT"`, appConfig)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sub/", getShellHandler(appConfig, shell, params, raphanus.New()))
	server := httptest.NewServer(mux)
	defer server.Close()

	res, err := http.Get(server.URL + "/sub/extra/path")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	if want := "/sub|/extra/path|127.0.0.1|" + port + "\n"; string(body) != want {
		t.Errorf("got %q, want %q", body, want)
	}
}

func Test_getShellHandler_CGIResponseHeaders(t *testing.T) {
	data := []struct {
		name       string
		cmd        string
		wantStatus int
		wantHeader http.Header
		wantBody   string
	}{
		{
			name:       "Status wins over Location",
			cmd:        `printf "Status: 301\nLocation: /x\n\n"`,
			wantStatus: http.StatusMovedPermanently,
			wantHeader: http.Header{"Location": {"/x"}, "Status": nil},
		},
		{
			name:       "Location without Status",
			cmd:        `printf "Location: /x\n\n"`,
			wantStatus: http.StatusFound,
			wantHeader: http.Header{"Location": {"/x"}},
		},
		{
			name:       "lower case header names",
			cmd:        `printf "status: 404\ncontent-type: text/plain\n\nnot found\n"`,
			wantStatus: http.StatusNotFound,
			wantHeader: http.Header{"Content-Type": {"text/plain"}, "Status": nil},
			wantBody:   "not found\n",
		},
		{
			name:       "repeated headers",
			cmd:        `printf "Set-Cookie: a=1\nSet-Cookie: b=2\n\nok\n"`,
			wantStatus: http.StatusOK,
			wantHeader: http.Header{"Set-Cookie": {"a=1", "b=2"}},
			wantBody:   "ok\n",
		},
		{
			name:       "empty header value",
			cmd:        `printf "X-Empty:\nContent-Type: text/plain\n\nbody\n"`,
			wantStatus: http.StatusOK,
			wantHeader: http.Header{"X-Empty": {""}, "Content-Type": {"text/plain"}},
			wantBody:   "body\n",
		},
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, item := range data {
		t.Run(item.name, func(t *testing.T) {
			appConfig := testConfig(0)
			appConfig.setCGI = true
			shell, params, err := getShellAndParams(item.cmd, appConfig)
			if err != nil {
				t.Fatal(err)
			}

			for range 20 { // headers come from a map, repeat to catch an order dependency
				server := httptest.NewServer(http.HandlerFunc(getShellHandler(appConfig, shell, params, raphanus.New())))
				res, err := client.Get(server.URL)
				if err != nil {
					server.Close()
					t.Fatal(err)
				}
				body, err := io.ReadAll(res.Body)
				res.Body.Close()
				server.Close()
				if err != nil {
					t.Fatal(err)
				}

				if res.StatusCode != item.wantStatus {
					t.Fatalf("status: got %d, want %d", res.StatusCode, item.wantStatus)
				}
				for key, want := range item.wantHeader {
					if got := res.Header.Values(key); !reflect.DeepEqual(got, want) {
						t.Fatalf("header %s: got %q, want %q", key, got, want)
					}
				}
				if string(body) != item.wantBody {
					t.Fatalf("body: got %q, want %q", body, item.wantBody)
				}
			}
		})
	}
}

func Test_execShellCommand_timeoutKillsProcessGroup(t *testing.T) {
	appConfig := testConfig(1)
	shell, params, err := getShellAndParams("sleep 5; echo done", appConfig)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, _, err := execShellCommand(appConfig, shell, params, httptest.NewRequest("GET", "/", nil), raphanus.New())
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "killed") {
		t.Errorf("expected killed error, got: %v", err)
	}
	if strings.Contains(string(out), "done") {
		t.Errorf("command was not killed, out: %q", out)
	}
	if elapsed > 3*time.Second {
		t.Errorf("request blocked for %s, expected about 1s", elapsed)
	}
}

func Test_execShellCommand_timeoutBackgroundHoldsOutput(t *testing.T) {
	appConfig := testConfig(10)
	shell, params, err := getShellAndParams("sleep 5 & echo started", appConfig)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, exitCode, err := execShellCommand(appConfig, shell, params, httptest.NewRequest("GET", "/", nil), raphanus.New())
	elapsed := time.Since(start)

	if err != nil || exitCode != 0 || string(out) != "started\n" {
		t.Errorf("unexpected result: out=%q, exit code=%d, err=%v", out, exitCode, err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("request blocked for %s by background process", elapsed)
	}
}

func Test_execShellCommand_detachedProcessSurvives(t *testing.T) {
	for _, timeout := range []int{0, 1} {
		appConfig := testConfig(timeout)
		shell, params, err := getShellAndParams("sleep 5 >/dev/null 2>&1 & echo $!", appConfig)
		if err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		out, _, err := execShellCommand(appConfig, shell, params, httptest.NewRequest("GET", "/", nil), raphanus.New())
		if err != nil {
			t.Fatalf("timeout=%d: %v", timeout, err)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("timeout=%d: request blocked for %s by detached process", timeout, elapsed)
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Fatalf("timeout=%d: bad pid %q: %v", timeout, out, err)
		}

		time.Sleep(time.Duration(timeout)*time.Second + 500*time.Millisecond) // past the timeout
		if err := syscall.Kill(pid, 0); err != nil {
			t.Errorf("timeout=%d: detached process was killed: %v", timeout, err)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func Test_execShellCommand_clientDisconnect(t *testing.T) {
	for _, timeout := range []int{0, 5} {
		appConfig := testConfig(timeout)
		finished := filepath.Join(t.TempDir(), "finished")
		shell, params, err := getShellAndParams("sleep 0.3; touch "+finished+"; echo done", appConfig)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // client has gone
		req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)

		out, _, err := execShellCommand(appConfig, shell, params, req, raphanus.New())
		if err != nil || string(out) != "done\n" {
			t.Errorf("timeout=%d: command was aborted: out=%q, err=%v", timeout, out, err)
		}
		if _, err := os.Stat(finished); err != nil {
			t.Errorf("timeout=%d: script did not finish: %v", timeout, err)
		}
	}
}

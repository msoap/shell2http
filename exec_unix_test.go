//go:build unix

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
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

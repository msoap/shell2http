package main

import (
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func Test_parseCGIHeaders(t *testing.T) {
	data := []struct {
		in      string
		out     string
		headers map[string]string
	}{
		{
			in:      "Some text",
			out:     "Some text",
			headers: map[string]string{},
		},
		{
			in:      "Location: url\n\nSome text",
			out:     "Some text",
			headers: map[string]string{"Location": "url"},
		},
		{
			in:      "Location: url\n\n",
			out:     "",
			headers: map[string]string{"Location": "url"},
		},
		{
			in:      "Location: url\nX-Name:  x-value\n\nSome text",
			out:     "Some text",
			headers: map[string]string{"Location": "url", "X-Name": "x-value"},
		},
	}

	for i, item := range data {
		out, headers := parseCGIHeaders(item.in)
		if !reflect.DeepEqual(item.headers, headers) || item.out != out {
			t.Errorf("%d:\nexpected: %s / %#v\nreal    : %s / %#v", i, item.out, item.headers, out, headers)
		}
	}
}

func Test_getShellAndParams(t *testing.T) {
	shell, params, err := getShellAndParams("ls", "sh", false)
	if shell != "sh" || !reflect.DeepEqual(params, []string{"-c", "ls"}) || err != nil {
		t.Errorf("1. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls", "sh", true)
	if shell != "cmd" || !reflect.DeepEqual(params, []string{"/C", "ls"}) || err != nil {
		t.Errorf("2. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls", "bash", false)
	if shell != "bash" || !reflect.DeepEqual(params, []string{"-c", "ls"}) || err != nil {
		t.Errorf("3. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls -l -a", "", false)
	if shell != "ls" || !reflect.DeepEqual(params, []string{"-l", "-a"}) || err != nil {
		t.Errorf("4. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls -l 'a b'", "", false)
	if shell != "ls" || !reflect.DeepEqual(params, []string{"-l", "a b"}) || err != nil {
		t.Errorf("5. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls '-l", "", false)
	if err == nil {
		t.Errorf("6. getShellAndParams() failed")
	}
}

func httpGet(url string) ([]byte, error) {
	res, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(res.Body)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	return body, nil
}

func getFreePort() string {
	listen, _ := net.Listen("tcp", ":0")
	defer listen.Close()
	parts := strings.Split(listen.Addr().String(), ":")

	return parts[len(parts)-1]
}

func Test_main1(t *testing.T) {
	port := getFreePort()
	os.Args = []string{"shell2http",
		"-add-exit",
		"-cache=1",
		"-cgi",
		"-export-all-vars",
		"-form",
		"-one-thread",
		"-shell=bash",
		"-log=/dev/null",
		"-port=" + port,
		"/echo", "echo 123"}
	go main()

	res, err := httpGet("http://localhost:" + port + "/")
	if err != nil {
		t.Errorf("1. main() failed: %s", err)
	}
	if len(res) == 0 || !strings.HasPrefix(string(res), "<!DOCTYPE html>") {
		t.Errorf("1. main() failed: real result: '%s'", string(res))
	}

	res, err = httpGet("http://localhost:" + port + "/echo")
	if err != nil {
		t.Errorf("2. main() failed: %s", err)
	}
	if string(res) != "123\n" {
		t.Errorf("2. main() failed: real result: '%s'", string(res))
	}
}

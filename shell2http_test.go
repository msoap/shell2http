package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
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
			headers: nil,
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
		{
			in:      "Some text\nText\n\ntext",
			out:     "Some text\nText\n\ntext",
			headers: nil,
		},
		{
			in:      "Some text\nText: value in text\n\ntext",
			out:     "Some text\nText: value in text\n\ntext",
			headers: nil,
		},
		{
			in:      "Text::::\n\ntext",
			out:     "text",
			headers: map[string]string{"Text": ":::"},
		},
		{
			in:      "Text:     :::\n\ntext",
			out:     "text",
			headers: map[string]string{"Text": ":::"},
		},
		{
			in:      "Text:     \n\ntext",
			out:     "Text:     \n\ntext",
			headers: nil,
		},
		{
			in:      "Header: value\nText:     \n\ntext",
			out:     "Header: value\nText:     \n\ntext",
			headers: nil,
		},
		{
			in:      "Location: url\r\nX-Name:  x-value\r\n\r\nOn Windows",
			out:     "On Windows",
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
	shell, params, err := getShellAndParams("ls", Config{shell: "sh", defaultShell: "sh", defaultShOpt: "-c"})
	if shell != "sh" || !reflect.DeepEqual(params, []string{"-c", "ls"}) || err != nil {
		t.Errorf("1. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls", Config{shell: "bash", defaultShell: "sh", defaultShOpt: "-c"})
	if shell != "bash" || !reflect.DeepEqual(params, []string{"-c", "ls"}) || err != nil {
		t.Errorf("3. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls -l -a", Config{shell: "", defaultShell: "sh", defaultShOpt: "-c"})
	if shell != "ls" || !reflect.DeepEqual(params, []string{"-l", "-a"}) || err != nil {
		t.Errorf("4. getShellAndParams() failed")
	}

	shell, params, err = getShellAndParams("ls -l 'a b'", Config{shell: "", defaultShell: "sh", defaultShOpt: "-c"})
	if shell != "ls" || !reflect.DeepEqual(params, []string{"-l", "a b"}) || err != nil {
		t.Errorf("5. getShellAndParams() failed")
	}

	_, _, err = getShellAndParams("ls '-l", Config{shell: "", defaultShell: "sh", defaultShOpt: "-c"})
	if err == nil {
		t.Errorf("6. getShellAndParams() failed")
	}
}

func Test_getShellAndParams_windows(t *testing.T) {
	shell, params, err := getShellAndParams("ls", Config{shell: "cmd", defaultShell: "cmd", defaultShOpt: "/C"})
	if shell != "cmd" || !reflect.DeepEqual(params, []string{"/C", "ls"}) || err != nil {
		t.Errorf("2. getShellAndParams() failed")
	}
}

func httpRequest(method, url, postData string) ([]byte, error) {
	var postDataReader io.Reader
	if method == "POST" && len(postData) > 0 {
		postDataReader = strings.NewReader(postData)
	}

	request, err := http.NewRequest(method, url, postDataReader)
	if err != nil {
		return nil, err
	}

	request.Header.Set("X-Real-Ip", "127.0.0.1")
	client := &http.Client{}
	res, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	err = res.Body.Close()
	if err != nil {
		return nil, err
	}

	return body, nil
}

func getFreePort(t *testing.T) string {
	listen, _ := net.Listen("tcp", ":0")
	parts := strings.Split(listen.Addr().String(), ":")
	err := listen.Close()
	if err != nil {
		t.Errorf("getFreePort() failed")
	}

	return parts[len(parts)-1]
}

func testHTTP(t *testing.T, method, url, postData string, fn func(body string) bool, message string) {
	res, err := httpRequest(method, url, postData)
	if err != nil {
		t.Errorf("%s, get %s failed: %s", message, url, err)
	}
	if !fn(string(res)) {
		t.Errorf("%s failed", message)
	}
}

func Test_main(t *testing.T) {
	port := getFreePort(t)
	os.Args = []string{"shell2http",
		"-add-exit",
		"-cache=1",
		"-cgi",
		// "-export-all-vars",
		"-export-vars=HOME",
		"-one-thread",
		"-shell=",
		"-log=/dev/null",
		"-port=" + port,
		"GET:/echo", "echo 123",
		"POST:/form", "echo var=$v_var",
		"/error", "/ not exists cmd",
		"POST:/post", "cat",
		"/redirect", `echo "Location: /` + "\n" + `"`,
	}
	go main()
	time.Sleep(100 * time.Millisecond) // wait for up http server

	// hide stderr
	oldStderr := os.Stderr // keep backup of the real stderr
	newStderr, err := os.Open("/dev/null")
	if err != nil {
		t.Errorf("open /dev/null: %s", err)
	}
	os.Stderr = newStderr
	defer func() {
		os.Stderr = oldStderr
		err := newStderr.Close()
		if err != nil {
			t.Errorf("Stderr Close failed: %s", err)
		}
	}()

	testHTTP(t, "GET", "http://localhost:"+port+"/", "",
		func(res string) bool { return len(res) > 0 && strings.HasPrefix(res, "<!DOCTYPE html>") },
		"1. get /",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/echo", "",
		func(res string) bool { return res == "123\n" },
		"2. echo",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/echo", "",
		func(res string) bool { return res == "123\n" },
		"3. echo from cache",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/404", "",
		func(res string) bool { return strings.HasPrefix(res, "404 page not found") },
		"4. 404",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/error", "",
		func(res string) bool { return strings.Contains(res, "exec error:") },
		"5. error",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/redirect", "",
		func(res string) bool { return strings.HasPrefix(res, "<!DOCTYPE html>") },
		"6. redirect",
	)

	testHTTP(t, "POST", "http://localhost:"+port+"/post", "X-header: value\n\ntext",
		func(res string) bool { return strings.HasPrefix(res, "text") },
		"7. POST",
	)

	testHTTP(t, "GET", "http://localhost:"+port+"/form", "",
		func(res string) bool {
			return strings.HasPrefix(res, http.StatusText(http.StatusMethodNotAllowed))
		},
		"8. POST with GET",
	)
}

func Test_errChain(t *testing.T) {
	err := errChain()
	if err != nil {
		t.Errorf("1. errChain() empty failed")
	}

	err = errChain(func() error { return nil })
	if err != nil {
		t.Errorf("2. errChain() failed")
	}

	err = errChain(func() error { return nil }, func() error { return nil })
	if err != nil {
		t.Errorf("3. errChain() failed")
	}

	err = errChain(func() error { return fmt.Errorf("error") })
	if err == nil {
		t.Errorf("4. errChain() failed")
	}

	err = errChain(func() error { return nil }, func() error { return fmt.Errorf("error") })
	if err == nil {
		t.Errorf("5. errChain() failed")
	}

	var1 := false
	err = errChain(func() error { return fmt.Errorf("error") }, func() error { var1 = true; return nil })
	if err == nil || var1 {
		t.Errorf("6. errChain() failed")
	}
}

func Test_errChainAll(t *testing.T) {
	err := errChainAll()
	if err != nil {
		t.Errorf("1. errChainAll() empty failed")
	}

	err = errChainAll(func() error { return nil })
	if err != nil {
		t.Errorf("2. errChainAll() failed")
	}

	err = errChainAll(func() error { return nil }, func() error { return nil })
	if err != nil {
		t.Errorf("3. errChainAll() failed")
	}

	err = errChainAll(func() error { return fmt.Errorf("error") })
	if err == nil {
		t.Errorf("4. errChainAll() failed")
	}

	err = errChainAll(func() error { return nil }, func() error { return fmt.Errorf("error") })
	if err == nil {
		t.Errorf("5. errChainAll() failed")
	}

	var1 := false
	err = errChainAll(func() error { return fmt.Errorf("error") }, func() error { var1 = true; return nil })
	if err == nil || !var1 {
		t.Errorf("6. errChainAll() failed")
	}
}

func Test_parsePathAndCommands(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []command
		wantErr bool
	}{
		{
			name:    "empty list",
			args:    nil,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "empty list 2",
			args:    []string{},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "one arg",
			args:    []string{"arg"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "two arg without path",
			args:    []string{"arg", "arg2"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "three arg",
			args:    []string{"/arg", "date", "aaa"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "two arg",
			args:    []string{"/date", "date"},
			want:    []command{{path: "/date", cmd: "date"}},
			wantErr: false,
		},
		{
			name:    "four arg",
			args:    []string{"/date", "date", "/", "echo index"},
			want:    []command{{path: "/date", cmd: "date"}, {path: "/", cmd: "echo index"}},
			wantErr: false,
		},
		{
			name:    "with http method",
			args:    []string{"POST:/date", "date", "GET:/", "echo index"},
			want:    []command{{path: "/date", cmd: "date", httpMethod: "POST"}, {path: "/", cmd: "echo index", httpMethod: "GET"}},
			wantErr: false,
		},
		{
			name:    "invalid method",
			args:    []string{"get:/date"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid method2",
			args:    []string{"GET_A:/date"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid path",
			args:    []string{"GET:/date 2"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "not uniq path",
			args:    []string{"POST:/date", "date", "POST:/date", "echo index"},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePathAndCommands(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePathAndCommands() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePathAndCommands() = %v, want %v", got, tt.want)
			}
		})
	}
}

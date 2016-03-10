package main

import (
	"io"
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

func httpRequest(method string, url string, postData string) ([]byte, error) {
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
	port := getFreePort()
	os.Args = []string{"shell2http",
		"-add-exit",
		"-cache=1",
		"-cgi",
		"-export-all-vars",
		"-export-vars=HOME",
		"-one-thread",
		"-shell=",
		"-log=/dev/null",
		"-port=" + port,
		"/echo", "echo 123",
		"/form", "echo var=$v_var",
		"/error", "/ not exists cmd",
		"/post", "cat",
		"/redirect", `echo "Location: /` + "\n" + `"`,
	}
	go main()

	// hide stderr
	oldStderr := os.Stderr // keep backup of the real stderr
	newStderr, err := os.Open("/dev/null")
	if err != nil {
		t.Errorf("open /dev/null: %s", err)
	}
	os.Stderr = newStderr
	defer func() { os.Stderr = oldStderr; newStderr.Close() }()

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
		func(res string) bool { return strings.HasPrefix(res, "exec error:") },
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
}

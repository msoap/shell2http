package main

import (
	"reflect"
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

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

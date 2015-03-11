/*
Execute shell commands via http server

Install:
	go get github.com/msoap/shell2http
	ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage:
	shell2http /path "shell command" /path2 "shell command2" ...

Examples:
	shell2http /top "top -l 1 | head -10"
	shell2http /date date /ps "ps aux"
	shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
	shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
	shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'

Update:
	go get -u github.com/msoap/shell2http

*/
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
)

// default port for http-server
const PORT = "8080"

// ------------------------------------------------------------------
const INDEX_HTML = `
<!DOCTYPE html>
<html>
<head>
	<title>Index shell commands</title>
</head>
<body>
	<h1>Index shell commands</h1>
	<ul>
		%s
		<li><a href="/exit">/exit</a></li>
	</ul>
</body>
</html>
`

// ------------------------------------------------------------------
// one command type
type t_command struct {
	path string
	cmd  string
}

// ------------------------------------------------------------------
// parse argeuments
func get_handlers() ([]t_command, error) {
	// need >= 2 argeuments and count of it must be even
	if len(os.Args) < 3 || len(os.Args)%2 == 0 {
		return nil, fmt.Errorf(`usage: shell2http /path "shell command" /path2 "shell command2"`)
	}

	cmd_handlers := []t_command{}

	args_i := 1
	for args_i < len(os.Args) {
		path, cmd := os.Args[args_i], os.Args[args_i+1]
		if path[0] != '/' {
			return nil, fmt.Errorf("error: path %s dont starts with /", path)
		}
		cmd_handlers = append(cmd_handlers, t_command{path: path, cmd: cmd})
		args_i += 2
	}

	return cmd_handlers, nil
}

// ------------------------------------------------------------------
// setup http handlers
func setup_handlers(cmd_handlers []t_command) {
	index_li_html := ""
	for _, row := range cmd_handlers {
		path, cmd := row.path, row.cmd
		shell_handler := func(rw http.ResponseWriter, req *http.Request) {
			fmt.Println("GET", path)

			shell_out, err := exec.Command("sh", "-c", cmd).Output()
			if err != nil {
				fmt.Println("exec error: ", err)
				fmt.Fprint(rw, "exec error: ", err)
			} else {
				fmt.Fprint(rw, string(shell_out))
			}

			return
		}

		fmt.Printf("register: %s (%s)\n", path, cmd)
		http.HandleFunc(path, shell_handler)
		index_li_html += fmt.Sprintf(`<li><a href="%s">%s</a></li>`, path, path)
	}

	// --------------
	index_html := fmt.Sprintf(INDEX_HTML, index_li_html)
	http.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		fmt.Println("GET /")
		fmt.Fprint(rw, index_html)

		return
	})

	// --------------
	http.HandleFunc("/exit", func(rw http.ResponseWriter, req *http.Request) {
		fmt.Println("GET /exit")
		fmt.Fprint(rw, "Bye...")
		go os.Exit(0)

		return
	})
}

// ------------------------------------------------------------------
func main() {
	cmd_handlers, err := get_handlers()
	if err != nil {
		fmt.Println(err)
		return
	}
	setup_handlers(cmd_handlers)

	fmt.Println("listen http://localhost:" + PORT + "/")
	err = http.ListenAndServe(":"+PORT, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
}

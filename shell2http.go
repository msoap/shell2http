/*
Execute shell commands via http server

Install:
	go get github.com/msoap/shell2http
	ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage:
	shell2http [options] /path "shell command" /path2 "shell command2" ...
	options:
		-host="host": host for http server
		-host=      : for bind to all hosts
		-port=NNNN  : port for http server
		-help

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
	"flag"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"os/exec"
)

// default port for http-server
const PORT = 8080

// default host for bind
const HOST = "localhost"

// ------------------------------------------------------------------
const INDEX_HTML = `
<!DOCTYPE html>
<html>
<head>
	<title>shell2http</title>
</head>
<body>
	<h1>shell2http</h1>
	<ul>
		%s
		<li><a href="/exit">/exit</a></li>
	</ul>
	Get from: <a href="https://github.com/msoap/shell2http">github.com/msoap/shell2http</a>
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
// parse arguments
func get_config() (cmd_handlers []t_command, host string, port int, err error) {
	flag.IntVar(&port, "port", PORT, "port for http server")
	flag.StringVar(&host, "host", HOST, "host for http server")
	flag.Usage = func() {
		fmt.Printf("usage: %s [options] /path \"shell command\" /path2 \"shell command2\"\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()

	// need >= 2 arguments and count of it must be even
	args := flag.Args()
	if len(args) < 2 || len(args)%2 == 1 {
		return nil, host, port, fmt.Errorf("error: need pairs of path and shell command")
	}

	args_i := 0
	for args_i < len(args) {
		path, cmd := args[args_i], args[args_i+1]
		if path[0] != '/' {
			return nil, host, port, fmt.Errorf("error: path %s dont starts with /", path)
		}
		cmd_handlers = append(cmd_handlers, t_command{path: path, cmd: cmd})
		args_i += 2
	}

	return cmd_handlers, host, port, nil
}

// ------------------------------------------------------------------
// setup http handlers
func setup_handlers(cmd_handlers []t_command) {
	index_li_html := ""
	for _, row := range cmd_handlers {
		path, cmd := row.path, row.cmd
		shell_handler := func(rw http.ResponseWriter, req *http.Request) {
			log.Println("GET", path)

			shell_out, err := exec.Command("sh", "-c", cmd).Output()
			if err != nil {
				log.Println("exec error: ", err)
				fmt.Fprint(rw, "exec error: ", err)
			} else {
				fmt.Fprint(rw, string(shell_out))
			}

			return
		}

		log.Printf("register: %s (%s)\n", path, cmd)
		http.HandleFunc(path, shell_handler)
		index_li_html += fmt.Sprintf(`<li><a href="%s">%s</a> <span style="color: #888">- %s<span></li>`, path, path, html.EscapeString(cmd))
	}

	// --------------
	index_html := fmt.Sprintf(INDEX_HTML, index_li_html)
	http.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		log.Println("GET /")
		fmt.Fprint(rw, index_html)

		return
	})

	// --------------
	http.HandleFunc("/exit", func(rw http.ResponseWriter, req *http.Request) {
		log.Println("GET /exit")
		fmt.Fprint(rw, "Bye...")
		go os.Exit(0)

		return
	})
}

// ------------------------------------------------------------------
func main() {
	cmd_handlers, host, port, err := get_config()
	if err != nil {
		log.Println(err)
		return
	}
	setup_handlers(cmd_handlers)

	adress := fmt.Sprintf("%s:%d", host, port)
	log.Printf("listen http://%s/\n", adress)
	err = http.ListenAndServe(adress, nil)
	if err != nil {
		log.Println(err)
		return
	}
}

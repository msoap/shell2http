/*
Execute shell commands via http server

Install:
	go get github.com/msoap/shell2http
	ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage:
	shell2http [options] /path "shell command" /path2 "shell command2" ...
	options:
		-host="host" : host for http server
		-host=       : for bind to all hosts
		-port=NNNN   : port for http server
		-form        : parse query into enviroment vars
		-cgi         : set some CGI variables in enviroment
		-log=filename: log filename, default - STDOUT
		-help

Examples:
	shell2http /top "top -l 1 | head -10"
	shell2http /date date /ps "ps aux"
	shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
	shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
	shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
	shell2http -form /form 'echo $v_from, $v_to'
	shell2http -cgi /query 'echo $QUERY_STRING'

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
	"strings"
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
func get_config() (cmd_handlers []t_command, host string, port int, set_cgi bool, set_form bool, err error) {
	var log_filename string
	flag.StringVar(&log_filename, "log", "", "log filename, default - STDOUT")
	flag.IntVar(&port, "port", PORT, "port for http server")
	flag.StringVar(&host, "host", HOST, "host for http server")
	flag.BoolVar(&set_cgi, "cgi", false, "set some CGI variables in enviroment")
	flag.BoolVar(&set_form, "form", false, "parse query into enviroment vars")
	flag.Usage = func() {
		fmt.Printf("usage: %s [options] /path \"shell command\" /path2 \"shell command2\"\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()

	// setup log file
	if len(log_filename) > 0 {
		fh_log, err := os.OpenFile(log_filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		log.SetOutput(fh_log)
	}

	// need >= 2 arguments and count of it must be even
	args := flag.Args()
	if len(args) < 2 || len(args)%2 == 1 {
		return nil, host, port, set_cgi, set_form, fmt.Errorf("error: need pairs of path and shell command")
	}

	args_i := 0
	for args_i < len(args) {
		path, cmd := args[args_i], args[args_i+1]
		if path[0] != '/' {
			return nil, host, port, set_cgi, set_form, fmt.Errorf("error: path %s dont starts with /", path)
		}
		cmd_handlers = append(cmd_handlers, t_command{path: path, cmd: cmd})
		args_i += 2
	}

	return cmd_handlers, host, port, set_cgi, set_form, nil
}

// ------------------------------------------------------------------
// setup http handlers
func setup_handlers(cmd_handlers []t_command, host string, port int, set_cgi bool, set_form bool) {
	index_li_html := ""
	for _, row := range cmd_handlers {
		path, cmd := row.path, row.cmd
		shell_handler := func(rw http.ResponseWriter, req *http.Request) {
			log.Println("GET", path)

			if set_form {
				get_form(req)
			}
			if set_cgi {
				set_cgi_env(req, path, host, port)
			}

			os_exec_command := exec.Command("sh", "-c", cmd)
			os_exec_command.Stderr = os.Stderr
			shell_out, err := os_exec_command.Output()

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
// set some CGI variables
func set_cgi_env(req *http.Request, path string, host string, port int) {
	headers := map[string]string{
		"Accept":          "HTTP_ACCEPT",
		"Accept-Encoding": "HTTP_ACCEPT_ENCODING",
		"Accept-Language": "HTTP_ACCEPT_LANGUAGE",
		"User-Agent":      "HTTP_USER_AGENT",
	}
	for header_key, cgi_var_name := range headers {
		if header, exists := req.Header[header_key]; exists && len(header) > 0 {
			os.Setenv(cgi_var_name, header[0])
		}
	}
	remote_addr := strings.Split(req.RemoteAddr, ":")

	CGI_vars := [...]struct {
		name, value string
	}{
		{"PATH_INFO", req.URL.Path},
		{"QUERY_STRING", req.URL.RawQuery},
		{"REMOTE_ADDR", remote_addr[0]},
		{"REMOTE_PORT", remote_addr[1]},
		{"REQUEST_METHOD", req.Method},
		{"REQUEST_URI", req.RequestURI},
		{"SCRIPT_NAME", path},
		{"SERVER_NAME", host},
		{"SERVER_PORT", fmt.Sprintf("%d", port)},
		{"SERVER_PROTOCOL", req.Proto},
		{"SERVER_SOFTWARE", "shell2http"},
	}

	for _, row := range CGI_vars {
		os.Setenv(row.name, row.value)
	}
}

// ------------------------------------------------------------------
// parse form into enviroment vars
func get_form(req *http.Request) {
	// clear old variables
	for _, env_raw := range os.Environ() {
		env := strings.SplitN(env_raw, "=", 2)
		if strings.HasPrefix(env[0], "v_") && len(env[0]) > 2 {
			err := os.Unsetenv(env[0])
			if err != nil {
				log.Println(err)
				return
			}
		}
	}

	// set new
	err := req.ParseForm()
	if err != nil {
		log.Println(err)
		return
	}

	for key, value := range req.Form {
		os.Setenv("v_"+key, strings.Join(value, ","))
	}
}

// ------------------------------------------------------------------
func main() {
	cmd_handlers, host, port, set_cgi, set_form, err := get_config()
	if err != nil {
		log.Println(err)
		return
	}
	setup_handlers(cmd_handlers, host, port, set_cgi, set_form)

	adress := fmt.Sprintf("%s:%d", host, port)
	log.Printf("listen http://%s/\n", adress)
	err = http.ListenAndServe(adress, nil)
	if err != nil {
		log.Println(err)
		return
	}
}

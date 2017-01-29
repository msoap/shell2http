/*
Executing shell commands via simple http server.
Settings through 2 command line arguments, path and shell command.
By default bind to :8080.

Install/update:
	go get -u github.com/msoap/shell2http
	ln -s $GOPATH/bin/shell2http ~/bin/shell2http

MacOS install:
	brew tap msoap/tools
	brew install shell2http
	# update:
	brew upgrade shell2http

Usage:
	shell2http [options] /path "shell command" /path2 "shell command2" ...
	options:
		-host="host"    : host for http server, default - all interfaces
		-port=NNNN      : port for http server, default - 8080
		-form           : parse query into environment vars
		-cgi            : run scripts in CGI-mode:
		                  - set environment variables
		                  - write POST-data to STDIN (if not set -form)
		                  - parse headers from script (eg: "Location: URL\n\n")
		-export-vars=var: export environment vars ("VAR1,VAR2,...")
		-export-all-vars: export all current environment vars
		-no-index       : dont generate index page
		-add-exit       : add /exit command
		-log=filename   : log filename, default - STDOUT
		-shell="shell"  : shell for execute command, "" - without shell
		-cache=NNN      : caching command out for NNN seconds
		-one-thread     : run each shell command in one thread
		-show-errors    : show the standard output even if the command exits with a non-zero exit code
		-include-stderr : include stderr to output (default is stdout only)
		-cert=cert.pem  : SSL certificate path (if specified -cert/-key options - run https server)
		-key=key.pem    : SSL private key path
		-basic-auth=""	: setup HTTP Basic Authentication ("user_name:password")
		-version
		-help

Examples:
	shell2http /top "top -l 1 | head -10"
	shell2http /date date /ps "ps aux"
	shell2http -export-all-vars /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
	shell2http -export-all-vars /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
	shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
	shell2http -form /form 'echo $v_from, $v_to'
	shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'
	shell2http -cgi /set 'touch file; echo "Location: /\n"'
	shell2http -cgi /404 'echo "Status: 404"; echo; echo "404 page"'
	shell2http -export-vars=GOPATH /get 'echo $GOPATH'

More complex examples:

simple http-proxy server (for logging all URLs)
	# setup proxy as "http://localhost:8080/"
	shell2http \
		-log=/dev/null \
		-cgi \
		/ 'echo $REQUEST_URI 1>&2; [ "$REQUEST_METHOD" == "POST" ] && post_param="-d@-"; curl -sL $post_param "$REQUEST_URI" -A "$HTTP_USER_AGENT"'

test slow connection
	# http://localhost:8080/slow?duration=10
	shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'

proxy with cache in files (for debug with production API with rate limit)
	# get "http://localhost:8080/get?url=http://api.url/"
	shell2http \
		-form \
		/form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
		/get 'MD5=$(printf "%s" $v_url | md5); cat cache_$MD5 || (curl -sL $v_url | tee cache_$MD5)'

remote sound volume control (Mac OS)
	shell2http \
		/get  'osascript -e "output volume of (get volume settings)"' \
		/up   'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")+10))"' \
		/down 'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")-10))"'

remote control for Vox.app player (Mac OS)
	shell2http \
		/play_pause 'osascript -e "tell application \"Vox\" to playpause" && echo ok' \
		/get_info 'osascript -e "tell application \"Vox\"" -e "\"Artist: \" & artist & \"\n\" & \"Album: \" & album & \"\n\" & \"Track: \" & track" -e "end tell"'

get four random OS X wallpapers
	shell2http \
		/img 'cat "$(ls "/Library/Desktop Pictures/"*.jpg | ruby -e "puts STDIN.readlines.shuffle[0]")"' \
		/wallpapers 'echo "<html><h3>OS X Wallpapers</h3>"; seq 4 | xargs -I@ echo "<img src=/img?@ width=500>"'

More examples on https://github.com/msoap/shell2http/wiki

*/
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/koding/cache"
	"github.com/mattn/go-shellwords"
)

// VERSION - version
const VERSION = "1.8"

// PORT - default port for http-server
const PORT = 8080

// ------------------------------------------------------------------

// INDEXHTML - Template for index page
const INDEXHTML = `<!DOCTYPE html>
<html>
<head>
	<title>shell2http</title>
</head>
<body>
	<h1>shell2http</h1>
	<ul>
		%s
	</ul>
	Get from: <a href="https://github.com/msoap/shell2http">github.com/msoap/shell2http</a>
</body>
</html>
`

// ------------------------------------------------------------------

// Command - one command type
type Command struct {
	path    string
	cmd     string
	handler http.HandlerFunc
}

// Config - config struct
type Config struct {
	port          int    // server port
	cache         int    // caching command out (in seconds)
	host          string // server host
	exportVars    string // list of environment vars for export to script
	shell         string // export all current environment vars
	cert          string // SSL certificate
	key           string // SSL private key path
	authUser      string // basic authentication user name
	authPass      string // basic authentication password
	exportAllVars bool   // export all current environment vars
	setCGI        bool   // set CGI variables
	setForm       bool   // parse form from URL
	noIndex       bool   // dont generate index page
	addExit       bool   // add /exit command
	oneThread     bool   // run each shell commands in one thread
	showErrors    bool   // returns the standard output even if the command exits with a non-zero exit code
	includeStderr bool   // also returns output written to stderr (default is stdout only)
}

const (
	maxHTTPCode     = 1000
	cacheGCInterval = 60 // in seconds
)

// ------------------------------------------------------------------
// parse arguments
func getConfig() (cmdHandlers []Command, appConfig Config, err error) {
	var (
		logFilename string
		basicAuth   string
	)

	flag.StringVar(&logFilename, "log", "", "log filename, default - STDOUT")
	flag.IntVar(&appConfig.port, "port", PORT, "port for http server")
	flag.StringVar(&appConfig.host, "host", "", "host for http server")
	flag.BoolVar(&appConfig.setCGI, "cgi", false, "run scripts in CGI-mode")
	flag.StringVar(&appConfig.exportVars, "export-vars", "", "export environment vars (\"VAR1,VAR2,...\")")
	flag.BoolVar(&appConfig.exportAllVars, "export-all-vars", false, "export all current environment vars")
	flag.BoolVar(&appConfig.setForm, "form", false, "parse query into environment vars")
	flag.BoolVar(&appConfig.noIndex, "no-index", false, "dont generate index page")
	flag.BoolVar(&appConfig.addExit, "add-exit", false, "add /exit command")
	flag.StringVar(&appConfig.shell, "shell", "sh", "custom shell or \"\" for execute without shell")
	flag.IntVar(&appConfig.cache, "cache", 0, "caching command out (in seconds)")
	flag.BoolVar(&appConfig.oneThread, "one-thread", false, "run each shell command in one thread")
	flag.BoolVar(&appConfig.showErrors, "show-errors", false, "show the standard output even if the command exits with a non-zero exit code")
	flag.BoolVar(&appConfig.includeStderr, "include-stderr", false, "include stderr to output (default is stdout only)")
	flag.StringVar(&appConfig.cert, "cert", "", "SSL certificate path (if specified -cert/-key options - run https server)")
	flag.StringVar(&appConfig.key, "key", "", "SSL private key path")
	flag.StringVar(&basicAuth, "basic-auth", "", "setup HTTP Basic Authentication (\"user_name:password\")")

	flag.Usage = func() {
		fmt.Printf("usage: %s [options] /path \"shell command\" /path2 \"shell command2\"\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	version := flag.Bool("version", false, "get version")

	flag.Parse()

	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	// setup log file
	if len(logFilename) > 0 {
		fhLog, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		log.SetOutput(fhLog)
	}

	if len(appConfig.cert) > 0 && len(appConfig.key) == 0 ||
		len(appConfig.cert) == 0 && len(appConfig.key) > 0 {
		return nil, Config{}, fmt.Errorf("error: need both -cert and -key options")
	}

	if len(basicAuth) > 0 {
		basicAuthParts := strings.SplitN(basicAuth, ":", 2)
		if len(basicAuthParts) != 2 {
			log.Fatalf("HTTP basic authentication must be in format: name:password")
		}
		appConfig.authUser, appConfig.authPass = basicAuthParts[0], basicAuthParts[1]
	}

	// need >= 2 arguments and count of it must be even
	args := flag.Args()
	if len(args) < 2 || len(args)%2 == 1 {
		return nil, Config{}, fmt.Errorf("error: need pairs of path and shell command")
	}

	for i := 0; i < len(args); i += 2 {
		path, cmd := args[i], args[i+1]
		if path[0] != '/' {
			return nil, Config{}, fmt.Errorf("error: path %s dont starts with /", path)
		}
		cmdHandlers = append(cmdHandlers, Command{path: path, cmd: cmd})
	}

	return cmdHandlers, appConfig, nil
}

// ------------------------------------------------------------------
// get default shell and command
func getShellAndParams(cmd string, customShell string, isWindows bool) (shell string, params []string, err error) {
	shell, params = "sh", []string{"-c", cmd}
	if isWindows {
		shell, params = "cmd", []string{"/C", cmd}
	}

	// custom shell
	switch {
	case customShell != "sh" && customShell != "":
		shell = customShell
	case customShell == "":
		cmdLine, err := shellwords.Parse(cmd)
		if err != nil {
			return shell, params, fmt.Errorf("Parse '%s' failed: %s", cmd, err)
		}

		shell, params = cmdLine[0], cmdLine[1:]
	}

	return shell, params, nil
}

// ------------------------------------------------------------------
// printAccessLogLine - out one line of access log
func printAccessLogLine(req *http.Request) {
	remoteAddr := req.RemoteAddr
	if realIP, ok := req.Header["X-Real-Ip"]; ok && len(realIP) > 0 {
		remoteAddr += ", " + realIP[0]
	}
	log.Printf("%s %s %s %s \"%s\"", req.Host, remoteAddr, req.Method, req.RequestURI, req.UserAgent())
}

// ------------------------------------------------------------------
// getShellHandler - get handler function for one shell command
func getShellHandler(appConfig Config, path string, shell string, params []string, cacheTTL *cache.MemoryTTL) func(http.ResponseWriter, *http.Request) {
	mutex := sync.Mutex{}
	reStatusCode := regexp.MustCompile(`^\d+`)

	shellHandler := func(rw http.ResponseWriter, req *http.Request) {
		printAccessLogLine(req)
		setCommonHeaders(rw)

		if appConfig.cache > 0 {
			cacheData, err := cacheTTL.Get(req.RequestURI)
			if err != cache.ErrNotFound && err != nil {
				log.Print(err)
			} else if err == nil {
				// cache hit
				fmt.Fprint(rw, cacheData.(string))
				return
			}
		}

		osExecCommand := exec.Command(shell, params...)

		proxySystemEnv(osExecCommand, appConfig)
		if appConfig.setForm {
			getForm(osExecCommand, req)
		}

		if appConfig.setCGI {
			setCGIEnv(osExecCommand, req, appConfig)
		}

		if appConfig.oneThread {
			mutex.Lock()
			defer mutex.Unlock()
		}

		var (
			shellOut []byte
			err      error
		)
		if appConfig.includeStderr {
			shellOut, err = osExecCommand.CombinedOutput()
		} else {
			osExecCommand.Stderr = os.Stderr
			shellOut, err = osExecCommand.Output()
		}

		exitCode := 0
		if err != nil {
			log.Println("exec error: ", err)

			// get exit code. May be works on POSIX-system only, need test on Windows
			if exiterr, ok := err.(*exec.ExitError); ok {
				if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
					exitCode = status.ExitStatus()
				}
			}
		}
		rw.Header().Set("X-Shell2http-Exit-Code", fmt.Sprintf("%d", exitCode))

		if err != nil && !appConfig.showErrors {
			fmt.Fprint(rw, "exec error: ", err)
		} else {
			outText := string(shellOut)
			if appConfig.setCGI {
				var headers map[string]string
				outText, headers = parseCGIHeaders(outText)
				customStatusCode := 0

				for headerKey, headerValue := range headers {
					switch headerKey {
					case "Status":
						statusParts := reStatusCode.FindAllString(headerValue, -1)
						if len(statusParts) > 0 {
							statusCode, err := strconv.Atoi(statusParts[0])
							if err == nil && statusCode > 0 && statusCode < maxHTTPCode {
								customStatusCode = statusCode
								continue
							}
						}
					case "Location":
						customStatusCode = http.StatusFound
					}

					rw.Header().Set(headerKey, headerValue)
				}

				if customStatusCode > 0 {
					rw.WriteHeader(customStatusCode)
				}
			}
			fmt.Fprint(rw, outText)

			if appConfig.cache > 0 {
				err := cacheTTL.Set(req.RequestURI, outText)
				if err != nil {
					log.Print(err)
				}
			}
		}

		return
	}

	return shellHandler
}

// ------------------------------------------------------------------
// setupHandlers - setup http handlers
func setupHandlers(cmdHandlers []Command, appConfig Config, cacheTTL *cache.MemoryTTL) ([]Command, error) {
	indexLiHTML := ""
	existsRootPath := false

	for i, row := range cmdHandlers {
		path, cmd := row.path, row.cmd
		shell, params, err := getShellAndParams(cmd, appConfig.shell, runtime.GOOS == "windows")
		if err != nil {
			return nil, err
		}

		existsRootPath = existsRootPath || path == "/"

		indexLiHTML += fmt.Sprintf(`<li><a href="%s">%s</a> <span style="color: #888">- %s<span></li>`, path, path, html.EscapeString(cmd))
		cmdHandlers[i].handler = getShellHandler(appConfig, path, shell, params, cacheTTL)
	}

	// --------------
	if appConfig.addExit {
		cmdHandlers = append(cmdHandlers, Command{
			path: "/exit",
			cmd:  "/exit",
			handler: func(rw http.ResponseWriter, req *http.Request) {
				printAccessLogLine(req)
				setCommonHeaders(rw)
				fmt.Fprint(rw, "Bye...")
				go os.Exit(0)

				return
			},
		})

		indexLiHTML += fmt.Sprintf(`<li><a href="%s">%s</a></li>`, "/exit", "/exit")
	}

	// --------------
	if !appConfig.noIndex && !existsRootPath {
		indexHTML := fmt.Sprintf(INDEXHTML, indexLiHTML)
		cmdHandlers = append(cmdHandlers, Command{
			path: "/",
			cmd:  "index page",
			handler: func(rw http.ResponseWriter, req *http.Request) {
				setCommonHeaders(rw)
				if req.URL.Path != "/" {
					log.Printf("404: %s", req.URL.Path)
					http.NotFound(rw, req)
					return
				}
				printAccessLogLine(req)
				fmt.Fprint(rw, indexHTML)

				return
			},
		})
	}

	return cmdHandlers, nil
}

// ------------------------------------------------------------------
// set some CGI variables
func setCGIEnv(cmd *exec.Cmd, req *http.Request, appConfig Config) {
	// set HTTP_* variables
	for headerName, headerValue := range req.Header {
		envName := strings.ToUpper(strings.Replace(headerName, "-", "_", -1))
		if envName == "PROXY" {
			continue
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("HTTP_%s=%s", envName, headerValue[0]))
	}

	remoteAddr := regexp.MustCompile(`^(.+):(\d+)$`).FindStringSubmatch(req.RemoteAddr)
	if len(remoteAddr) != 3 {
		remoteAddr = []string{"", "", ""}
	}
	CGIVars := [...]struct {
		cgiName, value string
	}{
		{"PATH_INFO", req.URL.Path},
		{"QUERY_STRING", req.URL.RawQuery},
		{"REMOTE_ADDR", remoteAddr[1]},
		{"REMOTE_PORT", remoteAddr[2]},
		{"REQUEST_METHOD", req.Method},
		{"REQUEST_URI", req.RequestURI},
		{"SCRIPT_NAME", req.URL.Path},
		{"SERVER_NAME", appConfig.host},
		{"SERVER_PORT", fmt.Sprintf("%d", appConfig.port)},
		{"SERVER_PROTOCOL", req.Proto},
		{"SERVER_SOFTWARE", "shell2http"},
	}

	for _, row := range CGIVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", row.cgiName, row.value))
	}

	// get POST data to stdin of script (if not parse form vars above)
	if req.Method == "POST" && !appConfig.setForm {

		var (
			stdin    io.WriteCloser
			postBody []byte
		)
		err := errChain(func() (err error) {
			stdin, err = cmd.StdinPipe()
			return err
		}, func() (err error) {
			postBody, err = ioutil.ReadAll(req.Body)
			return err
		}, func() error {
			_, err := stdin.Write(postBody)
			return err
		}, func() error {
			return stdin.Close()
		})
		if err != nil {
			log.Println("get STDIN error: ", err)
			return
		}

	}
}

// errChain - handle errors on few functions
func errChain(chainFuncs ...func() error) error {
	for _, fn := range chainFuncs {
		if err := fn(); err != nil {
			return err
		}
	}

	return nil
}

// ------------------------------------------------------------------
/* parse headers from script output:

Header-name1: value1\n
Header-name2: value2\n
\n
text

*/
func parseCGIHeaders(shellOut string) (string, map[string]string) {
	parts := regexp.MustCompile(`\r?\n\r?\n`).Split(shellOut, 2)
	if len(parts) == 2 {

		headerRe := regexp.MustCompile(`^([^:\s]+):\s*(\S.*)$`)
		headerLines := regexp.MustCompile(`\r?\n`).Split(parts[0], -1)
		headersMap := map[string]string{}

		for _, headerLine := range headerLines {
			headerParts := headerRe.FindStringSubmatch(headerLine)
			if len(headerParts) == 3 {
				headersMap[headerParts[1]] = headerParts[2]
			} else {
				// headers is not valid, return all text
				return shellOut, map[string]string{}
			}
		}

		return parts[1], headersMap
	}

	// headers dont found, return all text
	return shellOut, map[string]string{}
}

// ------------------------------------------------------------------
// parse form into environment vars
func getForm(cmd *exec.Cmd, req *http.Request) {
	err := req.ParseForm()
	if err != nil {
		log.Println(err)
		return
	}

	for key, value := range req.Form {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "v_"+key, strings.Join(value, ",")))
	}
}

// ------------------------------------------------------------------
// proxy some system vars
func proxySystemEnv(cmd *exec.Cmd, appConfig Config) {
	varsNames := []string{"PATH", "HOME", "LANG", "USER", "TMPDIR"}

	if appConfig.exportVars != "" {
		varsNames = append(varsNames, strings.Split(appConfig.exportVars, ",")...)
	}

	for _, envRaw := range os.Environ() {
		env := strings.SplitN(envRaw, "=", 2)
		if appConfig.exportAllVars {
			cmd.Env = append(cmd.Env, envRaw)
		} else {
			for _, envVarName := range varsNames {
				if env[0] == envVarName {
					cmd.Env = append(cmd.Env, envRaw)
				}
			}
		}
	}
}

// ------------------------------------------------------------------
// set headers for all handlers
func setCommonHeaders(rw http.ResponseWriter) {
	rw.Header().Set("Server", fmt.Sprintf("shell2http %s", VERSION))
}

// basicAuthWrapper - add HTTP Basic Authentication
func basicAuthWrapper(handler http.HandlerFunc, user, pass string) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		reqUser, reqPass, ok := req.BasicAuth()
		if !ok || reqUser != user || reqPass != pass {
			setCommonHeaders(rw)
			rw.Header().Set("WWW-Authenticate", `Basic realm="Please enter user and passoerd"`)
			http.Error(rw, "name/password is required", http.StatusUnauthorized)
			printAccessLogLine(req)
			return
		}

		handler(rw, req)
	}
}

// ------------------------------------------------------------------
func main() {
	cmdHandlers, appConfig, err := getConfig()
	if err != nil {
		log.Fatal(err)
	}

	var cacheTTL *cache.MemoryTTL
	if appConfig.cache > 0 {
		cacheTTL = cache.NewMemoryWithTTL(time.Duration(appConfig.cache) * time.Second)
		cacheTTL.StartGC(cacheGCInterval * time.Second)
	}

	cmdHandlers, err = setupHandlers(cmdHandlers, appConfig, cacheTTL)
	if err != nil {
		log.Fatal(err)
	}
	for _, handler := range cmdHandlers {
		handlerFunc := handler.handler
		if len(appConfig.authUser) > 0 {
			handlerFunc = basicAuthWrapper(handler.handler, appConfig.authUser, appConfig.authPass)
		}

		http.HandleFunc(handler.path, handlerFunc)
		log.Printf("register: %s (%s)\n", handler.path, handler.cmd)
	}

	address := fmt.Sprintf("%s:%d", appConfig.host, appConfig.port)

	if len(appConfig.cert) > 0 && len(appConfig.key) > 0 {
		log.Printf("listen https://%s/\n", address)
		log.Fatal(http.ListenAndServeTLS(address, appConfig.cert, appConfig.key, nil))
	} else {
		log.Printf("listen http://%s/\n", address)
		log.Fatal(http.ListenAndServe(address, nil))
	}
}

package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
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

	"github.com/mattn/go-shellwords"
	"github.com/msoap/raphanus"
	raphanuscommon "github.com/msoap/raphanus/common"
)

// VERSION - version
const VERSION = "1.9"

// PORT - default port for http-server
const PORT = 8080

// ------------------------------------------------------------------

// INDEXHTML - Template for index page
const INDEXHTML = `<!DOCTYPE html>
<html>
<head>
    <title>❯ shell2http</title>
    <style>
    body {
        font-family: sans-serif;
    }
    li {
        list-style-type: none;
    }
    li:before {
        content: "❯";
        padding-right: 5px;
    }
    </style>
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
	timeout       int    // timeout for shell command (in seconds)
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
	maxHTTPCode            = 1000
	maxMemoryForUploadFile = 65536
)

// ------------------------------------------------------------------
// getConfig - parse arguments
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
	flag.BoolVar(&appConfig.setForm, "form", false, "parse query into environment vars, handle uploaded files")
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
	flag.IntVar(&appConfig.timeout, "timeout", 0, "set timeout for execute shell command (in seconds)")

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
			return nil, Config{}, fmt.Errorf("error opening log file: %v", err)
		}
		log.SetOutput(fhLog)
	}

	if len(appConfig.cert) > 0 && len(appConfig.key) == 0 ||
		len(appConfig.cert) == 0 && len(appConfig.key) > 0 {
		return nil, Config{}, fmt.Errorf("requires both -cert and -key options")
	}

	if len(basicAuth) > 0 {
		basicAuthParts := strings.SplitN(basicAuth, ":", 2)
		if len(basicAuthParts) != 2 {
			return nil, Config{}, fmt.Errorf("HTTP basic authentication must be in format: name:password")
		}
		appConfig.authUser, appConfig.authPass = basicAuthParts[0], basicAuthParts[1]
	}

	// need >= 2 arguments and count of it must be even
	args := flag.Args()
	if len(args) < 2 || len(args)%2 == 1 {
		return nil, Config{}, fmt.Errorf("requires a pair of path and shell command")
	}

	for i := 0; i < len(args); i += 2 {
		path, cmd := args[i], args[i+1]
		if path[0] != '/' {
			return nil, Config{}, fmt.Errorf("the path %q does not begin with the prefix /", path)
		}
		cmdHandlers = append(cmdHandlers, Command{path: path, cmd: cmd})
	}

	return cmdHandlers, appConfig, nil
}

// ------------------------------------------------------------------
// getShellAndParams - get default shell and command
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
func getShellHandler(appConfig Config, path string, shell string, params []string, cacheTTL raphanus.DB) func(http.ResponseWriter, *http.Request) {
	mutex := sync.Mutex{}
	reStatusCode := regexp.MustCompile(`^\d+`)

	return func(rw http.ResponseWriter, req *http.Request) {
		if appConfig.oneThread {
			mutex.Lock()
			defer mutex.Unlock()
		}

		printAccessLogLine(req)
		setCommonHeaders(rw)

		shellOut, err := execShellCommand(appConfig, path, shell, params, req, cacheTTL)
		if err != nil {
			log.Println("exec error: ", err)
		}

		rw.Header().Set("X-Shell2http-Exit-Code", fmt.Sprintf("%d", getExitCode(err)))

		if err != nil && !appConfig.showErrors {
			responseWrite(rw, "exec error: "+err.Error())
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

			responseWrite(rw, outText)
		}

		return
	}
}

// ------------------------------------------------------------------
// execShellCommand - execute shell command, returns bytes out and error
func execShellCommand(appConfig Config, path string, shell string, params []string, req *http.Request, cacheTTL raphanus.DB) ([]byte, error) {
	if appConfig.cache > 0 {
		if cacheData, err := cacheTTL.GetBytes(req.RequestURI); err != raphanuscommon.ErrKeyNotExists && err != nil {
			log.Printf("get from cache failed: %s", err)
		} else if err == nil {
			// cache hit
			return cacheData, nil
		}
	}

	ctx := context.Background()
	if appConfig.timeout > 0 {
		var cancelFn context.CancelFunc
		ctx, cancelFn = context.WithTimeout(ctx, time.Duration(appConfig.timeout)*time.Second)
		defer cancelFn()
	}
	osExecCommand := exec.CommandContext(ctx, shell, params...) // #nosec

	proxySystemEnv(osExecCommand, appConfig)

	finalizer := func() {}
	if appConfig.setForm {
		var err error
		if finalizer, err = getForm(osExecCommand, req); err != nil {
			log.Printf("parse form failed: %s", err)
		}
	}

	var (
		waitPipeWrite bool
		pipeErrCh     = make(chan error)
		shellOut      []byte
		err           error
	)

	if appConfig.setCGI {
		setCGIEnv(osExecCommand, req, appConfig)

		// get POST data to stdin of script (if not parse form vars above)
		if req.Method == "POST" && !appConfig.setForm {
			if stdin, pipeErr := osExecCommand.StdinPipe(); pipeErr != nil {
				log.Println("write POST data to shell failed:", pipeErr)
			} else {
				waitPipeWrite = true
				go func() {
					if _, pipeErr := io.Copy(stdin, req.Body); pipeErr != nil {
						pipeErrCh <- pipeErr
						return
					}
					pipeErrCh <- stdin.Close()
				}()
			}
		}
	}

	if appConfig.includeStderr {
		shellOut, err = osExecCommand.CombinedOutput()
	} else {
		osExecCommand.Stderr = os.Stderr
		shellOut, err = osExecCommand.Output()
	}

	if waitPipeWrite {
		if pipeErr := <-pipeErrCh; pipeErr != nil {
			log.Println("write POST data to shell failed:", pipeErr)
		}
	}

	finalizer()

	if appConfig.cache > 0 {
		if cacheErr := cacheTTL.SetBytes(req.RequestURI, shellOut, appConfig.cache); cacheErr != nil {
			log.Printf("set to cache failed: %s", cacheErr)
		}
	}

	return shellOut, err
}

// ------------------------------------------------------------------
// getExitCode - get exit code. May be works on POSIX-system only, need test on Windows
func getExitCode(execErr error) int {
	if exiterr, ok := execErr.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}

	return 0
}

// ------------------------------------------------------------------
// setupHandlers - setup http handlers
func setupHandlers(cmdHandlers []Command, appConfig Config, cacheTTL raphanus.DB) ([]Command, error) {
	indexLiHTML := ""
	existsRootPath := false

	for i, row := range cmdHandlers {
		path, cmd := row.path, row.cmd
		shell, params, err := getShellAndParams(cmd, appConfig.shell, runtime.GOOS == "windows")
		if err != nil {
			return nil, err
		}

		existsRootPath = existsRootPath || path == "/"

		indexLiHTML += fmt.Sprintf(`<li><a href=".%s">%s</a> <span style="color: #888">- %s<span></li>`, path, path, html.EscapeString(cmd))
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
				responseWrite(rw, "Bye...")
				go os.Exit(0)

				return
			},
		})

		indexLiHTML += fmt.Sprintf(`<li><a href=".%s">%s</a></li>`, "/exit", "/exit")
	}

	// --------------
	if !appConfig.noIndex && !existsRootPath {
		indexHTML := fmt.Sprintf(INDEXHTML, indexLiHTML)
		cmdHandlers = append(cmdHandlers, Command{
			path: "/",
			cmd:  "index page",
			handler: func(rw http.ResponseWriter, req *http.Request) {
				printAccessLogLine(req)
				setCommonHeaders(rw)

				if req.URL.Path != "/" {
					log.Printf("%s - 404", req.URL.Path)
					http.NotFound(rw, req)
					return
				}

				responseWrite(rw, indexHTML)
				return
			},
		})
	}

	return cmdHandlers, nil
}

// ------------------------------------------------------------------
// responseWrite - write text to response
func responseWrite(rw io.Writer, text string) {
	if _, err := io.WriteString(rw, text); err != nil {
		log.Printf("print string failed: %s", err)
	}
}

// ------------------------------------------------------------------
// setCGIEnv - set some CGI variables
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
// getForm - parse form into environment vars, also handle uploaded files
func getForm(cmd *exec.Cmd, req *http.Request) (func(), error) {
	tempDir := ""
	safeFileNameRe := regexp.MustCompile(`[^\.\w\-]+`)
	finalizer := func() {
		if tempDir != "" {
			if err := os.RemoveAll(tempDir); err != nil {
				log.Println(err)
			}
		}
	}

	if err := req.ParseForm(); err != nil {
		return finalizer, err
	}

	if isMultipartFormData(req.Header) {
		if err := req.ParseMultipartForm(maxMemoryForUploadFile); err != nil {
			return finalizer, err
		}
	}

	for key, value := range req.Form {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "v_"+key, strings.Join(value, ",")))
	}

	// handle uploaded files, save all to temporary files and set variables filename_XXX, filepath_XXX
	if req.MultipartForm != nil {
		for key, value := range req.MultipartForm.File {
			if len(value) == 1 {
				var (
					uplFile     multipart.File
					outFile     *os.File
					err         error
					reqFileName = value[0].Filename
				)

				errCreate := errChain(func() error {
					uplFile, err = value[0].Open()
					return err
				}, func() error {
					tempDir, err = ioutil.TempDir("", "shell2http_")
					return err
				}, func() error {
					prefix := safeFileNameRe.ReplaceAllString(reqFileName, "")
					outFile, err = ioutil.TempFile(tempDir, prefix+"_")
					return err
				}, func() error {
					_, err = io.Copy(outFile, uplFile)
					return err
				})

				errClose := errChainAll(func() error {
					if uplFile != nil {
						return uplFile.Close()
					}
					return nil
				}, func() error {
					if outFile != nil {
						return outFile.Close()
					}
					return nil
				})
				if errClose != nil {
					return finalizer, errClose
				}

				if errCreate != nil {
					return finalizer, errCreate
				}

				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "filepath_"+key, outFile.Name()))
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "filename_"+key, reqFileName))
			}
		}
	}

	return finalizer, nil
}

// ------------------------------------------------------------------
// isMultipartFormData - check header for multipart/form-data
func isMultipartFormData(headers http.Header) bool {
	if contentType, ok := headers["Content-Type"]; ok && len(contentType) == 1 && strings.HasPrefix(contentType[0], "multipart/form-data; ") {
		return true
	}

	return false
}

// ------------------------------------------------------------------
// proxySystemEnv - proxy some system vars
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
// setCommonHeaders - set headers for all handlers
func setCommonHeaders(rw http.ResponseWriter) {
	rw.Header().Set("Server", fmt.Sprintf("shell2http %s", VERSION))
}

// ------------------------------------------------------------------
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

// errChain - handle errors on few functions
func errChain(chainFuncs ...func() error) error {
	for _, fn := range chainFuncs {
		if err := fn(); err != nil {
			return err
		}
	}

	return nil
}

// errChainAll - handle errors on few functions, exec all func and returns the first error
func errChainAll(chainFuncs ...func() error) error {
	var resErr error
	for _, fn := range chainFuncs {
		if err := fn(); err != nil {
			resErr = err
		}
	}

	return resErr
}

// ------------------------------------------------------------------
func main() {
	cmdHandlers, appConfig, err := getConfig()
	if err != nil {
		log.Fatal(err)
	}

	var cacheTTL raphanus.DB
	if appConfig.cache > 0 {
		cacheTTL = raphanus.New("", 0)
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

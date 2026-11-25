package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-shellwords"
	"github.com/msoap/raphanus"
	raphanuscommon "github.com/msoap/raphanus/common"
)

var version = "dev"

// reHeaderName - request headers passed to CGI scripts as HTTP_* variables
var reHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// regexps for parsing headers from CGI script output
var (
	reCGIHeadersEnd = regexp.MustCompile(`\r?\n\r?\n`)
	reCGIHeaderLine = regexp.MustCompile(`^([^:\s]+):(.*)$`)
	reCGINewLine    = regexp.MustCompile(`\r?\n`)
)

const (
	// defaultPort - default port for http-server
	defaultPort = 8080

	// shBasicAuthVar - name of env var for basic auth credentials
	shBasicAuthVar = "SH_BASIC_AUTH"

	// shMaxUploadSizeVar - name of env var for max size of request body parsed by -form (in bytes), 0 - no limit
	shMaxUploadSizeVar = "SH_MAX_UPLOAD_SIZE"

	// defaultShellPOSIX - shell executable by default in POSIX systems
	defaultShellPOSIX = "sh"

	// defaultShellWindows - shell executable by default in Windows
	defaultShellWindows = "cmd"

	// defaultShellPlan9 - shell executable by default in Plan9
	defaultShellPlan9 = "rc"

	// minHTTPCode, maxHTTPCode - range of status codes accepted from the CGI "Status" header,
	// http.ResponseWriter.WriteHeader panics on codes outside of [100, 999]
	minHTTPCode            = 100
	maxHTTPCode            = 1000
	maxMemoryForUploadFile = 65536

	// readHeaderTimeout - max time for reading request headers
	readHeaderTimeout = 30 * time.Second

	// waitDelay - with -timeout, how long to wait for the command output to be closed
	// after the command has exited or has been killed
	waitDelay = time.Second

	// shutdownTimeout - on SIGINT/SIGTERM, how long to wait for running requests to finish
	shutdownTimeout = 3 * time.Second

	// exitCodeHeader - response header (or trailer in the stream mode) with the exit code of the command
	exitCodeHeader = "X-Shell2http-Exit-Code"
)

// outputMode - how the command output is sent to the client, set by the endpoint options
type outputMode int

const (
	outputBuffered  outputMode = iota // wait for the command to finish, then send the whole output (default)
	outputStream                      // stream=1: send the output as it is written
	outputSSE                         // sse=1: send each line of the output as a Server-Sent Event
	outputWebSocket                   // websocket=1: messages from the client to stdin, each line of the output as a message
)

// allowOrigin* - special values of the allow-origin option, any other value is a list of origins
const (
	allowOriginSameOrigin = "same-origin" // the Origin host is the same as the Host header (default)
	allowOriginSameSite   = "same-site"   // the Origin host has the same registrable domain as the Host header
	allowOriginAll        = "*"           // any origin
)

// endpointOptions - settings of one endpoint, set as a query in the path: "/path?stream=1"
type endpointOptions struct {
	output      outputMode
	allowOrigin string // allow-origin: the origins allowed to open a WebSocket, "" - same-origin
}

// indexTmpl - template for index page
const indexTmpl = `<!DOCTYPE html>
<!-- Served by shell2http/%s -->
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

// command - one command
type command struct {
	path       string
	cmd        string
	httpMethod string
	options    endpointOptions
	handler    http.HandlerFunc
}

// parsePathAndCommands - get all commands with pathes
func parsePathAndCommands(args []string) ([]command, error) {
	var cmdHandlers []command

	if len(args) < 2 || len(args)%2 == 1 {
		return cmdHandlers, fmt.Errorf("requires a pair of path and shell command")
	}

	pathRe := regexp.MustCompile(`^(?:([A-Z]+):)?(/\S*)$`)
	uniqPaths := map[string]bool{}

	for i := 0; i < len(args); i += 2 {
		rawPath, cmd := args[i], args[i+1]

		pathParts := pathRe.FindStringSubmatch(rawPath)
		if len(pathParts) != 3 {
			return nil, fmt.Errorf("the path %q must begin with the prefix /, and with optional METHOD: prefix", rawPath)
		}

		path, rawOptions, hasOptions := strings.Cut(pathParts[2], "?")
		var options endpointOptions
		if hasOptions {
			var err error
			if options, err = parseEndpointOptions(rawOptions); err != nil {
				return nil, fmt.Errorf("the path %q has invalid options: %s", rawPath, err)
			}
		}

		// the WebSocket handshake is always a GET request
		if options.output == outputWebSocket && pathParts[1] != "" && pathParts[1] != http.MethodGet {
			return nil, fmt.Errorf("the path %q has invalid options: websocket works only with the GET method", rawPath)
		}

		// the same path with different options is a duplicate too
		pathKey := pathParts[1] + ":" + path
		if uniqPaths[pathKey] {
			return nil, fmt.Errorf("a duplicate path was detected: %q", rawPath)
		}
		uniqPaths[pathKey] = true

		cmdHandlers = append(cmdHandlers, command{path: path, cmd: cmd, httpMethod: pathParts[1], options: options})
	}

	return cmdHandlers, nil
}

// parseEndpointOptions - parse the endpoint options from the query part of the path ("stream=1&..."),
// the only allowed value is "1", except allow-origin
func parseEndpointOptions(rawQuery string) (endpointOptions, error) {
	var options endpointOptions

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return options, err
	}
	if len(query) == 0 {
		return options, fmt.Errorf("no options after \"?\"")
	}

	outputModes := map[string]outputMode{"stream": outputStream, "sse": outputSSE, "websocket": outputWebSocket}

	for _, name := range slices.Sorted(maps.Keys(query)) {
		mode, ok := outputModes[name]
		if !ok && name != "allow-origin" {
			return options, fmt.Errorf("unknown option %q, supported: stream, sse, websocket, allow-origin", name)
		}

		values := query[name]
		if len(values) > 1 {
			return options, fmt.Errorf("the option %q is set several times", name)
		}

		if name == "allow-origin" {
			if err := checkAllowOrigin(values[0]); err != nil {
				return options, fmt.Errorf("the option %q: %s", name, err)
			}
			options.allowOrigin = values[0]
			continue
		}

		if values[0] != "1" {
			return options, fmt.Errorf("the option %q must be set to 1, got %q", name, values[0])
		}

		if options.output != outputBuffered {
			return options, fmt.Errorf("only one of the options stream, sse and websocket can be set")
		}
		options.output = mode
	}

	if options.allowOrigin != "" && options.output != outputWebSocket {
		return options, fmt.Errorf("the option \"allow-origin\" can be used only with websocket=1")
	}

	return options, nil
}

// getShellAndParams - get default shell and command
func getShellAndParams(cmd string, appConfig Config) (shell string, params []string, err error) {
	shell, params = appConfig.defaultShell, []string{appConfig.defaultShOpt, cmd} // sh -c "cmd"

	// custom shell
	switch {
	case appConfig.shell != appConfig.defaultShell && appConfig.shell != "":
		shell = appConfig.shell
	case appConfig.shell == "":
		cmdLine, err := shellwords.Parse(cmd)
		if err != nil {
			return shell, params, fmt.Errorf("failed to parse %q: %s", cmd, err)
		}
		if len(cmdLine) == 0 {
			return shell, params, fmt.Errorf("the command %q is empty", cmd)
		}

		shell, params = cmdLine[0], cmdLine[1:]
	}

	return shell, params, nil
}

// getShellHandler - get handler function for one shell command
func getShellHandler(appConfig Config, shell string, params []string, cacheTTL raphanus.DB) func(http.ResponseWriter, *http.Request) {
	reStatusCode := regexp.MustCompile(`^\d+`)

	return func(rw http.ResponseWriter, req *http.Request) {
		limitFormBody(rw, req, appConfig)

		shellOut, exitCode, err := execShellCommand(appConfig, shell, params, req, cacheTTL)
		if writeFormError(rw, err) {
			return
		}
		if err != nil {
			log.Printf("out: %s, exec error: %s", string(shellOut), err)
		}

		customStatusCode := 0
		outText := string(shellOut)

		if err != nil && !appConfig.showErrors {
			outText = fmt.Sprintf("%s\nexec error: %s", string(shellOut), err)
		} else {
			if appConfig.setCGI {
				var headers http.Header
				outText, headers = parseCGIHeaders(outText)

				for headerKey, headerValues := range headers {
					if headerKey == "Status" {
						statusParts := reStatusCode.FindAllString(headerValues[0], -1)
						if len(statusParts) > 0 {
							statusCode, err := strconv.Atoi(statusParts[0])
							if err == nil && statusCode >= minHTTPCode && statusCode < maxHTTPCode {
								customStatusCode = statusCode
								continue
							}
						}
					}

					for _, headerValue := range headerValues {
						rw.Header().Add(headerKey, headerValue)
					}
				}

				// a redirect without an explicit Status is "302 Found" (RFC 3875 6.2.3), an explicit Status wins
				if customStatusCode == 0 && headers.Get("Location") != "" {
					customStatusCode = http.StatusFound
				}
			}
		}

		rw.Header().Set(exitCodeHeader, strconv.Itoa(exitCode))

		if customStatusCode > 0 {
			rw.WriteHeader(customStatusCode)
		} else if exitCode > 0 && appConfig.intServerErr {
			rw.WriteHeader(http.StatusInternalServerError)
		}

		responseWrite(rw, outText)
	}
}

// limitFormBody - limit the size of the request body parsed by -form (SH_MAX_UPLOAD_SIZE)
func limitFormBody(rw http.ResponseWriter, req *http.Request, appConfig Config) {
	if appConfig.setForm && appConfig.maxUploadSize > 0 && isFormData(req.Header) {
		req.Body = http.MaxBytesReader(rw, req.Body, appConfig.maxUploadSize)
	}
}

// writeFormError - if the command was not run because of the request body, send the error to the client
func writeFormError(rw http.ResponseWriter, err error) bool {
	var formErr *formError
	if !errors.As(err, &formErr) {
		return false
	}

	// don't show internal details (like temp paths) to the client
	log.Printf("read request body failed: %s", err)
	http.Error(rw, http.StatusText(formErr.status), formErr.status)
	return true
}

// execShellCommand - execute shell command, returns bytes out and error
func execShellCommand(appConfig Config, shell string, params []string, req *http.Request, cacheTTL raphanus.DB) ([]byte, int, error) {
	if appConfig.cache > 0 {
		if cacheData, err := cacheTTL.GetBytes(req.RequestURI); err != raphanuscommon.ErrKeyNotExists && err != nil {
			log.Printf("get from cache failed: %s", err)
		} else if err == nil {
			// cache hit
			return cacheData, 0, nil // TODO: save exit code in cache
		}
	}

	// the command is not bound to the request context: a client disconnect must not
	// abort a script halfway, only -timeout limits the execution time. Without -timeout
	// the command is in the process group of shell2http and gets Ctrl-C from the terminal,
	// with -timeout it is in its own process group and is killed on shutdown.
	ctx := context.Background()
	if appConfig.timeout > 0 {
		var cancelFn context.CancelFunc
		ctx, cancelFn = context.WithTimeout(appConfig.commandContext(), time.Duration(appConfig.timeout)*time.Second)
		defer cancelFn()
	}

	shellCmd, err := newShellCommand(ctx, appConfig, shell, params, req)
	if err != nil {
		return nil, 0, err
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	stderr := io.Writer(&stderrBuf)
	if appConfig.includeStderr {
		stderr = &stdoutBuf
	}

	exitCode, err := shellCmd.run(&stdoutBuf, stderr)
	if stderrBuf.Len() > 0 {
		log.Printf("stderr: %s", stderrBuf.String())
	}
	shellOut := stdoutBuf.Bytes()

	if appConfig.cache > 0 {
		if cacheErr := cacheTTL.SetBytes(req.RequestURI, shellOut, appConfig.cache); cacheErr != nil {
			log.Printf("set to cache failed: %s", cacheErr)
		}
	}

	return shellOut, exitCode, err
}

// shellCommand - a command prepared for the request, see newShellCommand
type shellCommand struct {
	cmd       *exec.Cmd
	finalizer func()     // removes temporary files of the request
	pipeErrCh chan error // result of writing the request body to the command stdin, nil if the body is not written

	// called after the command has exited, if the request body is still being read (a stalled upload),
	// to interrupt the reading, nil - wait for it as long as it takes (the buffered mode)
	interruptInput func()
}

// newShellCommand - prepare the command for the request: environment, form variables and uploaded files,
// CGI variables and the request body on stdin. If ctx can be canceled, the shell and all processes it started
// are killed when ctx is done. On error the command must not be run.
func newShellCommand(ctx context.Context, appConfig Config, shell string, params []string, req *http.Request) (*shellCommand, error) {
	var osExecCommand *exec.Cmd
	if ctx.Done() != nil {
		osExecCommand = exec.CommandContext(ctx, shell, params...) // #nosec
		setProcessGroup(osExecCommand)
		// don't wait for processes that escaped the process group and still hold stdout/stderr
		osExecCommand.WaitDelay = waitDelay
	} else {
		osExecCommand = exec.Command(shell, params...) // #nosec
	}

	proxySystemEnv(osExecCommand, appConfig)

	shellCmd := &shellCommand{cmd: osExecCommand, finalizer: func() {}}
	if appConfig.setForm {
		var err error
		if shellCmd.finalizer, err = getForm(osExecCommand, req, appConfig.formCheckRe); err != nil {
			// fail closed: don't run the command with some of the form variables missing
			shellCmd.finalizer()
			return nil, err
		}
	}

	if appConfig.setCGI {
		// get request body data data to stdin of script (if not parse form vars above)
		writeBody := (req.Method == "POST" || req.Method == "PUT" || req.Method == "PATCH") && (!appConfig.setForm || !isFormData(req.Header))

		if writeBody && req.ContentLength < 0 {
			// a chunked body has no length, read it all to set CONTENT_LENGTH (RFC 3875 4.1.2)
			removeSpool, spoolErr := spoolBody(req)
			if spoolErr != nil {
				shellCmd.finalizer()
				return nil, newFormParseError(spoolErr)
			}
			formFinalizer := shellCmd.finalizer
			shellCmd.finalizer = func() {
				removeSpool()
				formFinalizer()
			}
		}

		setCGIEnv(osExecCommand, req, appConfig, writeBody)

		if writeBody {
			if stdin, pipeErr := osExecCommand.StdinPipe(); pipeErr != nil {
				log.Println("write request body data to shell failed:", pipeErr)
			} else {
				pipeErrCh := make(chan error, 1)
				shellCmd.pipeErrCh = pipeErrCh
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

	return shellCmd, nil
}

// run - run the command with the output to stdout and stderr (may be the same writer),
// removes temporary files of the request, returns the exit code
func (sc *shellCommand) run(stdout, stderr io.Writer) (int, error) {
	sc.cmd.Stdout, sc.cmd.Stderr = stdout, stderr
	err := sc.cmd.Run()

	if errors.Is(err, exec.ErrWaitDelay) {
		// the command exited successfully, but some background process kept its output open
		log.Printf("command exited, but its output was not closed within %s", waitDelay)
		err = nil
	}

	if sc.pipeErrCh != nil {
		var pipeErr error
		select {
		case pipeErr = <-sc.pipeErrCh:
		default:
			// the request body is still being read (eg: a stalled upload)
			if sc.interruptInput != nil {
				sc.interruptInput()
			}
			pipeErr = <-sc.pipeErrCh
		}
		if pipeErr != nil {
			log.Println("write request body data to shell failed:", pipeErr)
		}
	}

	sc.finalizer()

	// -1 if the command was not started (eg: the shell is not found), the same as for a killed command
	exitCode := -1
	if sc.cmd.ProcessState != nil {
		exitCode = sc.cmd.ProcessState.ExitCode()
	}

	return exitCode, err
}

// setupHandlers - setup http handlers
func setupHandlers(cmdHandlers []command, appConfig Config, cacheTTL raphanus.DB) ([]command, error) {
	resultHandlers := []command{}
	indexLiHTML := []string{}
	existsRootPath := false

	// map[path][http-method]handler
	groupedCmd := map[string]map[string]http.HandlerFunc{}
	cmdsForLog := map[string][]string{}

	for _, row := range cmdHandlers {
		path, cmd := row.path, row.cmd
		shell, params, err := getShellAndParams(cmd, appConfig)
		if err != nil {
			return nil, err
		}

		existsRootPath = existsRootPath || path == "/"

		methodDesc := ""
		if row.httpMethod != "" {
			methodDesc = row.httpMethod + ": "
		}
		indexLiHTML = append(indexLiHTML, fmt.Sprintf(`<li><a href=".%s">%s%s</a> <span style="color: #888">- %s</span></li>`, html.EscapeString(path), methodDesc, html.EscapeString(path), html.EscapeString(cmd)))
		cmdsForLog[path] = append(cmdsForLog[path], cmd)

		var shellHandler http.HandlerFunc
		switch row.options.output {
		case outputBuffered:
			shellHandler = getShellHandler(appConfig, shell, params, cacheTTL)
		case outputWebSocket:
			shellHandler = getWebSocketHandler(appConfig, shell, params, row.options.allowOrigin)
		default:
			shellHandler = getStreamHandler(appConfig, shell, params, row.options.output)
		}

		handler := mwMethodOnly(shellHandler, row.httpMethod)
		if _, ok := groupedCmd[path]; !ok {
			groupedCmd[path] = map[string]http.HandlerFunc{}
		}
		groupedCmd[path][row.httpMethod] = handler
	}

	for path, cmds := range groupedCmd {
		handler, err := mwMultiMethod(cmds)
		if err != nil {
			return nil, err
		}
		resultHandlers = append(resultHandlers, command{
			path:    path,
			handler: handler,
			cmd:     strings.Join(cmdsForLog[path], "; "),
		})
	}

	// --------------
	if appConfig.addExit {
		resultHandlers = append(resultHandlers, command{
			path: "/exit",
			cmd:  "/exit",
			handler: func(rw http.ResponseWriter, _ *http.Request) {
				responseWrite(rw, "Bye...")
				go os.Exit(0)
			},
		})

		indexLiHTML = append(indexLiHTML, fmt.Sprintf(`<li><a href=".%s">%s</a></li>`, "/exit", "/exit"))
	}

	// --------------
	if !appConfig.noIndex && !existsRootPath {
		indexHTML := fmt.Sprintf(indexTmpl, version, strings.Join(indexLiHTML, "\n"))
		resultHandlers = append(resultHandlers, command{
			path: "/",
			cmd:  "index page",
			handler: func(rw http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/" {
					log.Printf("%q - 404", req.URL.Path)
					http.NotFound(rw, req)
					return
				}

				responseWrite(rw, indexHTML)
			},
		})
	}

	return resultHandlers, nil
}

// responseWrite - write text to response
func responseWrite(rw io.Writer, text string) {
	if _, err := io.WriteString(rw, text); err != nil {
		log.Printf("print string failed: %s", err)
	}
}

// setCGIEnv - set some CGI variables, hasBody - the request body is passed to the script stdin
func setCGIEnv(cmd *exec.Cmd, req *http.Request, appConfig Config, hasBody bool) {
	// set HTTP_* variables
	for headerName, headerValues := range req.Header {
		if !reHeaderName.MatchString(headerName) {
			// "X-Foo_Bar" would collide with "X-Foo-Bar" and could spoof a header set by a proxy,
			// other characters don't give a valid variable name (the same rule as in Apache httpd)
			continue
		}
		envName := strings.ToUpper(strings.ReplaceAll(headerName, "-", "_"))
		switch envName {
		case "PROXY": // httpoxy
			continue
		case "AUTHORIZATION", "PROXY_AUTHORIZATION": // credentials must not reach scripts (RFC 3875 4.1.18)
			continue
		}
		// repeated headers are joined into one value (RFC 3875 4.1.18), cookies use their own separator (RFC 6265 5.4)
		separator := ", "
		if envName == "COOKIE" {
			separator = "; "
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("HTTP_%s=%s", envName, strings.Join(headerValues, separator)))
	}

	// the request has passed mwBasicAuth, so expose the user name (but never the password)
	if len(appConfig.auth.users) > 0 {
		if reqUser, _, ok := req.BasicAuth(); ok {
			cmd.Env = append(cmd.Env, "AUTH_TYPE=Basic", "REMOTE_USER="+reqUser)
		}
	}

	// Host header is moved by net/http from req.Header to req.Host
	if req.Host != "" {
		cmd.Env = append(cmd.Env, "HTTP_HOST="+req.Host)
	}

	// CONTENT_LENGTH and CONTENT_TYPE are set without HTTP_ prefix (RFC 3875),
	// CONTENT_LENGTH is the length of the body on stdin, not set if the body was consumed by -form
	if hasBody && req.ContentLength > 0 {
		cmd.Env = append(cmd.Env, "CONTENT_LENGTH="+strconv.FormatInt(req.ContentLength, 10))
	}
	if contentType := req.Header.Get("Content-Type"); contentType != "" {
		cmd.Env = append(cmd.Env, "CONTENT_TYPE="+contentType)
	}

	if req.TLS != nil {
		cmd.Env = append(cmd.Env, "HTTPS=on")
	}

	remoteHost, remotePort, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		log.Printf("failed to parse remote address %q: %s", req.RemoteAddr, err)
	}
	serverName, serverPort := getServerNameAndPort(req, appConfig)
	scriptName, pathInfo := splitScriptPath(req.Pattern, req.URL.Path)

	CGIVars := [...]struct {
		cgiName, value string
	}{
		{"GATEWAY_INTERFACE", "CGI/1.1"},
		{"PATH_INFO", pathInfo},
		{"QUERY_STRING", req.URL.RawQuery},
		{"REMOTE_ADDR", remoteHost},
		{"REMOTE_HOST", remoteHost}, // without DNS lookup it is the same as REMOTE_ADDR (RFC 3875 4.1.9)
		{"REMOTE_PORT", remotePort},
		{"REQUEST_METHOD", req.Method},
		{"REQUEST_URI", req.RequestURI},
		{"SCRIPT_NAME", scriptName},
		{"SERVER_NAME", serverName},
		{"SERVER_PORT", serverPort},
		{"SERVER_PROTOCOL", req.Proto},
		{"SERVER_SOFTWARE", "shell2http"},
	}

	for _, row := range CGIVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", row.cgiName, row.value))
	}
}

// getServerNameAndPort - get SERVER_NAME from the Host header and SERVER_PORT from the accepted connection,
// -host/-port are only a fallback (-host may be empty and -port may be 0)
func getServerNameAndPort(req *http.Request, appConfig Config) (string, string) {
	serverName, serverPort := appConfig.host, strconv.Itoa(appConfig.port)

	if localAddr, ok := req.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if host, port, err := net.SplitHostPort(localAddr.String()); err == nil {
			serverName, serverPort = host, port
		}
	}

	if req.Host != "" {
		if host, _, err := net.SplitHostPort(req.Host); err == nil {
			serverName = host
		} else {
			serverName = strings.TrimSuffix(strings.TrimPrefix(req.Host, "["), "]")
		}
	}

	return serverName, serverPort
}

// splitScriptPath - split the request path into SCRIPT_NAME (the part matched by the handler pattern)
// and PATH_INFO (the rest), like Apache httpd and net/http/cgi:
//
//	pattern "/env", path "/env"             -> "/env", ""
//	pattern "/sub/", path "/sub/extra/path" -> "/sub", "/extra/path"
//	pattern "/", path "/any/path"           -> "", "/any/path"
func splitScriptPath(pattern, urlPath string) (scriptName, pathInfo string) {
	switch {
	case strings.HasSuffix(pattern, "...}"): // "/files/{path...}" is the same subtree as "/files/"
		pattern = pattern[:strings.LastIndex(pattern, "{")]
	case pattern == "" || !strings.HasSuffix(pattern, "/"): // an exact pattern (or "/path/{$}")
		return urlPath, ""
	}

	// the script owns as many leading path segments as there are in the pattern
	segments := strings.Count(strings.TrimSuffix(pattern, "/"), "/")
	end := 0
	for range segments {
		next := strings.IndexByte(urlPath[end+1:], '/')
		if next < 0 {
			return urlPath, ""
		}
		end += next + 1
	}

	return urlPath[:end], urlPath[end:]
}

/*
	parse headers from script output:

Header-name1: value1\n
Header-name2: value2\n
\n
text
*/
func parseCGIHeaders(shellOut string) (string, http.Header) {
	parts := reCGIHeadersEnd.Split(shellOut, 2)
	if len(parts) == 2 {
		headerLines := reCGINewLine.Split(parts[0], -1)
		headersMap := http.Header{}

		for _, headerLine := range headerLines {
			headerParts := reCGIHeaderLine.FindStringSubmatch(headerLine)
			if len(headerParts) == 3 {
				// header names are case-insensitive, a header may be repeated (eg: Set-Cookie), a value may be empty
				headersMap.Add(headerParts[1], strings.TrimSpace(headerParts[2]))
			} else {
				// headers is not valid, return all text
				return shellOut, nil
			}
		}

		return parts[1], headersMap
	}

	// headers don't found, return all text
	return shellOut, nil
}

var (
	safeFileNameRe  = regexp.MustCompile(`[^\.\w\-]+`)
	unsafeVarNameRe = regexp.MustCompile(`\W`)
)

// formError - the form could not be parsed or the uploaded files could not be saved,
// the command must not be run, status is the HTTP status for the response
type formError struct {
	status int
	err    error
}

func (e *formError) Error() string { return e.err.Error() }
func (e *formError) Unwrap() error { return e.err }

// newFormParseError - wrap an error of parsing the request body, a body over the limit gets 413
func newFormParseError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) || errors.Is(err, multipart.ErrMessageTooLarge) {
		return &formError{status: http.StatusRequestEntityTooLarge, err: err}
	}

	return &formError{status: http.StatusBadRequest, err: err}
}

// spoolBody - read a request body of unknown length into a temp file, then the body is read from that file
// and req.ContentLength is set, the returned function removes the file
func spoolBody(req *http.Request) (func(), error) {
	spool, err := os.CreateTemp("", "shell2http-body-")
	if err != nil {
		return func() {}, err
	}
	remove := func() {
		if err := spool.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			log.Printf("close temp file failed: %s", err)
		}
		if err := os.Remove(spool.Name()); err != nil {
			log.Printf("remove temp file failed: %s", err)
		}
	}

	size, err := io.Copy(spool, req.Body)
	if err == nil {
		_, err = spool.Seek(0, io.SeekStart)
	}
	if err != nil {
		remove()
		return func() {}, err
	}

	req.Body, req.ContentLength = spool, size
	return remove, nil
}

// isFormValueAllowed - check a value with the -form-check regexp, without the regexp all values are allowed
func isFormValueAllowed(value string, checkFormRe *regexp.Regexp) bool {
	return checkFormRe == nil || checkFormRe.MatchString(value)
}

// formValue - join the values of a form field with ",", with -form-check only the values
// that match the regexp are used, and the joined value must match it too
func formValue(values []string, checkFormRe *regexp.Regexp) (string, bool) {
	checkedValues := make([]string, 0, len(values))
	for _, v := range values {
		if isFormValueAllowed(v, checkFormRe) {
			checkedValues = append(checkedValues, v)
		}
	}
	if len(checkedValues) == 0 {
		return "", false
	}

	value := strings.Join(checkedValues, ",")
	if !isFormValueAllowed(value, checkFormRe) {
		return "", false
	}

	return value, true
}

// formVar - form key and the name of its variable (without the v_/filepath_/filename_ prefix)
type formVar struct {
	key, name string
}

// formVarNames - variable names for the form keys (non-word characters replaced with "_"), in a fixed order.
// When several keys give the same name only one of them is used: the key that is already a valid name,
// otherwise the first one in sorted order ("a-b" over "a.b"), so the same request always sets the same variables.
func formVarNames[V any](form map[string]V) []formVar {
	isRenamed := func(key string) int {
		if unsafeVarNameRe.MatchString(key) {
			return 1
		}
		return 0
	}

	keys := slices.Collect(maps.Keys(form))
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(isRenamed(a), isRenamed(b)), cmp.Compare(a, b))
	})

	result := make([]formVar, 0, len(keys))
	used := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		name := unsafeVarNameRe.ReplaceAllString(key, "_")
		if _, ok := used[name]; ok {
			continue
		}
		used[name] = struct{}{}
		result = append(result, formVar{key: key, name: name})
	}

	return result
}

// getForm - parse form into environment vars, also handle uploaded files.
// On error the command must not be run, the returned finalizer must be called anyway.
func getForm(cmd *exec.Cmd, req *http.Request, checkFormRe *regexp.Regexp) (func(), error) {
	tempDirs := []string{}
	finalizer := func() {
		for _, tempDir := range tempDirs {
			if err := os.RemoveAll(tempDir); err != nil {
				log.Println(err)
			}
		}
	}

	if err := req.ParseForm(); err != nil {
		return finalizer, newFormParseError(err)
	}

	if isMultipartFormData(req.Header) {
		if err := req.ParseMultipartForm(maxMemoryForUploadFile); err != nil {
			return finalizer, newFormParseError(err)
		}
	}

	for _, fv := range formVarNames(req.Form) {
		value, ok := formValue(req.Form[fv.key], checkFormRe)
		if !ok {
			continue
		}

		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "v_"+fv.name, value))
	}

	// handle uploaded files, save all to temporary files and set variables filename_XXX, filepath_XXX
	if req.MultipartForm != nil {
		for _, fv := range formVarNames(req.MultipartForm.File) {
			key, value := fv.name, req.MultipartForm.File[fv.key]
			// a file whose name (raw, from the client) doesn't pass -form-check is skipped entirely
			if len(value) == 1 && isFormValueAllowed(value[0].Filename, checkFormRe) {
				var (
					uplFile     multipart.File
					outFile     *os.File
					tempDir     string
					err         error
					reqFileName = value[0].Filename
				)

				errCreate := errChain(func() error {
					uplFile, err = value[0].Open()
					return err
				}, func() error {
					tempDir, err = os.MkdirTemp("", "shell2http_")
					if err == nil {
						tempDirs = append(tempDirs, tempDir)
					}
					return err
				}, func() error {
					prefix := safeFileNameRe.ReplaceAllString(reqFileName, "")
					outFile, err = os.CreateTemp(tempDir, prefix+"_")
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
					return finalizer, &formError{status: http.StatusInternalServerError, err: errClose}
				}

				if errCreate != nil {
					return finalizer, &formError{status: http.StatusInternalServerError, err: errCreate}
				}

				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "filepath_"+key, outFile.Name()))
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "filename_"+key, reqFileName))
			}
		}
	}

	return finalizer, nil
}

// isMultipartFormData - check header for multipart/form-data
func isMultipartFormData(headers http.Header) bool {
	if contentType, ok := headers["Content-Type"]; ok && len(contentType) == 1 && strings.HasPrefix(strings.ToLower(contentType[0]), "multipart/form-data;") {
		return true
	}

	return false
}

// isFormData - check that request body will be parsed by -form (urlencoded or multipart/form-data)
func isFormData(headers http.Header) bool {
	return strings.HasPrefix(strings.ToLower(headers.Get("Content-Type")), "application/x-www-form-urlencoded") || isMultipartFormData(headers)
}

// proxySystemEnv - proxy some system vars
func proxySystemEnv(cmd *exec.Cmd, appConfig Config) {
	varsNames := []string{"PATH", "HOME", "LANG", "USER", "TMPDIR"}

	if runtime.GOOS == "windows" {
		varsNames = append(varsNames, "USERNAME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "TEMP", "TMP", "PATHEXT", "COMSPEC", "OS")
	}

	if appConfig.exportVars != "" {
		for _, name := range strings.Split(appConfig.exportVars, ",") {
			if name = strings.TrimSpace(name); name != "" {
				varsNames = append(varsNames, name)
			}
		}
	}

	// environment names are case-sensitive on POSIX and case-insensitive on Windows
	sameName := func(a, b string) bool { return a == b }
	if runtime.GOOS == "windows" {
		sameName = strings.EqualFold
	}

	for _, envRaw := range os.Environ() {
		env := strings.SplitN(envRaw, "=", 2)
		if env[0] != shBasicAuthVar {
			if appConfig.exportAllVars {
				cmd.Env = append(cmd.Env, envRaw)
			} else {
				for _, envVarName := range varsNames {
					if sameName(env[0], envVarName) {
						cmd.Env = append(cmd.Env, envRaw)
						break
					}
				}
			}
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

func main() {
	appConfig, err := getConfig()
	if err != nil {
		log.Fatal(err)
	}

	cmdHandlers, err := parsePathAndCommands(flag.Args())
	if err != nil {
		log.Fatalf("failed to parse arguments: %s", err)
	}

	var cacheTTL raphanus.DB
	if appConfig.cache > 0 {
		cacheTTL = raphanus.New()
	}

	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stopSignals()
	appConfig.shutdownCtx = shutdownCtx
	appConfig.hijacked = &sync.WaitGroup{}

	cmdHandlers, err = setupHandlers(cmdHandlers, *appConfig, cacheTTL)
	if err != nil {
		log.Fatal(err)
	}
	for _, handler := range cmdHandlers {
		handlerFunc := handler.handler
		if len(appConfig.auth.users) > 0 {
			handlerFunc = mwBasicAuth(handlerFunc, appConfig.auth)
		}
		if appConfig.oneThread {
			handlerFunc = mwOneThread(handlerFunc)
		}
		handlerFunc = mwLogging(mwCommonHeaders(handlerFunc))

		http.HandleFunc(handler.path, handlerFunc)
		log.Printf("register: %s (%s)\n", handler.path, handler.cmd)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(appConfig.host, strconv.Itoa(appConfig.port)))
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("listen %s\n", appConfig.readableURL(listener.Addr()))

	server := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
	}
	if appConfig.timeout > 0 {
		// the whole request, including the body, must be read within the command timeout,
		// so a stalled body cannot hold the handler (and the -one-thread lock) forever
		server.ReadTimeout = time.Duration(appConfig.timeout) * time.Second
	}

	serveErr := make(chan error, 1)
	go func() {
		if len(appConfig.cert) > 0 && len(appConfig.key) > 0 {
			serveErr <- server.ServeTLS(listener, appConfig.cert, appConfig.key)
		} else {
			serveErr <- server.Serve(listener)
		}
	}()

	select {
	case err := <-serveErr:
		log.Fatal(err)
	case <-shutdownCtx.Done():
	}

	// a second Ctrl-C stops the server at once
	stopSignals()
	log.Printf("shutting down, running commands are stopped")

	// running commands are killed by shutdownCtx, wait for their handlers to send the rest of the response
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown failed: %s", err)
	}

	// server.Shutdown doesn't wait for hijacked connections (websocket)
	hijackedDone := make(chan struct{})
	go func() {
		appConfig.hijacked.Wait()
		close(hijackedDone)
	}()
	select {
	case <-hijackedDone:
	case <-ctx.Done():
		log.Printf("shutdown failed: websocket connections are not closed within %s", shutdownTimeout)
	}
}

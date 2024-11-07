package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
	"github.com/msoap/raphanus"
	raphanuscommon "github.com/msoap/raphanus/common"
)

var version = "dev"

const (
	// defaultPort - default port for http-server
	defaultPort = 8080

	// shBasicAuthVar - name of env var for basic auth credentials
	shBasicAuthVar = "SH_BASIC_AUTH"

	// defaultShellPOSIX - shell executable by default in POSIX systems
	defaultShellPOSIX = "sh"

	// defaultShellWindows - shell executable by default in Windows
	defaultShellWindows = "cmd"

	// defaultShellPlan9 - shell executable by default in Plan9
	defaultShellPlan9 = "rc"

	maxHTTPCode            = 1000
	maxMemoryForUploadFile = 65536
)

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
		path, cmd := args[i], args[i+1]
		if uniqPaths[path] {
			return nil, fmt.Errorf("a duplicate path was detected: %q", path)
		}

		pathParts := pathRe.FindStringSubmatch(path)
		if len(pathParts) != 3 {
			return nil, fmt.Errorf("the path %q must begin with the prefix /, and with optional METHOD: prefix", path)
		}
		cmdHandlers = append(cmdHandlers, command{path: pathParts[2], cmd: cmd, httpMethod: pathParts[1]})

		uniqPaths[path] = true
	}

	return cmdHandlers, nil
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

		shell, params = cmdLine[0], cmdLine[1:]
	}

	return shell, params, nil
}

// getShellHandler - get handler function for one shell command
func getShellHandler(appConfig Config, shell string, params []string, cacheTTL raphanus.DB) func(http.ResponseWriter, *http.Request) {
	reStatusCode := regexp.MustCompile(`^\d+`)

	return func(rw http.ResponseWriter, req *http.Request) {
		shellOut, exitCode, err := execShellCommand(appConfig, shell, params, req, cacheTTL)
		if err != nil {
			log.Printf("out: %s, exec error: %s", string(shellOut), err)
		}

		customStatusCode := 0
		outText := string(shellOut)

		if err != nil && !appConfig.showErrors {
			outText = fmt.Sprintf("%s\nexec error: %s", string(shellOut), err)
		} else {
			if appConfig.setCGI {
				var headers map[string]string
				outText, headers = parseCGIHeaders(outText)

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
			}
		}

		rw.Header().Set("X-Shell2http-Exit-Code", strconv.Itoa(exitCode))

		if customStatusCode > 0 {
			rw.WriteHeader(customStatusCode)
		} else if exitCode > 0 && appConfig.intServerErr {
			rw.WriteHeader(http.StatusInternalServerError)
		}

		responseWrite(rw, outText)
	}
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

	ctx := req.Context()
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
		if finalizer, err = getForm(osExecCommand, req, appConfig.formCheckRe); err != nil {
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

		// get request body data data to stdin of script (if not parse form vars above)
		if (req.Method == "POST" || req.Method == "PUT" || req.Method == "PATCH") && !appConfig.setForm {
			if stdin, pipeErr := osExecCommand.StdinPipe(); pipeErr != nil {
				log.Println("write request body data to shell failed:", pipeErr)
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
		var stderrBuf bytes.Buffer
		osExecCommand.Stderr = &stderrBuf
		shellOut, err = osExecCommand.Output()
		if stderrBuf.Len() > 0 {
			log.Printf("stderr: %s", stderrBuf.String())
		}
	}

	if waitPipeWrite {
		if pipeErr := <-pipeErrCh; pipeErr != nil {
			log.Println("write request body data to shell failed:", pipeErr)
		}
	}

	finalizer()

	if appConfig.cache > 0 {
		if cacheErr := cacheTTL.SetBytes(req.RequestURI, shellOut, appConfig.cache); cacheErr != nil {
			log.Printf("set to cache failed: %s", cacheErr)
		}
	}

	exitCode := osExecCommand.ProcessState.ExitCode()

	return shellOut, exitCode, err
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
		indexLiHTML = append(indexLiHTML, fmt.Sprintf(`<li><a href=".%s">%s%s</a> <span style="color: #888">- %s<span></li>`, path, methodDesc, path, html.EscapeString(cmd)))
		cmdsForLog[path] = append(cmdsForLog[path], cmd)

		handler := mwMethodOnly(getShellHandler(appConfig, shell, params, cacheTTL), row.httpMethod)
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
					log.Printf("%s - 404", req.URL.Path)
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

	remoteHost, remotePort, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		log.Printf("failed to parse remote address %s: %s", req.RemoteAddr, err)
	}
	CGIVars := [...]struct {
		cgiName, value string
	}{
		{"PATH_INFO", req.URL.Path},
		{"QUERY_STRING", req.URL.RawQuery},
		{"REMOTE_ADDR", remoteHost},
		{"REMOTE_PORT", remotePort},
		{"REQUEST_METHOD", req.Method},
		{"REQUEST_URI", req.RequestURI},
		{"SCRIPT_NAME", req.URL.Path},
		{"SERVER_NAME", appConfig.host},
		{"SERVER_PORT", strconv.Itoa(appConfig.port)},
		{"SERVER_PROTOCOL", req.Proto},
		{"SERVER_SOFTWARE", "shell2http"},
	}

	for _, row := range CGIVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", row.cgiName, row.value))
	}
}

/*
	parse headers from script output:

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
				return shellOut, nil
			}
		}

		return parts[1], headersMap
	}

	// headers don't found, return all text
	return shellOut, nil
}

// getForm - parse form into environment vars, also handle uploaded files
func getForm(cmd *exec.Cmd, req *http.Request, checkFormRe *regexp.Regexp) (func(), error) {
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

	for key, values := range req.Form {
		if checkFormRe != nil {
			checkedValues := []string{}
			for _, v := range values {
				if checkFormRe.MatchString(v) {
					checkedValues = append(checkedValues, v)
				}
			}
			values = checkedValues
		}
		if len(values) == 0 {
			continue
		}

		value := strings.Join(values, ",")
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "v_"+key, value))
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

// isMultipartFormData - check header for multipart/form-data
func isMultipartFormData(headers http.Header) bool {
	if contentType, ok := headers["Content-Type"]; ok && len(contentType) == 1 && strings.HasPrefix(contentType[0], "multipart/form-data;") {
		return true
	}

	return false
}

// proxySystemEnv - proxy some system vars
func proxySystemEnv(cmd *exec.Cmd, appConfig Config) {
	varsNames := []string{"PATH", "HOME", "LANG", "USER", "TMPDIR"}

	if runtime.GOOS == "windows" {
		varsNames = append(varsNames, "USERNAME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "TEMP", "TMP", "PATHEXT", "COMSPEC", "OS")
	}

	if appConfig.exportVars != "" {
		varsNames = append(varsNames, strings.Split(appConfig.exportVars, ",")...)
	}

	for _, envRaw := range os.Environ() {
		env := strings.SplitN(envRaw, "=", 2)
		if env[0] != shBasicAuthVar {
			if appConfig.exportAllVars {
				cmd.Env = append(cmd.Env, envRaw)
			} else {
				for _, envVarName := range varsNames {
					if strings.ToUpper(env[0]) == envVarName {
						cmd.Env = append(cmd.Env, envRaw)
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

	if len(appConfig.cert) > 0 && len(appConfig.key) > 0 {
		log.Fatal(http.ServeTLS(listener, nil, appConfig.cert, appConfig.key))
	} else {
		log.Fatal(http.Serve(listener, nil))
	}
}

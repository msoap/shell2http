package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type authUsers struct {
	users map[string]string
}

func (au *authUsers) String() string {
	if au != nil {
		return fmt.Sprintf("%v", au.users)
	}
	return ""
}

func (au *authUsers) Set(in string) error {
	basicAuthParts := strings.SplitN(in, ":", 2)
	if len(basicAuthParts) != 2 {
		return fmt.Errorf("HTTP basic authentication must be in format: name:password, got: %s", in)
	}
	au.add(basicAuthParts[0], basicAuthParts[1])

	return nil
}

func (au *authUsers) add(user, pass string) {
	if au.users == nil {
		au.users = make(map[string]string)
	}
	au.users[user] = pass
}

func (au authUsers) isAllow(user, pass string) bool {
	storedPass, ok := au.users[user]
	return ok && storedPass == pass
}

// Config - config struct
type Config struct {
	port          int            // server port
	cache         int            // caching command out (in seconds)
	timeout       int            // timeout for shell command (in seconds)
	host          string         // server host
	exportVars    string         // list of environment vars for export to script
	shell         string         // custom shell
	defaultShell  string         // shell by default
	defaultShOpt  string         // shell option for one-liner (-c or /C)
	cert          string         // SSL certificate
	key           string         // SSL private key path
	auth          authUsers      // basic authentication
	exportAllVars bool           // export all current environment vars
	setCGI        bool           // set CGI variables
	setForm       bool           // parse form from URL
	noIndex       bool           // don't generate index page
	addExit       bool           // add /exit command
	oneThread     bool           // run each shell commands in one thread
	showErrors    bool           // returns the standard output even if the command exits with a non-zero exit code
	includeStderr bool           // also returns output written to stderr (default is stdout only)
	intServerErr  bool           // return 500 error if shell status code != 0
	formCheckRe   *regexp.Regexp // regexp for check form fields
}

// getConfig - parse arguments
func getConfig() (*Config, error) {
	var (
		cfg            Config
		logFilename    string
		noLogTimestamp bool
	)

	switch runtime.GOOS {
	case "plan9":
		cfg.defaultShell, cfg.defaultShOpt = defaultShellPlan9, "-c"
	case "windows":
		cfg.defaultShell, cfg.defaultShOpt = defaultShellWindows, "/C"
	default:
		cfg.defaultShell, cfg.defaultShOpt = defaultShellPOSIX, "-c"
	}

	flag.StringVar(&logFilename, "log", "", "log `filename`, default - STDOUT")
	flag.BoolVar(&noLogTimestamp, "no-log-timestamp", false, "log output without timestamps")
	flag.IntVar(&cfg.port, "port", defaultPort, "`port` for http server")
	flag.StringVar(&cfg.host, "host", "", "`host` for http server")
	flag.BoolVar(&cfg.setCGI, "cgi", false, "run scripts in CGI-mode")
	flag.StringVar(&cfg.exportVars, "export-vars", "", "export environment vars (\"VAR1,VAR2,...\")")
	flag.BoolVar(&cfg.exportAllVars, "export-all-vars", false, "export all current environment vars")
	flag.BoolVar(&cfg.setForm, "form", false, "parse query into environment vars, handle uploaded files")
	flag.BoolVar(&cfg.noIndex, "no-index", false, "don't generate index page")
	flag.BoolVar(&cfg.addExit, "add-exit", false, "add /exit command")
	flag.StringVar(&cfg.shell, "shell", cfg.defaultShell, `custom shell or "" for execute without shell`)
	flag.IntVar(&cfg.cache, "cache", 0, "caching command out (in `seconds`)")
	flag.BoolVar(&cfg.oneThread, "one-thread", false, "run each shell command in one thread")
	flag.BoolVar(&cfg.showErrors, "show-errors", false, "show the standard output even if the command exits with a non-zero exit code")
	flag.BoolVar(&cfg.includeStderr, "include-stderr", false, "include stderr to output (default is stdout only)")
	flag.BoolVar(&cfg.intServerErr, "500", false, "return 500 error if shell exit code != 0")
	flag.StringVar(&cfg.cert, "cert", "", "SSL certificate `path` (if specified -cert/-key options - run https server)")
	flag.StringVar(&cfg.key, "key", "", "SSL private key `/path/...`")
	flag.Var(&cfg.auth, "basic-auth", "setup HTTP Basic Authentication (\"user_name:password\"), can be used several times")
	flag.IntVar(&cfg.timeout, "timeout", 0, "set `timeout` for execute shell command (in seconds)")

	formCheck := flag.String("form-check", "", "regexp for check form fields (pass only vars that match the regexp)")

	flag.Usage = func() {
		fmt.Printf("usage: %s [options] /path \"shell command\" /path2 \"shell command2\"\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	getVersion := flag.Bool("version", false, "get version")

	flag.Parse()

	if *getVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// setup log file
	if len(logFilename) > 0 {
		fhLog, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return nil, fmt.Errorf("error opening log file: %v", err)
		}
		log.SetOutput(fhLog)
	}

	if noLogTimestamp {
		log.SetFlags(0)
	}

	if len(cfg.cert) > 0 && len(cfg.key) == 0 ||
		len(cfg.cert) == 0 && len(cfg.key) > 0 {
		return nil, fmt.Errorf("requires both -cert and -key options")
	}

	if len(cfg.auth.users) == 0 && len(os.Getenv(shBasicAuthVar)) > 0 {
		if err := cfg.auth.Set(os.Getenv(shBasicAuthVar)); err != nil {
			return nil, err
		}
	}

	if cfg.shell != "" && cfg.shell != cfg.defaultShell {
		if _, err := exec.LookPath(cfg.shell); err != nil {
			return nil, fmt.Errorf("an error has occurred while searching for shell executable %q: %s", cfg.shell, err)
		}
	}

	if formCheck != nil && len(*formCheck) > 0 {
		re, err := regexp.Compile(*formCheck)
		if err != nil {
			return nil, fmt.Errorf("an error has occurred while compiling regexp %s: %s", *formCheck, err)
		}
		cfg.formCheckRe = re
	}

	return &cfg, nil
}

// readableURL - get readable URL for logging
func (cfg Config) readableURL(addr fmt.Stringer) string {
	prefix := "http"
	if len(cfg.cert) > 0 && len(cfg.key) > 0 {
		prefix = "https"
	}

	urlParts := strings.Split(addr.String(), ":")
	if len(urlParts) == 0 {
		log.Printf("listen address is invalid, port not found: %s", addr.String())
		return fmt.Sprintf("%s//%s/", prefix, addr.String())
	}

	port := strconv.Itoa(cfg.port)
	if port == "0" {
		port = urlParts[len(urlParts)-1]
	}

	host := cfg.host
	if host == "" {
		host = "localhost"
	}

	return fmt.Sprintf("%s://%s/", prefix, net.JoinHostPort(host, port))
}

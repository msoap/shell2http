shell2http
==========

[![Go Reference](https://pkg.go.dev/badge/github.com/msoap/shell2http.svg)](https://pkg.go.dev/github.com/msoap/shell2http)
[![Go build status](https://github.com/msoap/shell2http/actions/workflows/go.yml/badge.svg)](https://github.com/msoap/shell2http/actions/workflows/go.yml)
[![Coverage Status](https://coveralls.io/repos/github/msoap/shell2http/badge.svg?branch=master)](https://coveralls.io/github/msoap/shell2http?branch=master)
[![Report Card](https://goreportcard.com/badge/github.com/msoap/shell2http)](https://goreportcard.com/report/github.com/msoap/shell2http)
[![Github Releases](https://img.shields.io/github/downloads/msoap/shell2http/total.svg)](https://github.com/msoap/shell2http/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/msoap/shell2http.svg?maxAge=3600)](https://hub.docker.com/r/msoap/shell2http/)
[![Homebrew formula exists](https://img.shields.io/badge/homebrew-🍺-d7af72.svg)](https://github.com/msoap/shell2http#install)

HTTP-server to execute shell commands. Designed for development, prototyping or remote control.
Settings through two command line arguments, path and shell command.

Usage
-----

    shell2http [options] /path "shell command" /path2 "shell command2" ...
    options:
        -host="host"      : host IP for http server (default bind to all interfaces)
        -port=NNNN        : port for http server, 0 - to receive a random port (default 8080)
        -form             : parse query into environment vars, handle uploaded files
        -form-check       : regexp for check form fields (pass only vars that match the regexp)
        -cgi              : run scripts in CGI-mode:
                            - set environment variables with HTTP-request information
                            - write POST|PUT|PATCH-data to script STDIN (if is not set -form)
                            - parse headers from script (eg: "Location: URL\n\n")
        -export-vars=var  : export environment vars ("VAR1,VAR2,...")
                            by default export PATH, HOME, LANG, USER, TMPDIR
        -export-all-vars  : export all current environment vars
        -no-index         : don't generate index page
        -add-exit         : add /exit command
        -log=filename     : log filename, default - STDOUT
        -shell="shell"    : shell for execute command, "" - without shell (default "sh")
        -cache=N          : caching command out for N seconds
        -one-thread       : run each shell command in one thread
        -show-errors      : show the standard output even if the command exits with a non-zero exit code
        -include-stderr   : include stderr to output (default is stdout only)
        -500              : return 500 error if shell exit code != 0
        -cert=cert.pem    : SSL certificate path (if specified -cert/-key options - run https server)
        -key=key.pem      : SSL private key path
        -basic-auth=""    : setup HTTP Basic Authentication ("user_name:password"), can be used several times
        -timeout=N        : set timeout for execute shell command (in seconds)
        -no-log-timestamp : log output without timestamps
        -version
        -help

In the `-form` mode, variables are available for shell scripts:

  * $v_NNN -- data from query parameter with name "NNN" (example: `http://localhost:8080/path?NNN=123`)
  * $filepath_ID -- uploaded file path, ID - id from `<input type=file name=ID>`, temporary uploaded file will be automatically deleted
  * $filename_ID -- uploaded file name from browser

With `-form-check` option you can specify the regular expression for checking the form fields.
For example, if you want to allow only variables that contain the only digits,
you can specify the following option: `-form-check='^[0-9]+$'`.
Then only requests like `http://localhost:8080/path?NNN=123` will be produce variable `$v_NNN`.

To setup multiple auth users, you can specify the `-basic-auth` option multiple times.
The credentials for basic authentication may also be provided via the `SH_BASIC_AUTH` environment variable.
You can specify the preferred HTTP-method (via `METHOD:` prefix for path): `shell2http GET:/date date`

Install
-------

MacOS:

    brew install msoap/tools/shell2http
    # update:
    brew upgrade shell2http

Download binaries from: [releases](https://github.com/msoap/shell2http/releases/latest) (MacOS/Linux/Windows/RaspberryPi)

For Docker users, availeble tags see in [Docker Hub](https://hub.docker.com/r/msoap/shell2http):

    docker pull msoap/shell2http

Using snap (Ubuntu or any Linux distribution with snap):

    # install stable version:
    sudo snap install shell2http
    
    # install the latest version:
    sudo snap install --edge shell2http
    
    # update
    sudo snap refresh shell2http

*Notice*: the snap-package has its own sandbox with the `/bin`, `/usr/bin` directories which are not equal to system-wide `PATH` directories
and commands may not work as expected or not work at all.

Build from source (minimum Go version is 1.12):

    go install github.com/msoap/shell2http@latest
    # set link to your PATH if needed:
    ln -s $(go env GOPATH)/bin/shell2http ~/bin/shell2http

Compile for MIPS CPU (for example, for some WiFi routers like Asus):

    GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -trimpath -ldflags="-w -s" -o shell2http .

Examples
--------

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http -export-all-vars /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http -export-all-vars /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
    shell2http -export-vars=GOPATH /get 'echo $GOPATH'

<details><summary>HTML calendar for current year</summary>

```sh
shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
```
</details>

<details><summary>Get URL parameters (http://localhost:8080/form?from=10&to=100)</summary>

```sh
shell2http -form /form 'echo $v_from, $v_to'
```
</details>

<details><summary>CGI scripts</summary>

```sh
shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'
shell2http -cgi /set 'touch file; echo "Location: /another_path\n"' # redirect
shell2http -cgi /404 'echo "Status: 404"; echo; echo "404 page"' # custom HTTP code
```
</details>

<details><summary>Upload file</summary>

```sh
shell2http -form \
    GET:/form 'echo "<html><body><form method=POST action=/file enctype=multipart/form-data><input type=file name=uplfile><input type=submit></form>"' \
    POST:/file 'cat $filepath_uplfile > uploaded_file.dat; echo Ok'
```

Testing upload file with curl:

    curl -i -F uplfile=@some/file/path 'http://localhost:8080/file'

</details>

<details><summary>Simple http-proxy server (for logging all URLs)</summary>
Setup proxy as "http://localhost:8080/"

```sh
shell2http -log=/dev/null -cgi / 'echo $REQUEST_URI 1>&2; [ "$REQUEST_METHOD" == "POST" ] && post_param="-d@-"; curl -sL $post_param "$REQUEST_URI" -A "$HTTP_USER_AGENT"'
```
</details>

<details><summary>Test slow connection (http://localhost:8080/slow?duration=10)</summary>

```sh
shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'
```
</details>

<details><summary>Proxy with cache in files (for debug with production API with rate limit)</summary>
get `http://api.url/` as `http://localhost:8080/get?url=http://api.url/`

```sh
shell2http -form \
    /form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
    /get 'MD5=$(printf "%s" $v_url | md5); cat cache_$MD5 || (curl -sL $v_url | tee cache_$MD5)'
```
</details>

<details><summary>Remote sound volume control (Mac OS)</summary>

```sh
shell2http /get  'osascript -e "output volume of (get volume settings)"' \
           /up   'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")+10))"' \
           /down 'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")-10))"'
```
</details>

<details><summary>Remote control for Vox.app player (Mac OS)</summary>

```sh
shell2http /play_pause 'osascript -e "tell application \"Vox\" to playpause" && echo ok' \
           /get_info 'osascript -e "tell application \"Vox\"" -e "\"Artist: \" & artist & \"\n\" & \"Album: \" & album & \"\n\" & \"Track: \" & track" -e "end tell"'
```
</details>

<details><summary>Get four random OS X wallpapers</summary>

```sh
shell2http /img 'cat "$(ls "/Library/Desktop Pictures/"*.jpg | ruby -e "puts STDIN.readlines.shuffle[0]")"' \
           /wallpapers 'echo "<html><h3>OS X Wallpapers</h3>"; seq 4 | xargs -I@ echo "<img src=/img?@ width=500>"'
```
</details>

<details><summary>Mock service with JSON API</summary>

```sh
curl "http://some-service/v1/call1" > 1.json
shell2http -cgi /call1 'cat 1.json' /call2 'echo "Content-Type: application/json\n"; echo "{\"error\": \"ok\"}"'
```
</details>

<details><summary>Windows example</summary>

Returns value of `var` for run in Windows `cmd` (`http://localhost:8080/test?var=value123`)

```sh
shell2http.exe -form /test "echo %v_var%"
```
</details>

<details><summary>With HTTP headers</summary>

Send custom HTTP headers:

```sh
shell2http -cgi / 'echo "Content-Type: application/javascript\n"; echo "{\"error\": \"ok\"}"'
```

On Windows:

```sh
shell2http.exe -cgi / "echo Content-Type: application/javascript& echo.& echo body"
```

</details>

[More examples ...](https://github.com/msoap/shell2http/wiki)

Run from Docker-container
-------------------------
Example of `test.Dockerfile` for server for get current date:

```dockerfile
FROM msoap/shell2http
# may be install some alpine packages:
# RUN apk add --no-cache ...
CMD ["/date", "date"]
```

Build and run container:

    docker build -f test.Dockerfile -t date-server .
    docker run --rm -p 8080:8080 date-server

SSL
---

Run https server:

    shell2http -cert=./cert.pem -key=./key.pem ...

Generate self-signed certificate:

    go run $(go env GOROOT)/src/crypto/tls/generate_cert.go -host localhost

See also
--------

 * Create Telegram bot from command-line - [shell2telegram](https://github.com/msoap/shell2telegram)
 * A http daemon for local development - [devd](https://github.com/cortesi/devd)
 * Turn any program that uses STDIN/STDOUT into a WebSocket server - [websocketd](https://github.com/joewalnes/websocketd)
 * The same tool configurable via JSON - [webhook](https://github.com/adnanh/webhook)

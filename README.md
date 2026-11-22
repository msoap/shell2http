shell2http
==========

[![Go Reference](https://pkg.go.dev/badge/github.com/msoap/shell2http.svg)](https://pkg.go.dev/github.com/msoap/shell2http)
[![Go build status](https://github.com/msoap/shell2http/actions/workflows/go.yml/badge.svg)](https://github.com/msoap/shell2http/actions/workflows/go.yml)
[![Coverage Status](https://coveralls.io/repos/github/msoap/shell2http/badge.svg?branch=master)](https://coveralls.io/github/msoap/shell2http?branch=master)
[![Github Releases](https://img.shields.io/github/downloads/msoap/shell2http/total.svg)](https://github.com/msoap/shell2http/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/msoap/shell2http.svg?maxAge=3600)](https://hub.docker.com/r/msoap/shell2http/)
[![Homebrew formula exists](https://img.shields.io/badge/homebrew-🍺-d7af72.svg)](https://github.com/msoap/shell2http#install)

<img align="right" width="128" height="128" alt="shell2http logo" src="https://github.com/msoap/shell2http/assets/844117/d1b56204-2d7e-4ee7-878c-0cbc739e5c68">

HTTP-server designed to execute shell commands. It is suitable for development, prototyping or remote control,
facilitates rapid iteration and testing of shell-based functionalities.
Easy to set up using just two command-line arguments: URL-path and shell command.
Next, when an HTTP request is made for the specified path, the stdout of the shell script will be returned, that's all.

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
                            - write POST|PUT|PATCH-data to script STDIN (if is not set -form or body is not form data)
                            - parse headers from script (eg: "Location: URL\n\n")
                            - the script must always print its own headers block first (see Notes)
        -export-vars=var  : export environment vars ("VAR1,VAR2,...")
                            by default export PATH, HOME, LANG, USER, TMPDIR
        -export-all-vars  : export all current environment vars
        -no-index         : don't generate index page
        -add-exit         : add /exit command
        -log=filename     : log filename, default - STDERR
        -shell="shell"    : shell for execute command, "" - without shell (default "sh")
        -cache=N          : caching command out for N seconds
        -one-thread       : run each shell command in one thread
        -show-errors      : show the standard output even if the command exits with a non-zero exit code
        -include-stderr   : include stderr to output (default is stdout only)
        -500              : return 500 error if shell exit code != 0
        -cert=cert.pem    : SSL certificate path (if specified -cert/-key options - run https server)
        -key=key.pem      : SSL private key path
        -basic-auth=""    : setup HTTP Basic Authentication ("user_name:password"), can be used several times
        -timeout=N        : set timeout for execute shell command (in seconds),
                            on timeout the shell and all processes it started are killed,
                            also limits reading of the request body, recommended with -one-thread
        -no-log-timestamp : log output without timestamps
        -version
        -help

In the `-form` mode, variables are available for shell scripts:

  * $v_NNN -- data from query parameter with name "NNN" (example: `http://localhost:8080/path?NNN=123`)
  * $filepath_ID -- uploaded file path, ID - id from `<input type=file name=ID>`, temporary uploaded file will be automatically deleted
  * $filename_ID -- uploaded file name from browser

In variable names all characters except `A-Z`, `a-z`, `0-9` and `_` are replaced with `_`,
so different parameters can give the same variable (see [Form variable names](#form-variable-names)).

With `-form-check` option you can specify the regular expression for checking the form fields.
For example, if you want to allow only variables that contain the only digits,
you can specify the following option: `-form-check='^[0-9]+$'`.
Then only requests like `http://localhost:8080/path?NNN=123` will be produce variable `$v_NNN`.
A parameter passed several times (`?NNN=1&NNN=2`) is joined with `,`, and the joined value must match too
(use `-form-check='^[0-9,]+$'` to allow it). The regexp also applies to the uploaded file name:
a file whose name does not match is skipped, without `$filepath_ID` and `$filename_ID`.

If the form can not be parsed (malformed body, body over the size limit, failed saving of an uploaded file),
the command is not run and the client gets a `400`, `413` or `500` error.

To setup multiple auth users, you can specify the `-basic-auth` option multiple times.
The credentials for basic authentication may also be provided via the `SH_BASIC_AUTH` environment variable.
You can specify the preferred HTTP-method (via `METHOD:` prefix for path): `shell2http GET:/date date`

Endpoint options
----------------

Some settings are set per endpoint, as a query string in the path argument (it is not a part of the URL path):

    [METHOD:]/path[?option=value&option2=value2...]

    shell2http "/live?stream=1" "tail -F some.log"
    shell2http "GET:/events?sse=1" "tail -F some.log"

| Option     | Description |
|------------|-------------|
| `stream=1` | send the command output to the client as it is written, without waiting for the command to finish |
| `sse=1`    | the same as `stream`, but each line of the output is sent as a [Server-Sent Event](https://html.spec.whatwg.org/multipage/server-sent-events.html) |

  * the only allowed value is `1`, to disable an option just don't set it
  * an unknown option, a value other than `1` (`/path?sse`, `/path?sse=true`), an empty `?`
    or `stream` together with `sse` is an error on start
  * the options are not a part of the path, so `/live?stream=1` and `/live` are the same path (a duplicate),
    the path still can have different commands for different methods (`GET:/live?sse=1` and `POST:/live`)
  * without options an endpoint works as usual: the output is sent after the command has finished

Remember to quote the path argument, `?` and `&` are special characters for the shell.
See [Live output](#live-output-stream-and-sse) for details.

Install
-------

MacOS:

    brew install msoap/tools/shell2http
    # update:
    brew upgrade shell2http

Download binaries from: [releases](https://github.com/msoap/shell2http/releases/latest) (MacOS/Linux/Windows/RaspberryPi)

For Docker users, available tags see in [Docker Hub](https://hub.docker.com/r/msoap/shell2http):

    docker pull msoap/shell2http

Build from source (minimum Go version is 1.25):

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
    shell2http -export-vars=GOPATH /get 'echo $GOPATH'

<details><summary>HTML calendar for current year</summary>

```sh
shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
```
</details>

<details><summary>Using URL parameters (http://localhost:8080/form?from=10&to=100)</summary>

```sh
shell2http -form /form 'echo "$v_from, $v_to"'
```
</details>

<details><summary>CGI scripts</summary>

```sh
shell2http -cgi /user_agent 'echo "Content-Type: text/plain"; echo; echo "$HTTP_USER_AGENT"'
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

Testing by upload file with curl:

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

<details><summary>Live output of a log file (stream and SSE)</summary>

```sh
shell2http "/live?stream=1" "tail -F /var/log/system.log" "/events?sse=1" "tail -F /var/log/system.log"
```

Read the stream with curl (`-N` - don't buffer the output):

    curl -N http://localhost:8080/live
    curl -N http://localhost:8080/events

Or in a browser with [EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource):

```js
const events = new EventSource("/events");
events.onmessage = (e) => console.log(e.data);
events.addEventListener("exit", (e) => { console.log("exit code:", e.data); events.close(); });
```
</details>

<details><summary>Run command in background (don't wait for it to finish)</summary>

Redirect stdout and stderr of the background command, otherwise the request waits for it to finish:

```sh
shell2http /run 'some_command >/dev/null 2>&1 & echo started'
# or keep output in log file:
shell2http /run 'some_command >/tmp/cmd.log 2>&1 & echo started'
```

The background process keeps running even after shell2http server is stopped.
</details>

<details><summary>Proxy with cache in files (for debug API with rate limit)</summary>
get `http://api.url/` as `http://localhost:8080/get?url=http://api.url/`

```sh
shell2http -form \
    /form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
    /get 'MD5=$(printf "%s" "$v_url" | md5); cat cache_$MD5 || (curl -sL --proto "=http,https" -- "$v_url" | tee cache_$MD5)'
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

<details><summary>Mock service with JSON API</summary>

```sh
curl "http://some-service/v1/call1" > 1.json
shell2http -cgi /call1 'cat 1.json' /call2 'echo "Content-Type: application/json\n"; echo "{\"error\": \"ok\"}"'
```
</details>

<details><summary>Windows example</summary>

Returns value of `var` for run in Windows `cmd` (`http://localhost:8080/test?var=value123`)

```sh
shell2http.exe -form -form-check="^[0-9A-Za-z_]+$" /test "echo %v_var%"
```

`cmd` expands `%v_var%` before it parses the command line, so characters like `&` or `|`
in the value run as commands (`?var=x%26calc` runs `calc`). Quoting does not help,
so on Windows always limit the values with `-form-check`.
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

In some cases, such as when multiple shell scripts are running and the main script doesn't wait them properly,
you can use `--init` option for `docker run` for prevent zombie processes, see Docker [run reference](https://docs.docker.com/reference/cli/docker/container/run/#init) and about [multiple processes in a container](https://docs.docker.com/engine/containers/multi-service_container/).

SSL
---

Run https server:

    shell2http -cert=./cert.pem -key=./key.pem ...

Generate self-signed certificate:

    go run $(go env GOROOT)/src/crypto/tls/generate_cert.go -host localhost

Environment variables
---------------------

shell2http reads these environment variables on start:

| Variable             | Description |
|----------------------|-------------|
| `SH_BASIC_AUTH`      | credentials for HTTP Basic Authentication (`user_name:password`), used if `-basic-auth` is not set |
| `SH_MAX_UPLOAD_SIZE` | max size (in bytes) of a request body parsed by `-form` (query form and uploaded files), a bigger request gets a `413` error and the command is not run. Not set or `0` - no limit (default) |

Example:

    SH_MAX_UPLOAD_SIZE=10485760 shell2http -form POST:/file 'cat $filepath_uplfile > uploaded_file.dat; echo Ok'

Without a limit, uploaded files are saved to the temporary directory (`$TMPDIR`) and can fill the disk,
set `SH_MAX_UPLOAD_SIZE` if the server is reachable by untrusted clients.

Notes
-----

### Caching

With `-cache=N` option the command output is cached by the request URI (path + query string) only, this is by design.
HTTP method, request body, headers and cookies are not part of the cache key, so within N seconds:

  * requests with the same URI but different methods (`GET:/item` and `DELETE:/item`) get the same cached output, the second command is not run
  * POST/PUT/PATCH requests with the same URI but different bodies get the output of the first request
  * in `-cgi` mode, output that depends on headers or cookies (`$HTTP_COOKIE`, `$HTTP_USER_AGENT`, ...) is shared between all clients
  * output of failed commands is cached too, and a cache hit is always returned with exit code 0 (no 500 error with `-500`, `X-Shell2http-Exit-Code: 0`)

Use `-cache` only for idempotent commands whose output depends on the URI alone.

### Form variable names

In `-form` mode a parameter name becomes a variable name with all characters except `A-Z`, `a-z`, `0-9` and `_`
replaced with `_` (non-ASCII letters too, one `_` per character). So different parameters can give the same variable:
`a-b`, `a.b` and `a_b` all become `$v_a_b`, `ключ` and `имя_` both become `$v_____`.
Only one of them is used, the same one for every request:

  * the parameter whose name is already a valid variable name (`a_b`),
  * otherwise the first one in sorted (byte) order (`a-b` before `a.b`).

The other parameters are ignored. This is decided before `-form-check`: if the chosen parameter doesn't match the regexp,
the variable is not set, even when another parameter with the same variable name matches.
The same rules apply to uploaded files (`$filepath_ID`, `$filename_ID`), and a file that is not used is not saved.
Use only `A-Z`, `a-z`, `0-9` and `_` in parameter names to avoid this.

### CGI mode headers

In `-cgi` mode the headers block in the script output is optional. Output is treated as headers + body
when it starts with `Name: value` lines followed by an empty line, otherwise the whole output is the body.
Printing correct headers is the responsibility of the script:

  * if a script outputs request data (form fields, `$HTTP_*`, `$QUERY_STRING`, `$REQUEST_URI`, STDIN, ...)
    before its own headers, a client can supply the headers block,
    eg: `Location`, `Set-Cookie` or `Status` (response header injection)
  * without a `Content-Type` header, the content type is detected from the output,
    so request data that starts with an HTML tag is served as `text/html` (XSS)

So a CGI script should always print its own headers block first, at least `Content-Type` and an empty line,
even when it doesn't need any other headers:

```sh
# unsafe: shell2http -cgi -form /reflect 'echo "$v_x"'
shell2http -cgi -form /reflect 'echo "Content-Type: text/plain"; echo; echo "$v_x"'
```

The first empty line ends the headers block, everything after it is sent as the body as is.

### Live output (stream and SSE)

With the `stream=1` or `sse=1` [endpoint option](#endpoint-options) the output is sent to the client while the command is running:

  * the response headers are sent at once, with status `200`
  * `stream=1`: the output is sent as is, as `text/plain; charset=utf-8`;
    the exit code is sent at the end as the `X-Shell2http-Exit-Code` HTTP trailer
  * `sse=1`: the output is sent as `text/event-stream`, each line of stdout is a `data:` event (default type `message`),
    a line longer than 64KB is split into several events. With `-include-stderr` the stderr lines are sent as `stderr` events.
    When the command has finished, an `exit` event with the exit code is sent:

        data: first line

        event: stderr
        data: some error

        event: exit
        data: 0

  * the command (the shell and all processes it started) is killed when the client disconnects,
    on `-timeout` or on shutdown of shell2http (Unix only, on Windows only the shell process is killed)
  * a browser `EventSource` reconnects (and runs the command again) when the stream ends,
    close it on the `exit` event if the command is not endless

Not all options work for streaming endpoints:

  * `-cache` - the output is not cached
  * `-500` - not applicable, the status is sent before the command has finished
  * `-show-errors` - not applicable, an exec error is not appended to the output (see the exit code instead)
  * `-cgi` - CGI variables and the request body on STDIN work as usual, but headers are not parsed from the output
  * `-one-thread` - an endless stream holds the lock, so all other requests wait for it

Many programs buffer their output when it is not a terminal, so it comes in big chunks with a delay
(eg: `grep`, `awk`, `sed`, `python`). Use line buffering for them:
`grep --line-buffered`, `sed -u`, `python -u`, `stdbuf -oL some_command`.

A reverse proxy may buffer the response too: shell2http sends the `X-Accel-Buffering: no` header for nginx,
for other proxies disable buffering for streaming paths in their settings.

### Shutdown

On `Ctrl-C` (`SIGINT`) or `SIGTERM` shell2http stops accepting new requests, kills running commands
and waits up to 3 seconds for running requests to finish (a second `Ctrl-C` stops it at once):

  * streaming endpoints and commands with `-timeout` run in their own process group,
    the shell and all processes it started are killed (on Windows only `Ctrl-C`, and only the shell process is killed)
  * other commands are in the process group of shell2http: `Ctrl-C` in a terminal reaches them as usual,
    but on `SIGTERM` (eg: `kill <pid>`) they are not killed and shell2http waits for them up to 3 seconds
  * background processes of finished commands (`some_command >/dev/null 2>&1 &`) keep running

See also
--------

 * Command-line tool for creating Telegram bot - [shell2telegram](https://github.com/msoap/shell2telegram)
 * A http daemon for local development - [devd](https://github.com/cortesi/devd)
 * Turn any program that uses STDIN/STDOUT into a WebSocket server - [websocketd](https://github.com/joewalnes/websocketd)
 * The same tool configurable via JSON - [webhook](https://github.com/adnanh/webhook)

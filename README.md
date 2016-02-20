shell2http
==========

[![GoDoc](https://godoc.org/github.com/msoap/shell2http?status.svg)](https://godoc.org/github.com/msoap/shell2http)
[![Build Status](https://travis-ci.org/msoap/shell2http.svg?branch=master)](https://travis-ci.org/msoap/shell2http)
[![Coverage Status](https://coveralls.io/repos/github/msoap/shell2http/badge.svg?branch=master)](https://coveralls.io/github/msoap/shell2http?branch=master)
[![Github All Releases](https://img.shields.io/github/downloads/msoap/shell2http/total.svg)](https://github.com/msoap/shell2http/releases/latest)
[![Homebrew formula exists](https://img.shields.io/badge/homebrew-🍺-d7af72.svg)](https://github.com/msoap/shell2http#install)
[![Report Card](https://goreportcard.com/badge/github.com/msoap/shell2http)](https://goreportcard.com/report/github.com/msoap/shell2http)

Executing shell commands via simple http server (written in Go language).
Settings through 2 command line arguments, path and shell command.
By default bind to :8080.

Install
-------

MacOS:

    brew tap msoap/tools
    brew install shell2http
    # update:
    brew update; brew upgrade shell2http

Or download binaries from: [releases](https://github.com/msoap/shell2http/releases) (OS X/Linux/Windows/RaspberryPi)

Or build from source:

    # install Go (brew install go ...)
    # set $GOPATH if needed
    go get -u github.com/msoap/shell2http
    ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage
-----

    shell2http [options] /path "shell command" /path2 "shell command2" ...
    options:
        -host="host"    : host for http server, default - all interfaces
        -port=NNNN      : port for http server, default - 8080
        -form           : parse query into environment vars
        -cgi            : exec as CGI-script
                          set environment variables
                          write POST-data to STDIN (if not set -form)
                          parse headers from script (Location: XXX)
        -export-vars=var: export environment vars ("VAR1,VAR2,...")
                          by default export PATH, HOME, LANG, USER, TMPDIR
        -export-all-vars: export all current environment vars
        -no-index       : dont generate index page
        -add-exit       : add /exit command
        -log=filename   : log filename, default - STDOUT
        -shell="shell"  : shell for execute command, "" - without shell
        -cache=NNN      : caching command out for NNN seconds
        -one-thread     : run each shell command in one thread
        -version
        -help

Examples
--------

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http -export-all-vars /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http -export-all-vars /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
    shell2http -export-vars=GOPATH /get 'echo $GOPATH'

##### HTML calendar for current year
    shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'

##### get URL parameters (http://localhost:8080/form?from=10&to=100)
    shell2http -form /form 'echo $v_from, $v_to'

##### CGI scripts
    shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'
    # redirect
    shell2http -cgi /set 'touch file; echo "Location: /\n"'

##### simple http-proxy server (for logging all URLs)
    # setup proxy as "http://localhost:8080/"
    shell2http -log=/dev/null -cgi / 'echo $REQUEST_URI 1>&2; [ "$REQUEST_METHOD" == "POST" ] && post_param="-d@-"; curl -sL $post_param "$REQUEST_URI" -A "$HTTP_USER_AGENT"'

##### test slow connection (http://localhost:8080/slow?duration=10)
    shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'

##### proxy with cache in files (for debug with production API with rate limit)
    # get "http://localhost:8080/get?url=http://api.url/"
    shell2http -form \
        /form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
        /get 'MD5=$(printf "%s" $v_url | md5); cat cache_$MD5 || (curl -sL $v_url | tee cache_$MD5)'

##### remote sound volume control (Mac OS)
    shell2http /get  'osascript -e "output volume of (get volume settings)"' \
               /up   'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")+10))"' \
               /down 'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")-10))"'

##### remote control for Vox.app player (Mac OS)
    shell2http /play_pause 'osascript -e "tell application \"Vox\" to playpause" && echo ok' \
               /get_info 'osascript -e "tell application \"Vox\"" -e "\"Artist: \" & artist & \"\n\" & \"Album: \" & album & \"\n\" & \"Track: \" & track" -e "end tell"'

##### get four random OS X wallpapers
    shell2http /img 'cat "$(ls "/Library/Desktop Pictures/"*.jpg | ruby -e "puts STDIN.readlines.shuffle[0]")"' \
               /wallpapers 'echo "<html><h3>OS X Wallpapers</h3>"; seq 4 | xargs -I@ echo "<img src=/img?@ width=500>"'

[More examples](https://github.com/msoap/shell2http/wiki)

See also
--------

 * Emergency web server - [spark](https://github.com/rif/spark)
 * Share your terminal as a web application - [gotty](https://github.com/yudai/gotty)
 * Create Telegram bot from command-line - [shell2telegram](https://github.com/msoap/shell2telegram)
 * A http daemon for local development - [devd](https://github.com/cortesi/devd)
 * Turn any program that uses STDIN/STDOUT into a WebSocket server - [websocketd](https://github.com/joewalnes/websocketd)

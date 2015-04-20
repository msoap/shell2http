shell2http
==========

[![GoDoc](https://godoc.org/github.com/msoap/shell2http?status.svg)](https://godoc.org/github.com/msoap/shell2http)

Executing shell commands via simple http server (written in Go language).
Settings through 2 command line arguments, path and shell command.
By default bind to :8080.

Install
-------

Download binaries from: [releases](https://github.com/msoap/shell2http/releases) (OS X/Linux/RaspberryPi)

From source:

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
        -cgi            : set some CGI variables in environment
        -export-vars=var: export environment vars ("VAR1,VAR2,...")
                          by default export PATH, HOME, LANG, USER, TMPDIR
        -export-all-vars: export all current environment vars
        -no-index       : dont generate index page
        -add-exit       : add /exit command
        -log=filename   : log filename, default - STDOUT
        -version
        -help

Examples
--------

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
    shell2http -export-vars=GOPATH /get 'echo $GOPATH'

##### HTML calendar for current year
    shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'

##### get URL parameters (http://localhost:8080/form?from=10&to=100)
    shell2http -form /form 'echo $v_from, $v_to'

##### pseudo-CGI scripts
    shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'

##### test slow connection (http://localhost:8080/slow?duration=10)
    shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'

##### proxy with cache in files (for debug with production API with rate limit)
    shell2http -form \
        /form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
        /get 'MD5=$(printf "%s" $v_url | md5); cat cache_$MD5 || (curl -s $v_url | tee cache_$MD5)'

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

See also
--------

 * Emergency web server - [spark](https://github.com/rif/spark)

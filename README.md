shell2http
==========

Executing shell commands via simple http server (written in Go language).
Settings through 2 command line arguments, path and shell command.
By default bind to :8080.

Install
-------

Download binaries from: [releases](https://github.com/msoap/shell2http/releases) (OS X/Linux/Windows/RaspberryPi)

From source:

    # install Go (brew install go ...)
    # set $GOPATH if needed
    go get github.com/msoap/shell2http
    ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage
-----

    shell2http [options] /path "shell command" /path2 "shell command2" ...
    options:
        -host="host" : host for http server, default - all interfaces
        -port=NNNN   : port for http server, default - 8080
        -form        : parse query into environment vars
        -cgi         : set some CGI variables in environment
        -log=filename: log filename, default - STDOUT
        -help

Examples
--------

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'

##### HTML calendar for current year
    shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'

##### get URL parameters http://localhost:8080/form?from=10&to=100
    shell2http -form /form 'echo $v_from, $v_to'

##### pseudo-CGI scripts
    shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'

##### test slow connection
    shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'

##### remote sound volume control (Mac OS)
    shell2http /get  'osascript -e "output volume of (get volume settings)"' \
               /up   'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")+10))"' \
               /down 'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")-10))"'

##### remote control for Vox.app player (Mac OS)
    shell2http /play_pause 'osascript -e "tell application \"Vox\" to playpause" && echo ok' \
               /get_info 'osascript -e "tell application \"Vox\"" -e "\"Artist: \" & artist & \"\n\" & \"Album: \" & album & \"\n\" & \"Track: \" & track" -e "end tell"'

Update
------

    go get -u github.com/msoap/shell2http

See also
--------

 * Emergency web server - [spark](https://github.com/rif/spark)

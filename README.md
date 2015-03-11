shell2http
==========

Execute shell commands via simple http server (written in Go language).
Settings through 2 command line arguments, path and shell command.
Uses 8080 port.

Install:

    # install Go (brew install go ...)
    # set $GOPATH if needed
    go get github.com/msoap/shell2http
    ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage:

    shell2http /path "shell command" /path2 "shell command2" ...

Examples:

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
    shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'

Update:

    go get -u github.com/msoap/shell2http

shell2http
==========

Executing shell commands via simple http server (written in Go language).
Settings through 2 command line arguments, path and shell command.
By default bind to localhost:8080.

Install
-------

    # install Go (brew install go ...)
    # set $GOPATH if needed
    go get github.com/msoap/shell2http
    ln -s $GOPATH/bin/shell2http ~/bin/shell2http

Usage
-----

    shell2http [options] /path "shell command" /path2 "shell command2" ...
    options:
        -host="host" : host for http server
        -host=       : for bind to all hosts
        -port=NNNN   : port for http server
        -cgi         : set some CGI variables in enviroment
        -log=filename: log filename, default - STDOUT
        -help

Examples
--------

    shell2http /top "top -l 1 | head -10"
    shell2http /date date /ps "ps aux"
    shell2http /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
    shell2http /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
    shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
    shell2http -cgi /query 'echo $QUERY_STRING'

Update
------

    go get -u github.com/msoap/shell2http

See also
--------

 * Emergency web server - [spark](https://github.com/rif/spark)

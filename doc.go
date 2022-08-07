/*
Executing shell commands via simple http server.
Settings through 2 command line arguments, path and shell command.
By default bind to :8080.

Install/update:

	go get -u github.com/msoap/shell2http
	ln -s $GOPATH/bin/shell2http ~/bin/shell2http

MacOS install:

	brew tap msoap/tools
	brew install shell2http
	# update:
	brew upgrade shell2http

Usage:

	shell2http [options] /path "shell command" /path2 "shell command2" ...
	options:
		-host="host"      : host for http server, default - all interfaces
		-port=NNNN        : port for http server, 0 - to receive a random port, default - 8080
		-form             : parse query into environment vars, handle uploaded files
		-cgi              : run scripts in CGI-mode:
		                    - set environment variables with HTTP-request information
		                    - write POST|PUT|PATCH-data to script STDIN (if not set -form)
		                    - parse headers from script (eg: "Location: URL\n\n")
		-export-vars=var  : export environment vars ("VAR1,VAR2,...")
		-export-all-vars  : export all current environment vars
		-no-index         : don't generate index page
		-add-exit         : add /exit command
		-log=filename     : log filename, default - STDOUT
		-shell="shell"    : shell for execute command, "" - without shell
		-cache=N          : caching command out for N seconds
		-one-thread       : run each shell command in one thread
		-show-errors      : show the standard output even if the command exits with a non-zero exit code
		-include-stderr   : include stderr to output (default is stdout only)
		-cert=cert.pem    : SSL certificate path (if specified -cert/-key options - run https server)
		-key=key.pem      : SSL private key path
		-basic-auth=""	  : setup HTTP Basic Authentication ("user_name:password"), can be used several times
		-timeout=N        : set timeout for execute shell command (in seconds)
		-no-log-timestamp : log output without timestamps
		-version
		-help

In the "-form" mode, variables are available for shell scripts:

  - $v_NNN -- data from query parameter with name "NNN" (example: `http://localhost:8080/path?NNN=123`)
  - $filepath_ID -- uploaded file path, ID - id from `<input type=file name=ID>`, temporary uploaded file will be automatically deleted
  - $filename_ID -- uploaded file name from browser

To setup multiple auth users, you can specify the -basic-auth option multiple times.
The credentials for basic authentication may also be provided via the SH_BASIC_AUTH environment variable.
You can specify the preferred HTTP-method (via "METHOD:" prefix for path): shell2http GET:/date date

Examples:

	shell2http /top "top -l 1 | head -10"
	shell2http /date date /ps "ps aux"
	shell2http -export-all-vars /env 'printenv | sort' /env/path 'echo $PATH' /env/gopath 'echo $GOPATH'
	shell2http -export-all-vars /shell_vars_json 'perl -MJSON -E "say to_json(\%ENV)"'
	shell2http /cal_html 'echo "<html><body><h1>Calendar</h1>Date: <b>$(date)</b><br><pre>$(cal $(date +%Y))</pre></body></html>"'
	shell2http -form /form 'echo $v_from, $v_to'
	shell2http -cgi /user_agent 'echo $HTTP_USER_AGENT'
	shell2http -cgi /set 'touch file; echo "Location: /\n"'
	shell2http -cgi /404 'echo "Status: 404"; echo; echo "404 page"'
	shell2http -export-vars=GOPATH /get 'echo $GOPATH'

More complex examples:

Upload file:

	shell2http -form \
		/form 'echo "<html><body><form method=POST action=/file enctype=multipart/form-data><input type=file name=uplfile><input type=submit></form>"' \
		/file 'cat $filepath_uplfile > uploaded_file.dat; echo Ok'

Simple http-proxy server (for logging all URLs)

	# setup proxy as "http://localhost:8080/"
	shell2http \
		-log=/dev/null \
		-cgi \
		/ 'echo $REQUEST_URI 1>&2; [ "$REQUEST_METHOD" == "POST" ] && post_param="-d@-"; curl -sL $post_param "$REQUEST_URI" -A "$HTTP_USER_AGENT"'

Test slow connection

	# http://localhost:8080/slow?duration=10
	shell2http -form /slow 'sleep ${v_duration:-1}; echo "sleep ${v_duration:-1} seconds"'

Proxy with cache in files (for debug with production API with rate limit)

	# get "http://localhost:8080/get?url=http://api.url/"
	shell2http \
		-form \
		/form 'echo "<html><form action=/get>URL: <input name=url><input type=submit>"' \
		/get 'MD5=$(printf "%s" $v_url | md5); cat cache_$MD5 || (curl -sL $v_url | tee cache_$MD5)'

Remote sound volume control (Mac OS)

	shell2http \
		/get  'osascript -e "output volume of (get volume settings)"' \
		/up   'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")+10))"' \
		/down 'osascript -e "set volume output volume (($(osascript -e "output volume of (get volume settings)")-10))"'

Remote control for Vox.app player (Mac OS)

	shell2http \
		/play_pause 'osascript -e "tell application \"Vox\" to playpause" && echo ok' \
		/get_info 'osascript -e "tell application \"Vox\"" -e "\"Artist: \" & artist & \"\n\" & \"Album: \" & album & \"\n\" & \"Track: \" & track" -e "end tell"'

Get four random OS X wallpapers

	shell2http \
		/img 'cat "$(ls "/Library/Desktop Pictures/"*.jpg | ruby -e "puts STDIN.readlines.shuffle[0]")"' \
		/wallpapers 'echo "<html><h3>OS X Wallpapers</h3>"; seq 4 | xargs -I@ echo "<img src=/img?@ width=500>"'

More examples on https://github.com/msoap/shell2http/wiki
*/
package main

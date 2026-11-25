#!/bin/sh

# All live output modes of shell2http, open http://localhost:8080/ in a browser, or:
#   curl -N http://localhost:8080/stream
#   curl -N http://localhost:8080/sse
#   websocat ws://localhost:8080/ws
#
# from the repository: SHELL2HTTP="go run ../.." ./run.sh

cd "$(dirname "$0")" || exit 1

${SHELL2HTTP:-shell2http} \
    GET:/ 'cat index.html' \
    "/stream?stream=1" 'for i in 1 2 3 4 5; do echo "tick $i"; sleep 1; done' \
    "/sse?sse=1" 'for i in 1 2 3 4 5; do echo "tick $i"; sleep 1; done; exit 3' \
    "/ws?websocket=1" 'while read -r line; do echo "$line" | tr a-z A-Z; done'

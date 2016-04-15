run:
	go run shell2http.go -add-exit -cgi /date date /env 'printenv | sort'

build:
	go build shell2http.go

update-from-github:
	go get -u github.com/msoap/shell2http

test:
	go test -race -cover -v ./...

lint:
	golint ./...
	go vet ./...
	errcheck ./...

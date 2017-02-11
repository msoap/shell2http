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

gometalinter:
	gometalinter --vendor --cyclo-over=25 --line-length=150 --dupl-threshold=150 --min-occurrences=3 --enable=misspell --deadline=10m

build-docker-image:
	rocker build

generate-manpage:
	docker run -it --rm -v $$PWD:/app -w /app ruby-ronn sh -c 'cat README.md | grep -v "^\[" > shell2http.md; ronn shell2http.md; mv ./shell2http ./shell2http.1; rm ./shell2http.html ./shell2http.md'

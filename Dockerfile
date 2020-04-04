# docker build -t msoap/shell2http .

# build image
FROM golang:alpine as go_builder

RUN apk add --no-cache git

ADD . $GOPATH/src/github.com/msoap/shell2http
WORKDIR $GOPATH/src/github.com/msoap/shell2http
ENV CGO_ENABLED=0
RUN go install -a -v -ldflags="-w -s" ./...

# final image
FROM alpine

COPY --from=go_builder /go/bin/shell2http /app/shell2http
ENTRYPOINT ["/app/shell2http"]
CMD ["-help"]

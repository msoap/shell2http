# build image
FROM golang:alpine as go_builder

RUN apk add --no-cache git

ENV CGO_ENABLED=0
RUN go get -a -v -ldflags="-w -s" github.com/msoap/shell2http

# final image
FROM alpine

COPY --from=go_builder /go/bin/shell2http /app/shell2http
ENTRYPOINT ["/app/shell2http"]
CMD ["-help"]

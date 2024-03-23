# docker build -t msoap/shell2http .

# build image
FROM --platform=$BUILDPLATFORM golang:alpine as go_builder

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache git

ADD . $GOPATH/src/github.com/msoap/shell2http
WORKDIR $GOPATH/src/github.com/msoap/shell2http

ENV CGO_ENABLED=0
# GOARM=6 affects only "arm" builds
ENV GOARM=6
# "amd64", "arm64" or "arm" (--platform=linux/amd64,linux/arm64,linux/arm/v6)
ENV GOARCH=$TARGETARCH
ENV GOOS=linux

RUN echo "Building for $GOOS/$GOARCH"
RUN go build -v -trimpath -ldflags="-w -s -X 'main.version=$(git describe --abbrev=0 --tags | sed s/v//)'" -o /go/bin/shell2http .

# final image
FROM alpine

COPY --from=go_builder /go/bin/shell2http /app/shell2http
ENTRYPOINT ["/app/shell2http"]
CMD ["-help"]

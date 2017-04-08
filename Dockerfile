FROM alpine

ADD shell2http /app/shell2http
ENTRYPOINT ["/app/shell2http"]
CMD ["-help"]

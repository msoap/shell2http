FROM msoap/shell2http

# may be install some alpine packages:
# RUN apk add --no-cache ...

EXPOSE 8080
CMD ["/date", "date"]

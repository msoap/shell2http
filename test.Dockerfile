FROM msoap/shell2http

# may be install some alpine packages:
# RUN apk add --no-cache ...
CMD ["/date", "date"]

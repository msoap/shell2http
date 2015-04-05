build:
	go build shell2http.go

VERSION=$$(git tag | grep -E '^[0-9]+' | tail -1)
build-all-platform:
	@for GOOS in linux darwin windows; \
	do \
		for GOARCH in amd64 386; \
		do \
			echo build: $$GOOS/$$GOARCH; \
			GOOS=$$GOOS GOARCH=$$GOARCH go build; \
			if [ $$GOOS == windows ]; \
			then \
				zip -9 shell2http-$(VERSION).$$GOARCH.$$GOOS.zip shell2http.exe README.md LICENSE; \
				rm shell2http.exe; \
			else \
				zip -9 shell2http-$(VERSION).$$GOARCH.$$GOOS.zip shell2http README.md LICENSE; \
				rm shell2http; \
			fi \
		done \
	done
	GOOS=linux GOARCH=arm go build
	@zip -9 shell2http-$(VERSION).arm.linux.zip shell2http README.md LICENSE
	@rm shell2http

update-from-github:
	go get -u github.com/msoap/shell2http

sha1-binary:
	@ls shell2http*.{linux,darwin}.zip | xargs -I@ sh -c 'echo "@ $$(unzip -p @ shell2http | shasum)"'
	@ls shell2http*.windows.zip | xargs -I@ sh -c 'echo "@ $$(unzip -p @ shell2http.exe | shasum)"'

sha1-zip:
	shasum shell2http*.zip

clean:
	rm shell2http*.zip

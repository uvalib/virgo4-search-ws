GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test

build: darwin 

all: darwin linux

darwin:
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -a -o bin/v4search.darwin cmd/*.go
	cp -r i18n/ bin/i18n

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -a -installsuffix cgo -o bin/v4search.linux cmd/*.go
	cp -r i18n/ bin/i18n

clean:
	$(GOCLEAN) cmd/
	rm -rf bin

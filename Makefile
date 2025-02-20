GOCMD = go
GOBUILD = $(GOCMD) build
GOCLEAN = $(GOCMD) clean
GOTEST = $(GOCMD) test
GOFMT = $(GOCMD) fmt
GOVET = $(GOCMD) vet
GOGET = $(GOCMD) get
GOMOD = $(GOCMD) mod

build: darwin

all: darwin linux

darwin:
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -a -o bin/v4search.darwin cmd/*.go
	cp -r ttf/ bin/ttf

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -a -installsuffix cgo -o bin/v4search.linux cmd/*.go
	cp -r ttf/ bin/ttf

clean:
	$(GOCLEAN) cmd/
	rm -rf bin

fmt:
	cd cmd; $(GOFMT)

vet:
	cd cmd; $(GOVET)

dep:
	$(GOGET) -u ./cmd/...
	$(GOMOD) tidy
	$(GOMOD) verify

check:
	go install honnef.co/go/tools/cmd/staticcheck
	$(HOME)/go/bin/staticcheck -checks all,-S1002,-ST1003,-U1000 cmd/*.go
	go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow
	$(GOVET) -vettool=$(HOME)/go/bin/shadow ./cmd/...

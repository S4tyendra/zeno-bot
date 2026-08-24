BINARY := zeno
DIST := dist
SRC := $(shell find . -name '*.go' -type f)

.PHONY: all build buildtest run livetest clean

all: build

build:
	go mod tidy
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o $(DIST)/$(BINARY) .

buildtest: build
	./$(DIST)/$(BINARY)

run: livetest

livetest:
	reflex -r '\.go$$' -s -- sh -c "go mod tidy && go build -o $(DIST)/$(BINARY) . && ./$(DIST)/$(BINARY)"

clean:
	rm -rf $(DIST)/$(BINARY)

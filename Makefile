BINARY := zeno
DIST := dist
SRC := $(shell find . -name '*.go' -type f)

.PHONY: all build buildtest run livetest clean

all: build

build:
	go mod tidy
	go build -o $(DIST)/$(BINARY) .

buildtest: build
	./$(DIST)/$(BINARY)

run: livetest

livetest:
	reflex -r '\.go$$' -s -- sh -c "go mod tidy && go build -o $(DIST)/$(BINARY) . && ./$(DIST)/$(BINARY)"

clean:
	rm -rf $(DIST)/$(BINARY)

BINARY    := wasio
MODULE    := simonwaldherr.de/go/wasio
INST_DIR  := instruments
WASM_OUT  := $(patsubst $(INST_DIR)/%.go,$(INST_DIR)/%.wasm,$(wildcard $(INST_DIR)/*.go))

.PHONY: all build instruments run fmt test clean

## all: build everything (binary + instruments)
all: build instruments

## build: compile the WASIO server binary
build:
	go build -o $(BINARY) .

## instruments: compile all Go instruments to WASM via TinyGo
instruments: $(WASM_OUT)

$(INST_DIR)/%.wasm: $(INST_DIR)/%.go
	GOTOOLCHAIN=go1.23.3 tinygo build -o $@ -target wasi $<

## run: build and start the server
run: build
	./$(BINARY)

## fmt: format all Go source files (excluding trash directories)
fmt:
	gofmt -w -s $(shell find . -name '*.go' -not -path './-trash/*' -not -path './wasio/-trash/*')

## test: run unit tests
test:
	go test -v -count=1 .

## clean: remove the binary and compiled WASM files
clean:
	rm -f $(BINARY)
	rm -f $(INST_DIR)/*.wasm

## help: print this help message
help:
	@grep -E '^##' Makefile | sed 's/## //'

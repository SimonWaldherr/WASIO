BINARY    := wasio
MODULE    := simonwaldherr.de/go/wasio
INST_DIR  := instruments
WASM_OUT  := $(patsubst $(INST_DIR)/%.go,$(INST_DIR)/%.wasm,$(wildcard $(INST_DIR)/*.go))

# Rust instruments – each lives in instruments/<name>/ (Cargo workspace).
RUST_PROJECTS := $(wildcard $(INST_DIR)/*/Cargo.toml)
RUST_NAMES    := $(patsubst $(INST_DIR)/%/Cargo.toml,%,$(RUST_PROJECTS))
RUST_WASM     := $(patsubst %,$(INST_DIR)/%.wasm,$(RUST_NAMES))

.PHONY: all build instruments rust-instruments run fmt test clean help version

## all: build everything (binary + Go instruments)
all: build instruments

## all-with-rust: build everything including Rust instruments (requires cargo + wasm32-wasi target)
all-with-rust: build instruments rust-instruments

## build: compile the WASIO server/CLI binary
build:
	go build -ldflags "-X main.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo v0.1.0)" -o $(BINARY) .

## instruments: compile all Go instruments to WASM via TinyGo
instruments: $(WASM_OUT)

$(INST_DIR)/%.wasm: $(INST_DIR)/%.go
	GOTOOLCHAIN=go1.25.0 tinygo build -o $@ -target wasi $<

## rust-instruments: compile Rust instruments to WASM
## Requires: rustup target add wasm32-wasi
rust-instruments: $(RUST_WASM)

$(INST_DIR)/%.wasm: $(INST_DIR)/%/Cargo.toml $(INST_DIR)/%/src/main.rs
	cd $(INST_DIR)/$* && cargo build --target wasm32-wasip1 --release --quiet
	cp $(INST_DIR)/$*/target/wasm32-wasip1/release/$*.wasm $(INST_DIR)/$*.wasm

## run: build and start the server
run: build
	./$(BINARY) serve

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

## version: print version info
version:
	./$(BINARY) version

## help: print this help message
help:
	@grep -E '^##' Makefile | sed 's/## //'


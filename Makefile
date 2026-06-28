BINARY := md2
BIN_DIR := bin
PKG := ./...

.DEFAULT_GOAL := help

## help: list available targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

## build: compile the binary into bin/
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) .

## run: build and run (pass args via ARGS="...")
.PHONY: run
run:
	go run . $(ARGS)

## install: install the binary into GOBIN/GOPATH
.PHONY: install
install:
	go install .

## test: run all tests
.PHONY: test
test:
	go test $(PKG)

## cover: run tests with coverage summary
.PHONY: cover
cover:
	go test -cover $(PKG)

## fmt: format all Go source
.PHONY: fmt
fmt:
	go fmt $(PKG)

## vet: run go vet
.PHONY: vet
vet:
	go vet $(PKG)

## tidy: sync go.mod / go.sum
.PHONY: tidy
tidy:
	go mod tidy

## check: fmt, vet, and test
.PHONY: check
check: fmt vet test

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

.PHONY: all build test lint fmt clean install run release release-all \
       build-windows build-darwin build-linux check check-gitattributes

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Linting. The version is CI's version (golangci-lint-action, install-mode:
# goinstall, in .github/workflows/test.yml) named here rather than whatever
# binary happens to be on PATH: a golangci-lint compiled by a Go older than
# go.mod declares cannot read this module at all, and @latest would let a check
# added in someone else's linter decide whether this build is green. Bump the
# two together. `go run pkg@version` compiles it with the local toolchain, the
# same way CI does; repeats cost ~4s off the build cache, the first run on a
# fresh machine compiles the linter once.
GOLANGCI_LINT_VERSION=v1.64.8
GOLANGCI_LINT=$(GOCMD) run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Binary names
MAIN_BINARY=alayacore

# Build flags
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X github.com/alayacore/alayacore/internal/version.Version=$(VERSION)"
RELEASE_LDFLAGS=-ldflags "-s -w -X github.com/alayacore/alayacore/internal/version.Version=$(VERSION)"
BUILDTAGS=-tags netgo

all: test build

## build: Build main binary for the current OS (static)
build:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY) .

## build-windows: Build for Windows (amd64 + arm64)
build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-windows-arm64.exe .

## build-darwin: Build for macOS (amd64 + arm64)
build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-arm64 .

## build-linux: Build for Linux (amd64, arm64, arm, 386, riscv64, loong64, ppc64le, s390x)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-arm .
	CGO_ENABLED=0 GOOS=linux GOARCH=386 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-386 .
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-riscv64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=loong64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-loong64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-ppc64le .
	CGO_ENABLED=0 GOOS=linux GOARCH=s390x $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-s390x .

## release: Build optimized release binary for the current OS (stripped)
release:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY) .

## release-all: Build optimized release binaries for all platforms
release-all:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-windows-arm64.exe .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-arm .
	CGO_ENABLED=0 GOOS=linux GOARCH=386 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-386 .
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-riscv64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=loong64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-loong64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-ppc64le .
	CGO_ENABLED=0 GOOS=linux GOARCH=s390x $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-s390x .

## test: Run all tests
test:
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

## lint: Run golangci-lint (the version CI runs, built with the local toolchain)
lint:
	$(GOLANGCI_LINT) run ./...

## fmt: Format code
fmt:
	$(GOFMT) ./...

## vet: Run go vet
vet:
	$(GOCMD) vet ./...

## clean: Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(MAIN_BINARY) $(MAIN_BINARY).exe
	rm -f $(MAIN_BINARY)-windows-amd64.exe $(MAIN_BINARY)-windows-arm64.exe
	rm -f $(MAIN_BINARY)-darwin-amd64 $(MAIN_BINARY)-darwin-arm64
	rm -f $(MAIN_BINARY)-linux-amd64 $(MAIN_BINARY)-linux-arm64 $(MAIN_BINARY)-linux-arm $(MAIN_BINARY)-linux-386 $(MAIN_BINARY)-linux-riscv64 $(MAIN_BINARY)-linux-loong64 $(MAIN_BINARY)-linux-ppc64le $(MAIN_BINARY)-linux-s390x
	rm -f coverage.out coverage.html

## install: Install main binary to GOPATH/bin
install:
	CGO_ENABLED=0 $(GOCMD) install $(BUILDTAGS) $(LDFLAGS) .

## mod: Download and tidy modules
mod:
	$(GOMOD) download
	$(GOMOD) tidy

## run: Run the main binary
run:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) -o $(MAIN_BINARY) .
	./$(MAIN_BINARY)

## check-gitattributes: Assert .gitattributes still does what it says (misc/check-gitattributes.sh)
check-gitattributes:
	./misc/check-gitattributes.sh

## check: Run all checks (attributes, fmt, vet, lint, test)
check: check-gitattributes fmt vet lint test

## pre-commit: Run checks before committing
pre-commit: fmt vet test

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'

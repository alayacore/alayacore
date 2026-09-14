.PHONY: all build test lint tools fmt clean install run release release-all \
       build-windows build-darwin build-linux check check-gitattributes check-shell-style

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Linting. One file names the linter version — .golangci-lint-version, read here
# and by both CI jobs (golangci-lint-action, install-mode: goinstall) — so the pin
# cannot drift between the three. misc/run-golangci-lint.sh runs it, preferring an
# installed binary that satisfies both halves of the pin (the version, and a
# toolchain no older than go.mod declares: a golangci-lint built by an older Go
# cannot read this module at all) and falling back to `go run pkg@version`, which
# compiles the pinned tag with this module's toolchain the way CI does — the
# command for each path lives in that script. `make tools` installs the preferred
# binary.
GOLANGCI_LINT_VERSION=$(shell cat .golangci-lint-version)

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

## tools: Install the pinned golangci-lint, built with this module's toolchain (needs the network once)
tools:
	$(GOCMD) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

## lint: Run golangci-lint (the version CI runs, built with this module's toolchain)
lint:
	./misc/run-golangci-lint.sh

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

## check-shell-style: Assert shell scripts indent with tabs, like Go (misc/check-shell-style.sh)
check-shell-style:
	./misc/check-shell-style.sh

## check: Run all checks (attributes, shell style, fmt, vet, lint, test)
check: check-gitattributes check-shell-style fmt vet lint test

## pre-commit: Run checks before committing
pre-commit: fmt vet test

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'

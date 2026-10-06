.PHONY: build test clean install run fmt vet tidy

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GO_VERSION := $(shell go version | cut -d' ' -f3)

# Build flags
LDFLAGS := -s -w \
	-X 'github.com/rotaze/rotaze/internal/version.Version=$(VERSION)' \
	-X 'github.com/rotaze/rotaze/internal/version.GitCommit=$(GIT_COMMIT)' \
	-X 'github.com/rotaze/rotaze/internal/version.BuildDate=$(BUILD_DATE)' \
	-X 'github.com/rotaze/rotaze/internal/version.GoVersion=$(GO_VERSION)'

# The project has no cgo dependencies (glebarez/sqlite and embedded sing-box
# are pure Go; the Dockerfile already builds with CGO_ENABLED=0). Disable cgo
# by default: stdlib net's cgo resolver cannot compile against Android/bionic
# headers (getnameinfo hostlen is size_t, but GOOS=linux selects the glibc
# socklen_t variant). Targets that truly need cgo (-race) override it below.
export CGO_ENABLED := 0

# Default target
build:
	go build -ldflags "$(LDFLAGS)" -o build/rotaze ./cmd/rotaze

# Linux amd64 build (x86_64)
build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o build/rotaze-linux-amd64 ./cmd/rotaze

# Linux arm64 build (CGO cross-compile against a glibc toolchain)

build-linux-arm64:
	CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o build/rotaze-linux-arm64 ./cmd/rotaze

# Development build (with debug info)
build-debug:
	go build -o build/rotaze ./cmd/rotaze

# Run all tests
test:
	go test ./... -v -count=1

# Run tests with coverage
test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

# Run tests with race detector (race requires cgo)
test-race:
	CGO_ENABLED=1 go test -race -count=1

# Format code
fmt:
	gofmt -s -w .

# Run vet
vet:
	go vet ./...

# Tidy dependencies
tidy:
	go mod tidy

# Clean build artifacts
clean:
	rm -rf build/ coverage.out

# Run the binary
run: build
	./build/rotaze

# Cross-compile for multiple platforms
cross-compile:
	@mkdir -p build
	@echo "Building for linux/amd64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o build/rotaze-linux-amd64 ./cmd/rotaze
	@echo "Building for linux/arm64 (cgo)..."
	@CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o build/rotaze-linux-arm64 ./cmd/rotaze
	@echo "Building for linux/arm..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" -o build/rotaze-linux-armv7 ./cmd/rotaze
	@echo "Building for darwin/amd64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o build/rotaze-darwin-amd64 ./cmd/rotaze
	@echo "Building for darwin/arm64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o build/rotaze-darwin-arm64 ./cmd/rotaze
	@echo "Building for windows/amd64..."
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o build/rotaze-windows-amd64.exe ./cmd/rotaze
	@echo ""
	@echo "Cross-compilation complete:"
	@ls -lh build/rotaze-*

# Install to /usr/local/bin
install: build
	sudo cp build/rotaze /usr/local/bin/rotaze
	@echo "Installed to /usr/local/bin/rotaze"

# Install sing-box (auto-detects architecture)
SINGBOX_VERSION ?= v1.13.19
install-singbox:
	@arch=$$(uname -m); \
	case "$$arch" in \
		x86_64|amd64) sing=amd64 ;; \
		aarch64|arm64) sing=arm64 ;; \
		armv7l|armv6l) sing=armv7 ;; \
		*) echo "Unsupported architecture: $$arch"; exit 1 ;; \
	esac; \
	echo "Downloading sing-box $(SINGBOX_VERSION) for $$arch..."; \
	curl -sL https://github.com/SagerNet/sing-box/releases/download/$(SINGBOX_VERSION)/sing-box-$(SINGBOX_VERSION)-linux-$$sing.tar.gz | tar xz -C /tmp && \
	sudo cp /tmp/sing-box-*/sing-box /usr/local/bin/ && \
	rm -rf /tmp/sing-box-* && \
	echo "Installed sing-box to /usr/local/bin/sing-box"

# Show help
help:
	@echo "Rotaze Makefile targets:"
	@echo "  build          - Build the binary"
	@echo "  build-linux-arm64 - Build linux/arm64 (cgo cross-compile)"
	@echo "  build-debug    - Build without stripping symbols"
	@echo "  test           - Run all tests"
	@echo "  test-cover     - Run tests with coverage report"
	@echo "  test-race      - Run tests with race detector"
	@echo "  fmt            - Format Go code"
	@echo "  vet            - Run go vet"
	@echo "  tidy           - Tidy go.mod"
	@echo "  clean          - Remove build artifacts"
	@echo "  run            - Build and run"
	@echo "  cross-compile  - Build for all platforms"
	@echo "  install        - Install to /usr/local/bin"
	@echo "  install-singbox - Download sing-box for current arch"
	@echo ""
	@echo "Version: $(VERSION) (commit: $(GIT_COMMIT))"

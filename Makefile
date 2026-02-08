# Project information
PROJECT_NAME := ezlb
MODULE_NAME := github.com/easzlab/ezlb
BUILD_TIME := $(shell date +%Y-%m-%d\ %H:%M:%S)
BUILD_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
#BUILD_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")

# Build configuration
BUILD_DIR := build

# Linker flags for build information
LDFLAGS := -ldflags "-X 'main.BuildTime=$(BUILD_TIME)' \
                     -X 'main.BuildCommit=$(BUILD_COMMIT)' \
                     -s -w -extldflags -static"

# Default target
.PHONY: all
all: clean build

.PHONY: help
help: ## show help
	@echo "Available targets: "
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## build the binary
	@echo "Building $(PROJECT_NAME) ..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o build/ezlb cmd/ezlb/main.go
	@echo "✓ Build completed."

.PHONY: build-dev
build-dev: ## build the binary with debug info
	@echo "Building $(PROJECT_NAME) for development..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -race -o build/ezlb cmd/ezlb/main.go
	@echo "✓ Development build completed."

.PHONY: build-linux
build-linux: ## build the binary for Linux
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o build/ezlb-linux-amd64 cmd/ezlb/main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o build/ezlb-linux-arm64 cmd/ezlb/main.go
	@echo "✓ Linux build completed"

.PHONY: test
test: ## run unit tests (all platforms, using fake IPVS)
	@echo "Running unit tests..."
	@go test -v ./...
	@echo "✓ Tests completed"

# test-linux runs tests with real IPVS handle, serially (-p 1) because IPVS is a global kernel resource.
# Must be run as root on Linux.
.PHONY: test-linux
test-linux: ## run unit tests with real IPVS (Linux only)
	@echo "Running unit tests for linux..."
	@go test -count=1 -p 1 -tags integration ./...
	@echo "✓ Tests completed"

# e2e tests compile the ezlb binary and verify IPVS kernel rules end-to-end.
# Must be run as root on Linux.
.PHONY: test-e2e
test-e2e: ## run end-to-end tests for Linux
	@echo "Running e2e tests for linux..."
	@go test -count=1 -v -p 1 ./tests/e2e/
	@echo "✓ Tests completed"

.PHONY: clean
clean: ## clean build artifacts
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "✓ Clean completed"

.PHONY: update
update: ## update dependencies
	@echo "Updating dependencies..."
	@go get -u ./...
	@go mod tidy
	@echo "✓ Dependencies updated"
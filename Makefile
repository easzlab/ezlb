VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY  := ezlb
BUILD_DIR := build

LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test clean install

build:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/ezlb/

test:
	go test ./...

clean:
	rm -rf $(BUILD_DIR)/

install: build
	install -d /usr/local/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	install -d /etc/ezlb
	install -m 644 examples/ezlb.yaml /etc/ezlb/ezlb.yaml

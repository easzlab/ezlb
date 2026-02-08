VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY  := ezlb
BUILD_DIR := build

LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test clean install

build:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/ezlb/

test:
	go test ./...

# test-linux runs tests serially (-p 1) because IPVS is a global kernel resource.
# Must be run as root on Linux.
test-linux:
	go test -count=1 -p 1 ./...

clean:
	rm -rf $(BUILD_DIR)/

install: build
	install -d /usr/local/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	install -d /etc/ezlb
	install -m 644 examples/ezlb.yaml /etc/ezlb/ezlb.yaml

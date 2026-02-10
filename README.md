# ezlb

English | [中文](README_CN.md)

A lightweight Layer-4 TCP load balancer based on Linux IPVS, using declarative reconcile mode to dynamically manage IPVS services.

## Features

- **IPVS Kernel-Level Load Balancing**: High-performance Layer-4 forwarding powered by Linux IPVS
- **Declarative Reconcile**: Automatically compares desired state with actual IPVS rules and applies incremental changes
- **Multiple Scheduling Algorithms**: Round Robin (rr), Weighted Round Robin (wrr), Least Connection (lc), Weighted Least Connection (wlc), Destination Hashing (dh), Source Hashing (sh)
- **TCP & HTTP Health Checks**: Independent health check configuration per service, supporting TCP connection probes and HTTP GET probes with configurable path and expected status code
- **Hot Config Reload**: File changes automatically trigger reconciliation without restart

## Quick Start

### Build

```bash
make build
```

Cross-compile for Linux:

```bash
make build-linux
```

### Configuration

Create a config file `config.yaml`:

```yaml
global:
  log_level: info

services:
  - name: web-service
    listen: 10.0.0.1:80
    protocol: tcp
    scheduler: wrr
    health_check:
      enabled: true
      type: tcp              # optional: tcp (default), http
      interval: 5s
      timeout: 3s
      fail_count: 3
      rise_count: 2
    backends:
      - address: 192.168.1.10:8080
        weight: 5
      - address: 192.168.1.11:8080
        weight: 3

  - name: api-service
    listen: 10.0.0.1:443
    protocol: tcp
    scheduler: wlc
    health_check:
      enabled: true
      type: http             # HTTP health check
      interval: 10s
      timeout: 5s
      fail_count: 5
      rise_count: 3
      http_path: /healthz            # default: /
      http_expected_status: 200      # default: 200
    backends:
      - address: 192.168.2.10:8443
        weight: 1
      - address: 192.168.2.11:8443
        weight: 1
```

### Usage

```bash
# Daemon mode
sudo ezlb start -c config.yaml

# Single reconcile pass
sudo ezlb once -c config.yaml

# Show version
ezlb -v
```

## Testing

```bash
# Run unit tests (macOS/Linux)
make test

# Run all tests (Linux, requires root)
make test-linux

# Run e2e tests (Linux, requires root)
make test-e2e
```

## Project Structure

```
ezlb/
├── cmd/ezlb/            # Entry point, CLI commands
├── pkg/
│   ├── config/           # Config management (loading, validation, hot reload)
│   ├── lvs/              # IPVS management (operations, reconcile)
│   ├── healthcheck/      # Health checking (TCP & HTTP probes)
│   └── server/           # Server orchestration (lifecycle management)
├── tests/e2e/            # End-to-end tests
├── examples/             # Example configurations
└── Makefile
```

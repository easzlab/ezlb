# ezlb

English | [中文](README_CN.md)

A lightweight Layer-4 TCP/UDP load balancer based on Linux IPVS, using declarative reconcile mode to dynamically manage IPVS services.

## Features

- **IPVS Kernel-Level Load Balancing**: High-performance Layer-4 TCP/UDP forwarding powered by Linux IPVS
- **Declarative Reconcile**: Owns the complete IPVS table in a dedicated network namespace, with periodic resync every 30 seconds
- **Multiple Scheduling Algorithms**: Round Robin (rr), Weighted Round Robin (wrr), Least Connection (lc), Weighted Least Connection (wlc), Destination Hashing (dh), Source Hashing (sh)
- **TCP & HTTP Health Checks**: Independent health check configuration per service, supporting TCP connection probes and HTTP GET probes with configurable path and expected status code
- **FullNAT / SNAT Support**: Optional per-service FullNAT mode via IPVS NAT + iptables SNAT/MASQUERADE, with automatic nftables compatibility on iptables-nft backends
- **Hot Config Reload**: Service, backend, health-check and collection-interval changes are applied live; admin address, metrics endpoint settings and logging settings require restart
- **Prometheus Metrics**: Built-in metrics endpoint for monitoring traffic stats, health status, and reconcile errors

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

[Create a config file](examples/ezlb.yaml)

### Log Files

ezlb writes structured log files to the configured log directory (`global.log.home`, default `./logs`):

| File | Description |
|------|-------------|
| `ezlb.log` | System log (also printed to stdout) |
| `traffic.log` | Traffic statistics, emitted by debug-level entries when `global.log.level=debug` |

Log files are automatically rotated using [lumberjack](https://github.com/natefinch/lumberjack) based on `max_size`, `max_backups`, `max_age`, and `compress` settings.

### Prometheus Metrics

When `admin_address` is configured, ezlb exposes a Prometheus metrics endpoint:

```bash
# Access metrics
sudo ip netns exec ezlb curl http://127.0.0.1:9095/metrics

# Health check endpoint
sudo ip netns exec ezlb curl http://127.0.0.1:9095/health

# Readiness endpoint: 200 after a successful data-plane reconcile, 503 otherwise
sudo ip netns exec ezlb curl -i http://127.0.0.1:9095/ready
```

Available metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `ezlb_service_connections_total` | Counter | Total connections per service |
| `ezlb_service_bytes_in_total` | Counter | Total incoming bytes per service |
| `ezlb_service_bytes_out_total` | Counter | Total outgoing bytes per service |
| `ezlb_backend_connections_total` | Counter | Total connections per backend |
| `ezlb_backend_active_connections` | Gauge | Active connections per backend |
| `ezlb_backend_inactive_connections` | Gauge | Inactive connections per backend |
| `ezlb_backend_health_status` | Gauge | Health status per backend (1=healthy, 0=unhealthy) |
| `ezlb_config_reload_total` | Counter | Total config reloads |
| `ezlb_reconcile_errors_total` | Counter | Total reconcile errors |

### Usage

```bash
# Provision a dedicated ezlb network namespace with interfaces, VIPs, routes
# and a return path first. ezlb owns every IPVS service in that namespace.
# Never run it in the host's default network namespace.

# Daemon mode
sudo ip netns exec ezlb ezlb start --exclusive-netns -c config.yaml

# Single reconcile pass
sudo ip netns exec ezlb ezlb once --exclusive-netns -c config.yaml

# Show version
ezlb -v
```

Containers must likewise keep their own network namespace (see `docs/deploy/start-container.sh`). Docker bridge does not publish the VIP automatically; provide reachable routing separately. `--exclusive-netns` is an explicit operator assertion, not automatic namespace creation or isolation.

## Testing

```bash
# Run unit tests (macOS/Linux)
make test

# Run all tests (Linux, requires root)
sudo ip netns exec <disposable-test-namespace> env EZLB_TEST_EXCLUSIVE_NETNS=1 make test-linux

# Run e2e tests (Linux, requires root)
sudo ip netns exec <disposable-test-namespace> env EZLB_TEST_EXCLUSIVE_NETNS=1 make test-e2e

# Or use Docker's private container network namespace
make test-docker

# Run the Docker Compose data-plane test: two HTTP backends, FullNAT traffic,
# health-check failover/recovery, readiness and Prometheus metrics.
make test-compose
```

`make test-compose` creates a disposable bridge network (`172.30.0.0/24`).
The `ezlb` container is privileged only within its own network namespace so it
can load IPVS modules, create IPVS/iptables rules, and clean them up on exit.
The verifier is a separate container: it checks that the backends observe the
VIP as the source address, then takes one backend unhealthy and confirms both
traffic failover and recovery. It does not publish any host ports.

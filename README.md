# ezlb

English | [中文](README_CN.md)

A lightweight Layer-4 TCP/UDP load balancer based on Linux IPVS, using declarative reconcile mode to dynamically manage IPVS services.

## Features

- **IPVS Kernel-Level Load Balancing**: High-performance Layer-4 TCP/UDP forwarding powered by Linux IPVS
- **Declarative Reconcile**: Automatically compares desired state with actual IPVS rules and applies incremental changes
- **Multiple Scheduling Algorithms**: Round Robin (rr), Weighted Round Robin (wrr), Least Connection (lc), Weighted Least Connection (wlc), Destination Hashing (dh), Source Hashing (sh)
- **TCP & HTTP Health Checks**: Independent health check configuration per service, supporting TCP connection probes and HTTP GET probes with configurable path and expected status code
- **FullNAT / SNAT Support**: Optional per-service FullNAT mode via IPVS NAT + iptables SNAT/MASQUERADE, with automatic nftables compatibility on iptables-nft backends
- **Hot Config Reload**: File changes automatically trigger reconciliation without restart
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
curl http://127.0.0.1:9095/metrics

# Health check endpoint
curl http://127.0.0.1:9095/health
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

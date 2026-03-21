# ezlb

[English](README.md) | 中文

基于 Linux IPVS 的四层 TCP/UDP 负载均衡工具，采用声明式 Reconcile 模式动态管理 IPVS 服务。

## 特性

- **IPVS 内核级负载均衡**：基于 Linux IPVS 实现高性能四层 TCP/UDP 转发
- **声明式 Reconcile**：自动对比期望状态与实际 IPVS 规则，增量同步变更
- **多种调度算法**：支持轮询 (rr)、加权轮询 (wrr)、最少连接 (lc)、加权最少连接 (wlc)、目标地址哈希 (dh)、源地址哈希 (sh)
- **TCP & HTTP 健康检查**：每个服务独立配置检查参数，支持 TCP 连接探测和 HTTP GET 探测（可配置路径和期望状态码）
- **FullNAT / SNAT 支持**：按 service 粒度可选启用 FullNAT 模式（IPVS NAT + iptables SNAT/MASQUERADE），在 iptables-nft 后端系统上自动兼容 nftables
- **配置热加载**：修改配置文件自动触发 Reconcile，无需重启
- **Prometheus 监控指标**：内置指标端点，支持监控流量统计、健康状态和 Reconcile 错误

## 快速开始

### 编译

```bash
make build
```

交叉编译 Linux 版本：

```bash
make build-linux
```

### 配置

[创建配置文件](examples/ezlb.yaml)

### 日志文件

ezlb 将结构化日志写入配置的日志目录（`global.log.home`，默认 `./logs`）：

| 文件 | 说明 |
|------|------|
| `ezlb.log` | 系统日志（同时输出到 stdout） |
| `traffic.log` | 流量统计日志；仅当 `global.log.level=debug` 时写入 debug 级别统计 |

日志文件基于 [lumberjack](https://github.com/natefinch/lumberjack) 自动轮转，可通过 `max_size`、`max_backups`、`max_age`、`compress` 配置。

### Prometheus 监控指标

配置 `admin_address` 后，ezlb 会暴露 Prometheus 指标端点：

```bash
# 访问指标
curl http://127.0.0.1:9095/metrics

# 健康检查端点
curl http://127.0.0.1:9095/health
```

可用指标：

| 指标名 | 类型 | 说明 |
|--------|------|------|
| `ezlb_service_connections_total` | Counter | 每个服务的总连接数 |
| `ezlb_service_bytes_in_total` | Counter | 每个服务的入向字节数 |
| `ezlb_service_bytes_out_total` | Counter | 每个服务的出向字节数 |
| `ezlb_backend_connections_total` | Counter | 每个后端的总连接数 |
| `ezlb_backend_active_connections` | Gauge | 每个后端的活跃连接数 |
| `ezlb_backend_inactive_connections` | Gauge | 每个后端的非活跃连接数 |
| `ezlb_backend_health_status` | Gauge | 每个后端的健康状态（1=健康，0=不健康）|
| `ezlb_config_reload_total` | Counter | 配置重载总次数 |
| `ezlb_reconcile_errors_total` | Counter | Reconcile 错误总次数 |

### 运行

```bash
# 守护进程模式
sudo ezlb start -c config.yaml

# 单次 Reconcile
sudo ezlb once -c config.yaml

# 查看版本
ezlb -v
```

## 测试

```bash
# 运行单元测试（macOS/Linux 均可）
make test

# 运行全部测试（Linux，需要 root 权限）
make test-linux

# 运行 e2e 测试（Linux，需要 root 权限）
make test-e2e
```
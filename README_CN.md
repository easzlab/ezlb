# ezlb

[English](README.md) | 中文

基于 Linux IPVS 的四层 TCP/UDP 负载均衡工具，采用声明式 Reconcile 模式动态管理 IPVS 服务。

## 特性

[项目架构与测试层次](docs/ARCHITECTURE.md)

- **IPVS 内核级负载均衡**：基于 Linux IPVS 实现高性能四层 TCP/UDP 转发
- **声明式 Reconcile**：在 ezlb 独占的网络命名空间内管理整个 IPVS 表，启动和每 30 秒同步期望状态
- **多种调度算法**：支持轮询 (rr)、加权轮询 (wrr)、最少连接 (lc)、加权最少连接 (wlc)、目标地址哈希 (dh)、源地址哈希 (sh)
- **TCP & HTTP 健康检查**：每个服务独立配置检查参数，支持 TCP 连接探测和 HTTP GET 探测（可配置路径和期望状态码）
- **FullNAT / SNAT 支持**：按 service 粒度可选启用 FullNAT 模式（IPVS NAT + iptables SNAT/MASQUERADE），在 iptables-nft 后端系统上自动兼容 nftables
- **配置热加载**：服务、后端、健康检查、采集间隔等变更自动同步；管理地址、指标开关/路径与日志配置变更需重启
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
sudo ip netns exec ezlb curl http://127.0.0.1:9095/metrics

# 健康检查端点
sudo ip netns exec ezlb curl http://127.0.0.1:9095/health

# 就绪端点：最近一次数据面同步成功返回 200，否则返回 503
sudo ip netns exec ezlb curl -i http://127.0.0.1:9095/ready
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
# 先由部署系统创建专用 ezlb 网络命名空间，配置接口、VIP、路由与回程路径。
# ezlb 独占其中所有 IPVS 服务；不要在宿主机默认命名空间运行。

# 守护进程模式
sudo ip netns exec ezlb ezlb start --netns-mode=exclusive -c config.yaml

# 单次 Reconcile
sudo ip netns exec ezlb ezlb once --netns-mode=exclusive -c config.yaml

# 宿主网络集成（例如 kubeasz 的 kube-lb）。该模式会保留 kube-proxy
# 及其他控制器拥有的 IPVS 服务。
sudo ezlb start --netns-mode=shared -c config.yaml

# 查看版本
ezlb -v
```

容器部署同样必须使用容器自己的网络命名空间（示例见 `docs/deploy/start-container.sh`）；Docker bridge 默认不会自动对外发布 VIP，需另行规划可达路由。`--netns-mode=exclusive` 是操作员确认独占的显式开关，不会自动创建或隔离命名空间。

`--netns-mode=shared` 用于 ezlb 必须和已有 IPVS 控制器共用宿主网络命名空间的场景。它只会 reconcile 当前配置中的服务，并且只会删除当前进程曾管理的服务，不会清理其他控制器的 IPVS 规则。不得与其他控制器复用同一虚拟 IP、端口和协议。IPVS NAT 服务启用 `full_nat: true` 时，需确保 `net.ipv4.ip_forward=1` 和 `net.ipv4.vs.conntrack=1`。

Linux 上必须显式指定 `--netns-mode`，其值只能为 `exclusive` 或 `shared`。

## 测试

```bash
# 运行单元测试（macOS/Linux 均可）
make test

# 运行全部测试（Linux，需要 root 权限）
sudo ip netns exec <一次性测试命名空间> env EZLB_TEST_EXCLUSIVE_NETNS=1 make test-linux

# 运行 e2e 测试（Linux，需要 root 权限）
sudo ip netns exec <一次性测试命名空间> env EZLB_TEST_EXCLUSIVE_NETNS=1 make test-e2e

# 或让 Docker 使用容器自己的网络命名空间
make test-docker

# 运行 Docker Compose 数据面测试：两个 HTTP 后端、FullNAT 流量、
# 健康检查故障切换/恢复、就绪检查和 Prometheus 指标。
make test-compose
```

`make test-compose` 会创建一次性的 bridge 网络（`172.30.0.0/24`）。
`ezlb` 容器仅在自己的网络命名空间中使用特权，以加载 IPVS 模块、创建
IPVS/iptables 规则，并在退出时清理。验证器是独立容器：它会确认后端看到
VIP 作为源地址，然后将一个后端置为不健康并确认流量切换和恢复。测试不会
发布任何主机端口。

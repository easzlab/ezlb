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

创建配置文件 `config.yaml`：

```yaml
global:
  log:
    level: info              # 全局日志级别；traffic/nat 的 debug 日志仅在设为 debug 时写入
    home: ./logs             # 日志目录（默认: ./logs）
    max_size: 50             # 单个日志文件最大 MB（默认: 50）
    max_backups: 3           # 保留旧日志文件数量（默认: 3）
    traffic:
      enabled: true          # 启用流量日志（默认: true）
      interval: 15s          # 流量统计采集间隔，最小 5s（默认: 15s）
  cleanup_on_exit: true      # 退出时删除 ezlb 管理的 IPVS 服务和 EZLB-SNAT iptables 链（默认: true）

services:
  - name: web-service
    listen: 10.0.0.1:80
    protocol: tcp
    scheduler: wrr
    health_check:
      enabled: true
      type: tcp              # 可选: tcp（默认）、http
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
      type: http             # HTTP 健康检查
      interval: 10s
      timeout: 5s
      fail_count: 5
      rise_count: 3
      http_path: /healthz            # 默认: /
      http_expected_status: 200      # 默认: 200
    backends:
      - address: 192.168.2.10:8443
        weight: 1
      - address: 192.168.2.11:8443
        weight: 1

  - name: dns-service
    listen: 10.0.0.2:53
    protocol: udp            # UDP 负载均衡
    scheduler: rr
    full_nat: true           # 启用 FullNAT（IPVS NAT + iptables SNAT）
    snat_ip: 10.0.0.2        # SNAT 源地址；不配置则使用 MASQUERADE
    health_check:
      enabled: false
    backends:
      - address: 192.168.3.10:53
        weight: 1
      - address: 192.168.3.11:53
        weight: 1
```

### 日志文件

ezlb 将结构化日志写入配置的日志目录（`global.log.home`，默认 `./logs`）：

| 文件 | 说明 |
|------|------|
| `ezlb.log` | 系统日志（同时输出到 stdout） |
| `traffic.log` | 流量统计日志；仅当 `global.log.level=debug` 时写入 debug 级别统计 |
| `nat.log` | NAT/SNAT 统计与常规操作日志；主要在 `global.log.level=debug` 时写入 |

日志文件基于 [lumberjack](https://github.com/natefinch/lumberjack) 自动轮转，可通过 `max_size`、`max_backups`、`max_age`、`compress` 配置。

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

## 项目结构

```
ezlb/
├── cmd/ezlb/            # 程序入口，CLI 命令
├── pkg/
│   ├── config/           # 配置管理（加载、校验、热加载）
│   ├── lvs/              # IPVS 管理（操作封装、Reconcile）
│   ├── healthcheck/      # 健康检查（TCP & HTTP 探测）
│   ├── snat/             # SNAT/FullNAT 管理（iptables 规则）
│   └── server/           # 服务编排（生命周期管理）
├── tests/e2e/            # 端到端测试
├── examples/             # 示例配置
└── Makefile
```

# ezlb

基于 Linux IPVS 的四层 TCP 负载均衡工具，采用声明式 Reconcile 模式动态管理 IPVS 服务。

## 特性

- **IPVS 内核级负载均衡**：基于 Linux IPVS 实现高性能四层转发
- **声明式 Reconcile**：自动对比期望状态与实际 IPVS 规则，增量同步变更
- **多种调度算法**：支持轮询 (rr)、加权轮询 (wrr)、最少连接 (lc)
- **TCP 健康检查**：每个服务独立配置检查参数，支持禁用
- **配置热加载**：修改配置文件自动触发 Reconcile，无需重启
- **systemd 集成**：支持作为系统服务运行

## 快速开始

### 编译

```bash
make build
```

### 配置

编辑配置文件 `/etc/ezlb/ezlb.yaml`：

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
      interval: 5s
      timeout: 3s
      fail_count: 3
      rise_count: 2
    backends:
      - address: 192.168.1.10:8080
        weight: 5
      - address: 192.168.1.11:8080
        weight: 3
```

### 运行

```bash
# 守护进程模式
sudo ezlb -c /etc/ezlb/ezlb.yaml

# 单次 Reconcile
sudo ezlb once -c /etc/ezlb/ezlb.yaml

# 查看版本
ezlb version
```

### systemd 服务

```bash
sudo make install
sudo cp deploy/ezlb.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ezlb
```

## 项目结构

```
ezlb/
├── cmd/ezlb/           # 程序入口，CLI 命令
├── pkg/
│   ├── config/          # 配置管理（加载、校验、热加载）
│   ├── lvs/             # IPVS 管理（操作封装、Reconcile）
│   ├── healthcheck/     # 健康检查（TCP 探测）
│   └── server/          # 服务编排（生命周期管理）
├── examples/            # 示例配置
├── deploy/              # 部署文件
└── Makefile
```

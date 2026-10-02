## 1. 项目概述

### 1.1 项目目标

构建一套统一后台管理系统，在逻辑上划分为三个平面：

- **控制面 Control Plane**：统一管理用户、租户、节点、域名、证书、虚拟网络、ACL、子网路由、配置下发、监控与审计。
- **穿透代理面 Proxy Plane**：基于 NPS / frp / Traefik / Caddy 等能力，提供公网域名反向代理和内网穿透，让普通用户无需安装客户端即可访问内网 Web 服务。
- **Mesh 组网面 SD-WAN Plane**：基于 WireGuard / EasyTier / Tailscale / ZeroTier 等能力，构建设备间虚拟局域网，实现 P2P 直连、中继兜底、子网路由和大流量传输。

### 1.2 核心价值

- **对普通用户**：零客户端、域名直达，体验类似公网服务。
- **对技术用户**：高性能 P2P Mesh，大流量不绕路，支持子网互联。
- **对运营者**：统一控制面、多租户、易扩展、可商业化。

### 1.3 典型场景

1. 家庭 NAS、开发环境、内部管理系统通过域名对外发布。
2. 多台云服务器、边缘设备、办公电脑组成虚拟局域网。
3. 跨地域设备之间进行大文件同步、数据库复制、视频传输。
4. 将某个内网子网段暴露到虚拟网络中，供其他节点访问。
5. 外部用户访问 Web 服务，内部设备之间走 P2P 直连，互不干扰。

### 1.4 非目标

- MVP 阶段不实现完整商业化计费系统。
- MVP 阶段不追求全平台 Agent，优先 Linux + Docker。
- MVP 阶段不实现复杂 SD-WAN 流量工程和 QoS。
- MVP 阶段不替代企业级防火墙和零信任安全产品。

------

## 2. 总体架构





### 2.1 三个平面的职责

| 平面        | 职责                                      | 关键组件                                 |
| :---------- | :---------------------------------------- | :--------------------------------------- |
| 控制面      | 统一管理、配置下发、认证授权、监控审计    | API Gateway、Dashboard、数据库、消息队列 |
| 穿透代理面  | 公网入口、域名反向代理、内网穿透、SSL     | NPS Server、NPS Client、Traefik/Caddy    |
| Mesh 组网面 | 虚拟局域网、P2P 直连、中继、子网路由、ACL | Coordinator、Relay、Agent、WireGuard     |

------

## 3. 核心概念与术语

| 术语                     | 说明                                                   |
| :----------------------- | :----------------------------------------------------- |
| 租户 Tenant              | 多租户隔离单位，拥有独立用户、节点、网络、域名         |
| 用户 User                | 登录后台的自然人或系统账号                             |
| 节点 Node                | 运行 Agent 的设备，如服务器、NAS、PC、边缘盒子         |
| 设备 Device              | 物理或虚拟设备，通常与 Node 一一对应                   |
| 域名 Domain              | 对外访问使用的域名，如 `app.example.com`               |
| 代理规则 Proxy Rule      | 将域名/Host 映射到内网目标或虚拟 IP 的规则             |
| 虚拟网络 Virtual Network | 一组节点组成的 Mesh 网络                               |
| 网络成员 Network Member  | 加入虚拟网络的节点                                     |
| ACL 规则                 | 控制节点之间、子网之间的访问权限                       |
| 子网路由 Subnet Route    | 将某个内网网段暴露到虚拟网络                           |
| 中继 Relay / DERP        | P2P 打洞失败时的加密转发节点                           |
| 协调服务器 Coordinator   | 负责节点发现、密钥交换、配置下发                       |
| Agent                    | 运行在节点上的轻量程序，负责隧道、代理、心跳、配置拉取 |
| Exit Node                | 出口节点，其他节点可通过它访问外部网络                 |

------

## 4. 模块设计

### 4.1 控制面 Control Plane

#### 4.1.1 功能

- 多租户管理
- 用户认证与 RBAC
- 节点注册、审批、心跳、状态监控
- 域名、证书、代理规则管理
- 虚拟网络、成员、ACL、子网路由管理
- 配置版本化与下发
- 流量日志、操作日志、审计
- 监控告警

#### 4.1.2 子模块

| 子模块          | 说明                                 |
| :-------------- | :----------------------------------- |
| API Gateway     | 对外 REST/gRPC API，鉴权、限流、审计 |
| Auth Service    | 登录、JWT、OAuth2、RBAC              |
| Node Service    | 节点注册、心跳、在线状态、标签、分组 |
| Domain Service  | 域名、证书、代理规则                 |
| Network Service | 虚拟网络、成员、ACL、子网路由        |
| Config Service  | 配置生成、版本、下发、回滚           |
| Metrics Service | Prometheus 指标、日志、审计          |
| Relay Service   | 中继节点注册、健康检查、调度         |

### 4.2 穿透代理面 Proxy Plane

#### 4.2.1 功能

- 公网 HTTP/HTTPS 入口
- 基于域名/Host 的反向代理
- 内网穿透隧道
- SSL 证书自动签发与续期
- WebSocket / HTTP/2 / gRPC 支持
- 访问控制：IP 白名单、Basic Auth、JWT
- 与 SD-WAN 协同：代理目标可为虚拟 IP

#### 4.2.2 推荐实现

- MVP：基于 `nps` 或 `frp` 二次开发，增加动态 API 配置。
- V1：使用 `Traefik` 或 `Caddy` 作为反向代理，NPS 负责隧道。
- 与 Mesh 集成：NPS 入口将流量转发到目标虚拟 IP，由 Agent 接收。

### 4.3 Mesh 组网面 SD-WAN Plane

#### 4.3.1 功能

- 节点发现与注册
- WireGuard 密钥交换
- NAT 穿透与 P2P 直连
- 中继兜底
- 虚拟 IP 分配
- 子网路由
- ACL 访问控制
- Exit Node
- 网络状态监控

#### 4.3.2 推荐实现

| 方案      | 说明                                        |
| :-------- | :------------------------------------------ |
| EasyTier  | Rust 编写，轻量，去中心化，适合集成         |
| Headscale | Tailscale 控制面开源实现，生态成熟          |
| ZeroTier  | 自带 Controller，易于自建                   |
| 自研      | WireGuard + 协调服务 + 中继，灵活但工作量大 |

MVP 建议：优先集成 EasyTier 或 Headscale，控制面通过 API 管理其配置。

### 4.4 统一后台 Dashboard

#### 4.4.1 页面

- 登录页
- 仪表盘：在线节点、隧道状态、流量、告警
- 设备管理：列表、详情、审批、标签、分组
- 域名与代理：域名、证书、代理规则、访问控制
- 虚拟网络：网络列表、成员、ACL、子网路由、Exit Node
- 用户与权限：用户、角色、租户、API Token
- 日志与审计：操作日志、访问日志、流量日志
- 系统设置：中继节点、协调服务器、备份恢复

#### 4.4.2 技术

- React + TypeScript + Vite
- Ant Design Pro
- WebSocket 实时状态
- ECharts 图表

### 4.5 Agent

#### 4.5.1 职责

- 注册到控制面
- 定时心跳
- 拉取配置
- 启动/更新 WireGuard 接口
- NAT 穿透与中继连接
- 子网路由转发
- NPS 隧道客户端（可选）
- 本机指标上报

#### 4.5.2 支持平台

| 平台         | MVP  | V1   | V2   |
| :----------- | :--- | :--- | :--- |
| Linux x86_64 | ✅    | ✅    | ✅    |
| Linux ARM64  | ✅    | ✅    | ✅    |
| Docker       | ✅    | ✅    | ✅    |
| OpenWrt      | ❌    | ✅    | ✅    |
| Windows      | ❌    | ✅    | ✅    |
| macOS        | ❌    | ✅    | ✅    |

### 4.6 CLI

提供 `neilicoctl` 命令行工具：

bash

```
neilicoctl login
neilicoctl node list
neilicoctl node register --name nas-01
neilicoctl network create --name home
neilicoctl network join --network home
neilicoctl domain add --domain app.example.com --target 192.168.1.10:8080
neilicoctl status
```



------

## 5. 核心流程

### 5.1 普通用户通过域名访问内网 Web 服务





### 5.2 设备间 P2P 直连





### 5.3 子网路由

1. Agent B 所在内网为 `192.168.1.0/24`。

2. 在控制面为 B 添加子网路由 `192.168.1.0/24`。

3. 控制面将路由下发给网络内其他节点。

4. Agent A 收到路由后，添加系统路由：

   bash

   ```
   ip route add 192.168.1.0/24 dev wg0
   ```

   

5. A 可直接访问 `192.168.1.10`。

### 5.4 配置下发





------

## 6. 技术选型

| 层级       | 推荐技术                                    | 说明                 |
| :--------- | :------------------------------------------ | :------------------- |
| 控制面后端 | Go 1.22 + Gin/Echo + gRPC                   | 高性能、易交叉编译   |
| 控制面前端 | vue3 + TypeScript + Vite + Ant Design Pro   | 管理后台             |
| 数据库     | PostgreSQL 16                               | 主数据               |
| 缓存       | Redis 7                                     | 会话、状态、分布式锁 |
| 消息队列   | NATS JetStream                              | 配置下发、事件       |
| 日志分析   | ClickHouse                                  | 流量日志、访问日志   |
| 监控       | Prometheus + Grafana + Loki                 | 指标、日志、告警     |
| 代理面     | NPS / frp / Traefik / Caddy                 | 反向代理与隧道       |
| Mesh 面    | EasyTier / Headscale / ZeroTier / WireGuard | 虚拟组网             |
| 部署       | Docker Compose + Kubernetes + Helm          | 单机与集群           |
| 证书       | Let's Encrypt + ACME                        | 自动签发续期         |
| 认证       | JWT + OAuth2 + RBAC                         | 登录与权限           |
| Agent      | Go + WireGuard-go + gRPC                    | 轻量跨平台           |

------

## 7. 数据模型

### 7.1 核心表

#### tenants

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| name       | VARCHAR   | 租户名称 |
| plan       | VARCHAR   | 套餐     |
| created_at | TIMESTAMP | 创建时间 |

#### users

| 字段          | 类型      | 说明     |
| :------------ | :-------- | :------- |
| id            | UUID      | 主键     |
| tenant_id     | UUID      | 租户     |
| email         | VARCHAR   | 邮箱     |
| password_hash | VARCHAR   | 密码哈希 |
| role          | VARCHAR   | 角色     |
| status        | VARCHAR   | 状态     |
| created_at    | TIMESTAMP | 创建时间 |

#### nodes

| 字段       | 类型      | 说明           |
| :--------- | :-------- | :------------- |
| id         | UUID      | 主键           |
| tenant_id  | UUID      | 租户           |
| name       | VARCHAR   | 节点名称       |
| public_key | TEXT      | WireGuard 公钥 |
| virtual_ip | INET      | 虚拟 IP        |
| os         | VARCHAR   | 操作系统       |
| arch       | VARCHAR   | 架构           |
| version    | VARCHAR   | Agent 版本     |
| status     | VARCHAR   | 在线/离线      |
| last_seen  | TIMESTAMP | 最后心跳       |
| tags       | JSONB     | 标签           |
| created_at | TIMESTAMP | 创建时间       |

#### domains

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| tenant_id  | UUID      | 租户     |
| domain     | VARCHAR   | 域名     |
| cert_id    | UUID      | 证书     |
| status     | VARCHAR   | 状态     |
| created_at | TIMESTAMP | 创建时间 |

#### proxy_rules

| 字段           | 类型      | 说明                     |
| :------------- | :-------- | :----------------------- |
| id             | UUID      | 主键                     |
| tenant_id      | UUID      | 租户                     |
| domain_id      | UUID      | 域名                     |
| path           | VARCHAR   | 路径                     |
| target_type    | VARCHAR   | 内网 IP / 虚拟 IP / 节点 |
| target         | VARCHAR   | 目标地址                 |
| access_control | JSONB     | 访问控制                 |
| enabled        | BOOLEAN   | 是否启用                 |
| created_at     | TIMESTAMP | 创建时间                 |

#### certificates

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| domain     | VARCHAR   | 域名     |
| issuer     | VARCHAR   | 签发机构 |
| cert_pem   | TEXT      | 证书     |
| key_pem    | TEXT      | 私钥     |
| expires_at | TIMESTAMP | 过期时间 |

#### virtual_networks

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| tenant_id  | UUID      | 租户     |
| name       | VARCHAR   | 网络名称 |
| cidr       | CIDR      | 虚拟网段 |
| created_at | TIMESTAMP | 创建时间 |

#### network_members

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| network_id | UUID      | 网络     |
| node_id    | UUID      | 节点     |
| virtual_ip | INET      | 虚拟 IP  |
| role       | VARCHAR   | 成员角色 |
| joined_at  | TIMESTAMP | 加入时间 |

#### acl_rules

| 字段       | 类型    | 说明       |
| :--------- | :------ | :--------- |
| id         | UUID    | 主键       |
| network_id | UUID    | 网络       |
| src        | VARCHAR | 源         |
| dst        | VARCHAR | 目标       |
| action     | VARCHAR | allow/deny |
| protocol   | VARCHAR | 协议       |
| ports      | VARCHAR | 端口       |
| priority   | INT     | 优先级     |

#### subnet_routes

| 字段       | 类型    | 说明     |
| :--------- | :------ | :------- |
| id         | UUID    | 主键     |
| network_id | UUID    | 网络     |
| node_id    | UUID    | 出口节点 |
| cidr       | CIDR    | 子网     |
| enabled    | BOOLEAN | 是否启用 |

#### relay_servers

| 字段      | 类型      | 说明     |
| :-------- | :-------- | :------- |
| id        | UUID      | 主键     |
| name      | VARCHAR   | 名称     |
| endpoint  | VARCHAR   | 地址     |
| region    | VARCHAR   | 区域     |
| status    | VARCHAR   | 状态     |
| last_seen | TIMESTAMP | 最后心跳 |

#### config_versions

| 字段        | 类型      | 说明               |
| :---------- | :-------- | :----------------- |
| id          | UUID      | 主键               |
| tenant_id   | UUID      | 租户               |
| target_type | VARCHAR   | node/network/proxy |
| target_id   | UUID      | 目标 ID            |
| version     | INT       | 版本号             |
| config      | JSONB     | 配置内容           |
| created_at  | TIMESTAMP | 创建时间           |

#### audit_logs

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| tenant_id  | UUID      | 租户     |
| user_id    | UUID      | 用户     |
| action     | VARCHAR   | 操作     |
| resource   | VARCHAR   | 资源     |
| detail     | JSONB     | 详情     |
| ip         | INET      | 来源 IP  |
| created_at | TIMESTAMP | 创建时间 |

#### traffic_logs

| 字段       | 类型      | 说明     |
| :--------- | :-------- | :------- |
| id         | UUID      | 主键     |
| tenant_id  | UUID      | 租户     |
| node_id    | UUID      | 节点     |
| direction  | VARCHAR   | in/out   |
| bytes      | BIGINT    | 字节数   |
| protocol   | VARCHAR   | 协议     |
| peer       | VARCHAR   | 对端     |
| created_at | TIMESTAMP | 创建时间 |

------

## 8. API 设计

### 8.1 认证

http

```
POST /api/v1/auth/login
Content-Type: application/json

{
  "email": "admin@example.com",
  "password": "******"
}
```



响应：

json

```
{
  "token": "jwt-token",
  "user": {
    "id": "uuid",
    "email": "admin@example.com",
    "role": "admin"
  }
}
```



### 8.2 节点

http

```
GET /api/v1/nodes
POST /api/v1/nodes/register
POST /api/v1/nodes/{id}/heartbeat
GET /api/v1/nodes/{id}
DELETE /api/v1/nodes/{id}
```



节点注册请求：

json

```
{
  "name": "nas-01",
  "public_key": "wireguard-public-key",
  "os": "linux",
  "arch": "amd64",
  "version": "0.1.0",
  "tags": ["home", "nas"]
}
```



### 8.3 域名与代理

http

```
GET /api/v1/domains
POST /api/v1/domains
GET /api/v1/proxy-rules
POST /api/v1/proxy-rules
PUT /api/v1/proxy-rules/{id}
DELETE /api/v1/proxy-rules/{id}
```



创建代理规则：

json

```
{
  "domain": "app.example.com",
  "path": "/",
  "target_type": "virtual_ip",
  "target": "100.64.0.10:8080",
  "access_control": {
    "ip_whitelist": [],
    "basic_auth": false
  },
  "enabled": true
}
```



### 8.4 虚拟网络

http

```
GET /api/v1/networks
POST /api/v1/networks
GET /api/v1/networks/{id}
POST /api/v1/networks/{id}/members
DELETE /api/v1/networks/{id}/members/{node_id}
GET /api/v1/networks/{id}/acl
POST /api/v1/networks/{id}/acl
GET /api/v1/networks/{id}/routes
POST /api/v1/networks/{id}/routes
```



创建网络：

json

```
{
  "name": "home",
  "cidr": "100.64.0.0/24"
}
```



添加成员：

json

```
{
  "node_id": "uuid",
  "virtual_ip": "100.64.0.10"
}
```



添加子网路由：

json

```
{
  "node_id": "uuid",
  "cidr": "192.168.1.0/24",
  "enabled": true
}
```



### 8.5 配置拉取

http

```
GET /api/v1/agent/config?node_id={node_id}&version={current_version}
```



响应：

json

```
{
  "version": 12,
  "node": {
    "id": "uuid",
    "virtual_ip": "100.64.0.10"
  },
  "network": {
    "id": "uuid",
    "cidr": "100.64.0.0/24",
    "peers": [
      {
        "public_key": "peer-public-key",
        "endpoint": "1.2.3.4:51820",
        "allowed_ips": ["100.64.0.11/32", "192.168.1.0/24"]
      }
    ]
  },
  "proxy_rules": [],
  "acl": []
}
```



### 8.6 监控

http

```
GET /api/v1/metrics
GET /api/v1/nodes/{id}/metrics
GET /api/v1/traffic
GET /api/v1/audit-logs
```



------

## 9. Agent 设计

### 9.1 目录结构

text

```
neilico-agent/
├── cmd/
│   └── agent/
│       └── main.go
├── internal/
│   ├── config/
│   ├── heartbeat/
│   ├── mesh/
│   ├── proxy/
│   ├── route/
│   ├── metrics/
│   └── api/
├── pkg/
│   └── wireguard/
├── configs/
│   └── agent.yaml
├── Dockerfile
└── Makefile
```



### 9.2 配置文件

yaml

```
server: https://api.neilico.example.com
token: agent-token
node:
  name: nas-01
  tags: ["home", "nas"]
mesh:
  interface: wg0
  mtu: 1420
  listen_port: 51820
proxy:
  enabled: true
  nps_server: nps.example.com:8024
metrics:
  enabled: true
  listen: 0.0.0.0:9100
log:
  level: info
```



### 9.3 Agent 启动流程

1. 读取配置。
2. 调用 `/api/v1/nodes/register` 注册。
3. 获取虚拟 IP 和网络配置。
4. 创建 WireGuard 接口。
5. 启动心跳协程。
6. 启动配置拉取协程。
7. 启动指标上报。
8. 如果启用代理，启动 NPS 隧道客户端。
9. 监听系统信号，优雅退出。

### 9.4 子网路由

Agent 收到子网路由配置后：

bash

```
sysctl -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 100.64.0.0/24 -o eth0 -j MASQUERADE
ip route add 192.168.1.0/24 dev wg0
```



------

## 10. 安全设计

### 10.1 认证与授权

- 后台登录：JWT + Refresh Token。
- API 访问：JWT 或 API Token。
- 节点认证：节点注册 Token + WireGuard 密钥。
- RBAC：租户管理员、运维、只读、普通用户。

### 10.2 网络隔离

- 每个租户独立虚拟网络。
- WireGuard 密钥隔离。
- ACL 默认拒绝，按需放行。
- 子网路由需显式授权。

### 10.3 传输安全

- 公网入口强制 HTTPS。
- Agent 与控制面使用 mTLS 或 TLS。
- WireGuard 原生加密。
- 中继流量端到端加密。

### 10.4 审计

- 所有管理操作写入 `audit_logs`。
- 记录登录、配置变更、节点上下线、ACL 变更。
- 支持导出和告警。

------

## 11. 部署架构

### 11.1 单机 Docker Compose

适合 MVP 验证：

yaml

```
version: "3.9"
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: neilico
      POSTGRES_USER: neilico
      POSTGRES_PASSWORD: neilico
    volumes:
      - pgdata:/var/lib/postgresql/data

  redis:
    image: redis:7

  nats:
    image: nats:latest
    command: ["-js"]

  control-api:
    build: ./control-api
    ports:
      - "8080:8080"
    depends_on:
      - postgres
      - redis
      - nats

  dashboard:
    build: ./dashboard
    ports:
      - "3000:80"

  nps:
    image: nps:latest
    ports:
      - "80:80"
      - "443:443"
      - "8024:8024"

  relay:
    build: ./relay
    ports:
      - "3478:3478/udp"
      - "51820:51820/udp"

volumes:
  pgdata:
```



### 11.2 Kubernetes

- 控制面微服务部署为 Deployment。
- PostgreSQL 使用 Operator 或云数据库。
- Redis 使用 Sentinel/Cluster。
- NATS 使用 StatefulSet。
- NPS 入口使用 DaemonSet + HostNetwork 或 LoadBalancer。
- Relay 节点多地域部署。
- 使用 Helm Chart 管理。

------

## 12. 开发路线图

### 12.1 MVP

目标：验证核心链路。

- □ 

  控制面基础：租户、用户、登录、节点注册、心跳。

- □ 

  NPS 代理：HTTP/HTTPS 域名反向代理，手动配置。

- □ 

  SD-WAN：集成 EasyTier 或 Headscale，节点加入同一网络。

- □ 

  统一后台：设备、域名、网络基础管理。

- □ 

  Agent：Linux + Docker，注册、心跳、配置拉取。

- □ 

  验证：域名访问内网 Web；两台设备虚拟 IP 互 ping。

### 12.2 V1

- □ 

  多租户隔离

- □ 

  子网路由

- □ 

  ACL 精细控制

- □ 

  中继节点调度

- □ 

  Let's Encrypt 自动证书

- □ 

  监控与审计

- □ 

  API Token

- □ 

  Windows / macOS Agent

- □ 

  CLI 工具

### 12.3 V2

- □ 

  商业化计费

- □ 

  Exit Node

- □ 

  DNS 解析

- □ 

  插件系统

- □ 

  高可用控制面

- □ 

  多地域中继

- □ 

  流量工程与 QoS

- □ 

  OpenWrt / 路由器集成

------

## 13. 任务拆解与 AI 提示词

### 任务 A1：控制面 API 骨架

**目标**：实现 Go + Gin + PostgreSQL 的 API 骨架。

**输出**：

- 目录结构
- 配置加载
- 数据库连接
- 健康检查
- JWT 中间件
- 用户登录接口

**给 AI 的提示词**：

text

```
你是一名资深 Go 后端工程师。请使用 Go 1.22 + Gin + GORM + PostgreSQL 实现 NEILICO 控制面 API 骨架。
要求：
1. 支持配置文件 config.yaml。
2. 实现 /healthz、/api/v1/auth/login。
3. 使用 JWT 鉴权中间件。
4. 包含 users 表迁移。
5. 输出完整目录结构、代码和运行步骤。
```



### 任务 A2：节点注册与心跳

**目标**：实现节点注册、心跳、在线状态。

**给 AI 的提示词**：

text

```
请基于已有 NEILICO 控制面，实现节点模块：
1. POST /api/v1/nodes/register
2. POST /api/v1/nodes/{id}/heartbeat
3. GET /api/v1/nodes
4. 节点表 nodes 字段参考规格。
5. 心跳超时 60 秒标记离线。
6. 输出代码和测试。
```



### 任务 A3：域名与代理规则

**目标**：实现域名、证书、代理规则 CRUD。

**给 AI 的提示词**：

text

```
请实现 NEILICO 域名与代理规则模块：
1. domains、proxy_rules、certificates 表。
2. REST API CRUD。
3. 支持 target_type: internal_ip / virtual_ip / node。
4. 生成 NPS 可用的配置 JSON。
5. 输出代码和示例请求。
```



### 任务 B1：NPS 集成

**目标**：控制面动态生成 NPS 配置并 reload。

**给 AI 的提示词**：

text

```
请设计并实现 NEILICO 与 NPS 的集成：
1. 控制面保存代理规则。
2. 生成 NPS 客户端/服务端配置。
3. 通过 API 或文件 reload。
4. 支持 HTTPS 证书。
5. 输出集成方案、代码和测试。
```



### 任务 C1：Mesh 网络管理

**目标**：实现虚拟网络、成员、ACL、子网路由。

**给 AI 的提示词**：

text

```
请实现 NEILICO Mesh 网络管理模块：
1. virtual_networks、network_members、acl_rules、subnet_routes 表。
2. REST API。
3. 生成 Agent 可用的网络配置 JSON。
4. 支持 EasyTier 或 Headscale 配置导出。
5. 输出代码和测试。
```



### 任务 C2：Agent 注册与配置拉取

**目标**：实现 Linux Agent。

**给 AI 的提示词**：

text

```
请使用 Go 实现 NEILICO Agent：
1. 读取 agent.yaml。
2. 注册到控制面。
3. 每 30 秒心跳。
4. 拉取配置并应用 WireGuard。
5. 支持子网路由。
6. 输出代码、Dockerfile、运行步骤。
```



### 任务 D1：Dashboard

**目标**：实现管理后台基础页面。

**给 AI 的提示词**：

text

```
请使用 React + TypeScript + Ant Design Pro + Vite 实现 NEILICO Dashboard：
1. 登录页。
2. 节点列表。
3. 域名与代理规则。
4. 虚拟网络列表。
5. 调用后端 REST API。
6. 输出完整前端代码。
```



### 任务 E1：监控与日志

**目标**：Prometheus 指标、Grafana 面板、审计日志。

**给 AI 的提示词**：

text

```
请为 NEILICO 添加监控与审计：
1. 控制面暴露 /metrics。
2. Agent 暴露 /metrics。
3. 记录 audit_logs。
4. 提供 Grafana Dashboard JSON。
5. 输出代码和配置。
```



------

## 14. 测试计划

### 14.1 单元测试

- 控制面 API
- 配置生成
- ACL 匹配
- 子网路由计算
- Agent 配置解析

### 14.2 集成测试

- 用户登录 → 创建网络 → 注册节点 → 下发配置。
- 添加域名 → 访问 Web → 验证响应。
- 两台 Agent → 虚拟 IP 互 ping。
- 子网路由 → 访问内网设备。

### 14.3 性能测试

- 1000 节点心跳。
- 10000 代理规则配置生成。
- P2P 打洞成功率统计。
- 中继吞吐测试。

### 14.4 安全测试

- 未授权访问 API。
- ACL 绕过测试。
- 证书过期处理。
- JWT 伪造测试。

------

## 15. 监控与运维

### 15.1 指标

| 指标                         | 说明       |
| :--------------------------- | :--------- |
| neilico_nodes_online            | 在线节点数 |
| neilico_tunnel_up               | 隧道状态   |
| neilico_p2p_success_rate        | P2P 成功率 |
| neilico_relay_bytes             | 中继流量   |
| neilico_proxy_requests          | 代理请求数 |
| neilico_config_version          | 配置版本   |
| neilico_agent_heartbeat_latency | 心跳延迟   |

### 15.2 告警

- 节点离线超过 5 分钟。
- 证书 30 天内过期。
- P2P 成功率低于 60%。
- 中继流量异常增长。
- 配置下发失败。

### 15.3 日志

- 控制面日志：JSON 格式，输出到 stdout。
- Agent 日志：本地文件 + stdout。
- 访问日志：ClickHouse。
- 审计日志：PostgreSQL。

------

## 16. 风险与对策

| 风险         | 影响       | 对策                                 |
| :----------- | :--------- | :----------------------------------- |
| NAT 穿透失败 | P2P 不可用 | 多协议打洞、多地域中继、智能调度     |
| 配置不一致   | 网络异常   | 版本化配置、定期校验、回滚           |
| 安全隔离不足 | 租户越权   | 独立网络、ACL、密钥隔离              |
| 性能瓶颈     | 延迟高     | 水平扩展、P2P 优先、中继分布式       |
| 开源许可     | 法律风险   | 审查 NPS、EasyTier、Headscale 许可证 |
| 合规风险     | 运营风险   | 日志审计、实名、备案、内容安全       |
| Agent 兼容性 | 部署困难   | 优先 Docker，提供静态二进制          |
| 证书管理复杂 | HTTPS 失败 | ACME 自动签发、监控过期              |

------

## 17. 开源集成建议

| 项目       | 用途                  | 集成方式                   |
| :--------- | :-------------------- | :------------------------- |
| NPS        | 内网穿透、反向代理    | 二次开发或 API 动态配置    |
| frp        | 内网穿透              | 备选                       |
| Traefik    | 反向代理              | 动态配置                   |
| Caddy      | 反向代理 + 自动 HTTPS | 动态配置                   |
| EasyTier   | Mesh 组网             | 控制面生成配置，Agent 调用 |
| Headscale  | Tailscale 控制面      | API 集成                   |
| ZeroTier   | SD-WAN                | Controller API             |
| WireGuard  | 加密隧道              | 底层协议                   |
| Prometheus | 监控                  | 指标暴露                   |
| Grafana    | 可视化                | Dashboard                  |
| NATS       | 消息队列              | 配置下发                   |

------

## 18. 附录

### 18.1 推荐目录结构

text

```
neilico/
├── control-plane/
│   ├── cmd/
│   ├── internal/
│   ├── migrations/
│   └── configs/
├── dashboard/
│   ├── src/
│   └── package.json
├── agent/
│   ├── cmd/
│   ├── internal/
│   └── configs/
├── proxy/
│   ├── nps/
│   └── traefik/
├── mesh/
│   ├── coordinator/
│   └── relay/
├── deploy/
│   ├── docker-compose/
│   └── helm/
├── docs/
│   └── NEILICO_SPEC.md
└── README.md
```



### 18.2 术语表

| 缩写   | 全称                                         | 说明               |
| :----- | :------------------------------------------- | :----------------- |
| NEILICO   | Unified Mesh & Proxy Platform                | 本项目             |
| SD-WAN | Software-Defined Wide Area Network           | 软件定义广域网     |
| Mesh   | Mesh Network                                 | 网状网络           |
| P2P    | Peer-to-Peer                                 | 点对点             |
| DERP   | Designated Encrypted Relay for Packets       | Tailscale 中继     |
| ACL    | Access Control List                          | 访问控制列表       |
| RBAC   | Role-Based Access Control                    | 基于角色的访问控制 |
| ACME   | Automatic Certificate Management Environment | 证书自动管理       |

### 18.3 参考项目

- NPS: https://github.com/ehang-io/nps
- frp: https://github.com/fatedier/frp
- Tailscale: [https://tailscale.com](https://tailscale.com/)
- Headscale: https://github.com/juanfont/headscale
- ZeroTier: [https://www.zerotier.com](https://www.zerotier.com/)
- EasyTier: https://github.com/EasyTier/EasyTier
- WireGuard: [https://www.wireguard.com](https://www.wireguard.com/)
- Traefik: [https://traefik.io](https://traefik.io/)
- Caddy: [https://caddyserver.com](https://caddyserver.com/)

------

## 19. 最终交付物

- □ 

  `NEILICO_SPEC.md` 开发规格说明书

- □ 

  控制面 API 服务

- □ 

  Dashboard 管理后台

- □ 

  Linux/Docker Agent

- □ 

  NPS 代理集成

- □ 

  Mesh 组网集成

- □ 

  Docker Compose 部署文件

- □ 

  Helm Chart

- □ 

  测试用例

- □ 

  用户文档

- □ 

  运维文档

------

> 本文档可作为 AI 开发的总纲。建议按“MVP → V1 → V2”逐步实现，每完成一个模块就进行集成验证，避免一次性生成过多不可运行代码。
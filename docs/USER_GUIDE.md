# UMPP 用户指南

本指南覆盖：部署到自有服务器、安装 Agent、建立虚拟网络、配置域名、验证内网 Web 访问。

## 1. 部署到服务器

要求：Docker Engine 24+、Docker Compose v2、Linux x86_64/arm64。开发机上已占用的宿主端口可按 `.env` 修改。

```bash
cd /home/neil/Documents/Projects/umpp/deploy/docker-compose
cp .env.example .env
# 使用密码管理器或 secrets manager 生成并替换所有 replace-* 占位符：
openssl rand -hex 32
docker compose up -d --build
curl -fsS http://127.0.0.1:18080/healthz
```

默认本机端口（由于开发机已有服务，示例采用偏移端口）：

| 服务 | 宿主端口 | 内部端口 |
| :--- | :---: | :---: |
| Control API | 18080 | 8080 |
| Dashboard | 13000 | 8080（nginx） |
| 内置 HTTP 反代 | 18081 | 8081 |
| 内置 TLS/SNI 反代 | 18443（ACME overlay） | 8443 |
| HTTP-01 挑战 | 18082（ACME overlay 测试） | 5002（生产示例为 80） |
| PostgreSQL | 15432 | 5432 |
| Redis | 16379 | 6379 |
| NATS JetStream | 14222 | 4222 |
| Relay WireGuard | 51820/udp | 51820/udp |
| Relay TURN 预留 | 3478/udp | 3478/udp |

浏览器打开 `http://127.0.0.1:13000`，用 `BOOTSTRAP_ADMIN_EMAIL` 和 `BOOTSTRAP_ADMIN_PASSWORD` 登录。生产环境可直接启用下述 ACME + 内置 TLS，或在外部反向代理后启用 HTTPS。

## 2. 开启 ACME 自动证书

先在测试/预发验证，**不要直接对 Let's Encrypt 生产目录下单**。首次启用建议显式使用 staging：

```yaml
acme:
  enabled: true
  directory_url: "https://acme-staging-v02.api.letsencrypt.org/directory"
  email: "ops@example.com"
  challenge: "http-01"
  http_port: 80
  renew_before_days: 30
  check_interval: "6h"
  key_type: "ec256"
  agree_tos: true
  auto_renew: true
  ca_cert_file: ""
```

等价环境变量包括 `UMPP_ACME_ENABLED`、`UMPP_ACME_DIRECTORY_URL`、`UMPP_ACME_EMAIL`、`UMPP_ACME_AGREE_TOS`、`UMPP_ACME_HTTP_PORT`、`UMPP_ACME_CA_CERT_FILE`、`UMPP_ACME_RENEW_BEFORE_DAYS`、`UMPP_ACME_CHECK_INTERVAL`、`UMPP_ACME_AUTO_RENEW`。`enabled=false` 或 `agree_tos=false` 时接口返回 `acme_disabled` / `acme_tos_not_accepted`，并且不会连接 CA。

HTTP-01 前置条件：

1. 域名 A/AAAA 必须解析到 UMPP 的公网地址。
2. 公网 TCP 80 必须转发到 `acme.http_port`；该 listener 独立于反代监听器。若同时启用 TLS，公网 443 转发到 `proxy.tls.listen`。
3. HTTP-01 路径 `/.well-known/acme-challenge/{token}` 优先于反代路由，不依赖 Host。
4. `directory_url` 只允许 staging/测试 CA，直到 staging 签发、TLS 握手和续期都验证通过，再由运维显式切换生产目录。

启用内置 TLS：

```yaml
proxy:
  tls:
    enabled: true
    listen: ":8443"
    min_version: "1.2"
```

SNI 只返回同域名的 active 未过期证书，不会回退到其他域名。ACME 签发通过 API 异步执行：

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/certificates \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"issuer":"acme","domain":"nas.example.com"}'
```

返回 `202 {id,status:"pending"}`，轮询 `GET /api/v1/certificates/{id}` 直到 `active`。`POST /api/v1/certificates/{id}/renew` 可手动续期；同一证书已有 order 时返回 `409 order_in_flight`。

## 3. 安装 Agent

Agent 需要在 Linux 上运行。真实 WireGuard 应用需要 root、`ip`、`wg` 和 `CAP_NET_ADMIN`；没有权限时使用 `--dry-run` 验证配置不会触碰宿主网络。

```bash
cd agent
CGO_ENABLED=0 go build -o /usr/local/bin/umpp-agent ./cmd/agent
sudo install -d -m 0750 /etc/umpp-agent /var/lib/umpp-agent
sudo install -m 0640 configs/agent.example.yaml /etc/umpp-agent/agent.yaml
sudoedit /etc/umpp-agent/agent.yaml
```

`agent.yaml` 至少设置：

```yaml
server: "https://umpp.example.com"
token: "<tenant-admin-access-token 仅用于首次注册>"
node:
  name: "nas-01"
  tags: ["home"]
mesh:
  interface: "wg0"
  listen_port: 51820
  allow_forwarding: true
  external_interface: "eth0"
metrics:
  enabled: true
  listen: "127.0.0.1:9100"
state_path: "/var/lib/umpp-agent/state.json"
```

首次运行：

```bash
sudo /usr/local/bin/umpp-agent --config /etc/umpp-agent/agent.yaml
# 无 root 验证：
umpp-agent --config /etc/umpp-agent/agent.yaml --dry-run
```

注册成功后 `state.json` 保存 node_id、agent_token、WireGuard 私钥，权限应为 0600。后续心跳使用 agent token，不应把 admin token 留在生产配置中。

## 4. 建立虚拟网络并加入节点

1. 在 Dashboard「虚拟网络」创建 CIDR，例如 `100.64.250.0/24`。
2. 在「设备管理」找到两个节点，加入同一网络。系统自动分配虚拟 IP。
3. 在节点详情查看 Agent 配置版本和最近心跳。两个节点状态均为 `online` 后，A 的 peer 配置会包含 B 的 `/32` 虚拟 IP。
4. 如果 B 后面有内网子网，在「子网路由」添加 `192.168.1.0/24`，下一跳选 B。重新拉取 A 配置时应看到 `AllowedIPs` 包含该 CIDR，且 `version` 递增。

## 5. 域名访问内网 Web

1. 在「域名」添加 `nas.example.com`。无 TLS 时状态可为 active；绑定证书后自动变为 active。
2. 在「代理规则」添加 `/ -> node:<node-id>:8080`。`target_type=node` 会解析节点在虚拟网络中的虚拟 IP。
3. 将域名 DNS A/AAAA 记录指向 UMPP 公网入口，并在入口放行 80/443。
4. 验证：

```bash
curl -v -H 'Host: nas.example.com' http://127.0.0.1:18081/
```

应返回内网 Web 内容。HTTPS 可使用 ACME 自动签发，或在「证书」手工导入匹配 PEM 后绑定域名；客户端需信任完整证书链。TLS 请求会向目标设置 `X-Forwarded-Proto: https`，ACL（IP 白名单、Basic Auth、require_jwt）与 HTTP 模式保持一致。

## 6. 常见问题

### NAT 不通、节点一直 offline

- 确认 Agent 进程存活、`state.json` 可读，`server` 能访问 Control API。
- 查看 `umpp-agent` 日志中的注册/心跳错误；Agent 默认每 30 秒心跳，超过 60 秒由控制面标离线。
- UDP 51820 是否被运营商/防火墙拦截；先用 `--dry-run` 排除权限问题，再检查 `wg show`、`ip link`。
- 多层 NAT 时配置正确的 `public_endpoint: host:port`，检查中继服务是否可达。

### 心跳离线排查

```bash
curl -fsS http://127.0.0.1:18080/healthz
journalctl -u umpp-agent -n 100
sudo wg show
```

检查控制面 `/metrics` 的 `umpp_nodes_online`、Agent `:9100/metrics` 的心跳延迟，并确认系统时间同步。

### 代理 502

- 域名必须是 `active` 且规则 `enabled=true`。
- `target_type=node` 要求节点已加入网络并有虚拟 IP；确认配置版本已递增。
- 从控制面容器到目标 `host:port` 必须可达，目标服务要监听该端口。
- 查看 `docker compose logs control-api` 中 `builtin proxy request failed`，通常表示虚拟 IP、端口或防火墙错误。

### ACME 签发失败

- `acme_disabled`：设置 `UMPP_ACME_ENABLED=true` 后重启 control-api。
- `acme_tos_not_accepted`：由运维确认 CA 条款后显式设置 `UMPP_ACME_AGREE_TOS=true`。
- 挑战 404/超时：确认公网 80 转发到 `acme.http_port`、DNS 解析正确，且防火墙/CDN 没有拦截 `/.well-known/acme-challenge/`。
- CA TLS 错误：Pebble/私有 CA 需要设置 `UMPP_ACME_CA_CERT_FILE`；文件应是 CA PEM。
- `last_error` 有 badNonce/网络错误时，自动续期会写 `next_attempt_at` 并退避，当前 active 证书继续服务。手工 `/renew` 可在修复后立即重试。

### 证书导入/过期

- PEM 证书和私钥必须匹配；私钥只在导入时使用，响应不会回显。
- ACME 证书在 `renew_before_days` 内自动续期；失败不会把现有 active 证书改成失效。
- 证书到期前 30 天应告警；过期后 HTTPS 客户端会拒绝连接，但 HTTP 反代仍可测试。
- 重新导入证书后确认域名状态为 active，再访问域名。

### 配置没有更新

`GET /api/v1/agent/config?version=N` 只有在 N 等于最新版本时才返回 304。落后版本会返回最新配置；如果 Agent 持续 304，确认 state 中 `applied_version` 已写入并查看 Agent 日志。

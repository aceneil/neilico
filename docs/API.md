# NEILICO Control API

本文档由 `control-plane/internal/api` 的真实路由表整理。Base URL 在 Compose 部署中为 `http://127.0.0.1:18080`（生产可替换为 HTTPS 域名）。

## 认证

| 场景 | 方式 |
| :--- | :--- |
| 管理 API | `Authorization: Bearer <JWT>`，登录 `/api/v1/auth/login` 获取 |
| 自动化 API | `Authorization: Bearer <API Token>`，明文格式 `neilico_<32-byte base64url>`；只在创建/轮换响应中出现一次 |
| 刷新 | `Authorization: Bearer <refresh token>` 或请求体 `{ "refresh_token": "..." }` |
| Agent 心跳、流量、端点上报 | `Authorization: Bearer <agent_token>` |
| Agent config | agent token（返回私钥），或 tenant/platform admin JWT（不返回私钥） |
| `/healthz`, `/metrics`, `/install.sh`, `/install.ps1`, `/downloads/*` | 无需认证；公开安装/下载路径限流豁免 |

登录请求不会输出密码；生产环境必须使用 TLS 和独立的密钥管理。

## 通用响应

成功响应通常为 JSON 对象或列表：

```json
{"items": [], "total": 0, "page": 1, "page_size": 20}
```

错误响应统一为：

```json
{"error":{"code":"invalid_request","message":"human readable message"}}
```

scope 不足时还会返回 `detail`；API Token 使用 `insufficient_scope`，JWT 为保持 M1–M4 兼容仍使用 `forbidden`，但 `detail.reason` 固定为 `insufficient_scope`。任何错误、日志和审计都只允许显示 `token_prefix + "…"`，不会回显完整凭据。

## 路由表

| Method | Path | 认证/权限 | 说明 |
| :--- | :--- | :--- | :--- |
| GET | `/healthz` | public | 进程与数据库健康 |
| GET | `/metrics` | public | Prometheus 指标 |
| POST | `/api/v1/auth/login` | public | `{email,password}` -> access/refresh token |
| POST | `/api/v1/auth/refresh` | refresh token | 刷新 token；改密后旧 refresh token（token_version 不匹配）失效 |
| GET | `/api/v1/setup/status` | public | 初始化状态：`{initialized,registration_open,password_policy}` |
| POST | `/api/v1/setup/register` | public（仅无账号时） | 创建第一个平台管理员；已有账号返回 `409 already_initialized` |
| GET | `/api/v1/account` | JWT 会话 | 当前账号信息 + 密码强度策略 |
| PUT | `/api/v1/account/email` | JWT 会话 | `{current_password,email}` 改登录邮箱；占用返回 409 |
| POST | `/api/v1/account/password/rotate` | JWT 会话 | `{current_password,new_password}` 轮换密码；旧 refresh token 失效，返回新会话 |
| GET/POST | `/api/v1/tenants` | platform_admin | 租户列表/创建 |
| GET/PUT/DELETE | `/api/v1/tenants/{id}` | platform_admin | 租户详情/更新/删除 |
| GET/POST | `/api/v1/users` | JWT；创建需 admin | 用户列表/创建 |
| GET/PUT/DELETE | `/api/v1/users/{id}` | JWT，按 tenant scope | 用户详情/更新/删除 |
| POST | `/api/v1/enroll-tokens` | `nodes:write` | 创建一次性自注册令牌，返回 token 与 Linux/macOS/Windows/Docker 命令 |
| GET | `/api/v1/enroll-tokens` | `nodes:read` | 接入令牌列表与 active/used/expired/revoked 状态 |
| DELETE | `/api/v1/enroll-tokens/{id}` | `nodes:write` | 幂等撤销接入令牌 |
| POST | `/api/v1/nodes/enroll` | public + 接入令牌 + IP 限流 | 自注册；同一 jti/请求幂等 |
| POST | `/api/v1/nodes/register` | platform_admin/tenant_admin/ops | 兼容的管理 token 注册，返回一次性 `agent_token` |
| GET | `/api/v1/nodes` | JWT | 节点列表；返回 `capabilities`，可按 `status`、`tag`、分页 |
| POST | `/api/v1/nodes/{id}/heartbeat` | agent_token | 心跳；可更新 `capabilities`，返回下一次间隔 |
| GET/DELETE | `/api/v1/nodes/{id}` | JWT，按 tenant scope | 详情含 `capabilities`/`virtual_ip`/`network_id`；删除 |
| POST | `/api/v1/nodes/{id}/network-report` | agent_token | 公网端点上报 |
| POST | `/api/v1/nodes/{id}/traffic` | agent_token | 流量增量上报 |
| GET/POST | `/api/v1/domains` | JWT；写需 proxy 管理权限 | 域名列表/创建 |
| GET/PUT/DELETE | `/api/v1/domains/{id}` | JWT，按 tenant scope | 域名详情/更新/删除 |
| GET/POST | `/api/v1/certificates` | JWT；GET 可只读，POST 需 platform/tenant admin | 证书列表、手工 PEM 导入或异步 ACME 签发 |
| GET/DELETE | `/api/v1/certificates/{id}` | JWT，按 tenant scope；DELETE 需 admin | 证书详情/删除（仍被 domain 引用时 409） |
| POST | `/api/v1/certificates/{id}/renew` | platform/tenant admin，按 tenant scope | 手动触发续期；返回 202，single-flight |
| POST | `/api/v1/certificates/{id}/revoke` | platform/tenant admin，按 tenant scope | 撤销路由；V1-R1 实现明确返回 501 |
| GET/POST | `/api/v1/proxy-rules` | JWT；写需 proxy 管理权限 | 反代规则列表/创建 |
| GET/PUT/DELETE | `/api/v1/proxy-rules/{id}` | JWT，按 tenant scope | 反代规则详情/更新/删除 |
| GET | `/api/v1/proxy/providers` | JWT | ProxyProvider 状态 |
| POST | `/api/v1/proxy/render` | tenant_admin | 渲染 NPS/内置反代配置 |
| GET | `/api/v1/traffic` | JWT | 流量列表，支持 `node_id`、分页 |
| GET/POST | `/api/v1/networks` | JWT；写需 network 管理权限 | 虚拟网络列表/创建 |
| GET/PUT/DELETE | `/api/v1/networks/{id}` | JWT，按 tenant scope | 虚拟网络详情/更新/删除 |
| GET/POST | `/api/v1/networks/{id}/members` | JWT；写需 network 管理权限 | 成员列表/加入 |
| DELETE | `/api/v1/networks/{id}/members/{node_id}` | JWT，需 network 管理权限 | 成员移除 |
| GET/POST | `/api/v1/networks/{id}/acl` | JWT；写需 network 管理权限 | ACL 列表/创建 |
| DELETE | `/api/v1/networks/{id}/acl/{rule_id}` | JWT，需 network 管理权限 | ACL 删除 |
| GET/POST | `/api/v1/networks/{id}/routes` | JWT；写需 network 管理权限 | 子网路由列表/创建 |
| PUT/DELETE | `/api/v1/networks/{id}/routes/{route_id}` | JWT，需 network 管理权限 | 子网路由更新/删除 |
| GET | `/api/v1/networks/{id}/mesh/export` | JWT | WireGuard/EasyTier 配置导出 |
| POST | `/api/v1/nodes/{id}/keys/rotate` | JWT，需 node 管理权限 | WireGuard key 轮换 |
| GET | `/api/v1/agent/config?node_id=&version=` | agent/admin JWT | 版本化下发；相等 version 返回 304 |
| GET | `/api/v1/configs?target_type=&target_id=` | tenant_admin | 配置版本列表 |
| POST | `/api/v1/configs/{target_type}/{target_id}/rollback` | tenant_admin | 生成新版本回滚 |
| GET | `/api/v1/alerts` | JWT；readonly 可读 | 告警列表，支持 state/severity/rule/target_type/target_id/page/page_size；platform_admin 可传 tenant_id |
| GET | `/api/v1/alerts/rules` | JWT | 生效规则、阈值、数据源状态，只读 |
| POST | `/api/v1/alerts/evaluate` | platform_admin/tenant_admin/ops | 手工执行一轮评估；返回 firing/resolved 变迁和 `insufficient_data` |
| GET | `/api/v1/alerts/summary` | JWT | firing 按严重级、24h resolved、按规则计数 |
| GET | `/api/v1/alerts/{id}` | JWT，按 tenant scope | 告警详情和 `alert_events` 时间线 |
| GET | `/api/v1/audit-logs` | JWT | 审计列表，支持 action/resource/from/to/分页 |
| GET | `/api/v1/nodes/{id}/metrics` | JWT | 节点指标 |
| GET/POST | `/api/v1/relay-servers` | JWT；写需 admin | 中继服务器元数据 |
| PUT/DELETE | `/api/v1/relay-servers/{id}` | tenant/platform admin | 中继服务器元数据 |
| GET | `/api/v1/networks/{id}/status` | JWT / `networks:read` | 网络成员/隧道摘要 |
| GET | `/api/v1/api-tokens` | JWT 或 API Token；`tokens:read` | Token 列表，只返回 8 字符前缀、名称、scopes、过期/最近使用/撤销时间 |
| POST | `/api/v1/api-tokens` | JWT 或 API Token；`tokens:write` | 创建 Token；201 响应一次性返回 `token` |
| DELETE | `/api/v1/api-tokens/{id}` | JWT 或 API Token；`tokens:write` | 幂等撤销，已撤销仍返回 200 + `already_revoked:true` |
| POST | `/api/v1/api-tokens/{id}/rotate` | JWT 或 API Token；`tokens:write` | 旧 Token 立即撤销，新 Token 一次性返回 |

## API Token 与 Scope

Token 明文为 `neilico_` 加 32 字节 `base64url` 随机值。服务端只保存 SHA-256 十六进制哈希；`token_prefix` 是完整明文的前 8 字符。`user_id` 为空表示租户级服务账号；所有 Token 都以 `tenant_id` 为隔离根。

| Scope | GET 能力 | 写能力 |
| :--- | :--- | :--- |
| `nodes:read` / `nodes:write` | 节点、流量、节点指标 | 注册、删除、密钥轮换 |
| `networks:read` / `networks:write` | 网络/成员/ACL/路由/状态/配置版本 | 网络、成员、ACL、路由、配置回滚 |
| `proxy:read` / `proxy:write` | 域名、代理规则、provider/render | 域名和代理规则变更 |
| `certs:read` / `certs:write` | 证书读取 | 导入、续期、删除（V1 revoke 仍 501） |
| `tokens:read` / `tokens:write` | API Token 列表 | 创建、撤销、轮换 |
| `alerts:read` / `alerts:write` | 告警、规则、汇总、时间线 | 手工评估 |
| `admin` | 全部 | 全部；同时开放 users/tenants/audit/relay 等 V1 管理接口 |

JWT 角色映射保持既有 RBAC 语义：

| 角色 | 等价 scopes |
| :--- | :--- |
| `platform_admin` | `admin` |
| `tenant_admin` | 除 `admin` 外全部 read/write |
| `ops` | `nodes/networks/proxy/certs/alerts` read/write，`tokens:read`，无 `tokens:write`；证书写仍受既有 admin-only 限制 |
| `readonly` | 全部 `:read` |

API Token 严格按数据库 `scopes` 判定，不继承创建者角色。用 API Token 创建新 Token 时，新 scopes 必须是旧 scopes 的子集；尝试创建 `admin` 或其他新增权限返回 403 `insufficient_scope`。

### 创建与使用

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/api-tokens \
  -H "Authorization: Bearer $ADMIN_JWT" -H 'Content-Type: application/json' \
  -d '{"name":"ci-read","scopes":["nodes:read"],"expires_in_days":90}'
# 201 的 token 字段只显示一次；保存到 secrets manager，不要写入 shell history/日志。

curl -sS http://127.0.0.1:18080/api/v1/nodes \
  -H "Authorization: Bearer $API_TOKEN"

curl -sS -X DELETE http://127.0.0.1:18080/api/v1/api-tokens/<token-id> \
  -H "Authorization: Bearer $ADMIN_JWT"

curl -sS -X POST http://127.0.0.1:18080/api/v1/api-tokens/<token-id>/rotate \
  -H "Authorization: Bearer $ADMIN_JWT"
```

列表响应只含 `id/name/token_prefix/scopes/expires_at/last_used_at/revoked_at`，永不包含 `token_hash` 或明文。`last_used_at`/`last_used_ip` 采用异步节流更新，同 Token 默认最多每 60 秒写一次库。

### 简易限流

按 API Token ID（JWT 按 user ID）维护内存令牌桶，单实例语义：

```yaml
ratelimit:
  enabled: true
  rps: 20
  burst: 40
```

也可用 `NEILICO_RATELIMIT_ENABLED`、`NEILICO_RATELIMIT_RPS`、`NEILICO_RATELIMIT_BURST`。超限返回 `429 rate_limited` 和 `Retry-After`。`/healthz`、`/metrics`、`/.well-known/acme-challenge/*` 不计数；Agent 心跳/流量上报走独立 agent-token 路径，也不进入此 API Token/JWT 桶。

## curl 示例

### 登录

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"<password>"}'
# {"token":"<access>","refresh_token":"<refresh>","user":{"id":"...","email":"...","role":"platform_admin","tenant_id":"..."}}
```

### 创建租户、管理员和网络

```bash
TOKEN="<access>"
curl -sS -X POST http://127.0.0.1:18080/api/v1/tenants \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"acme","plan":"pro"}'

curl -sS -X POST http://127.0.0.1:18080/api/v1/users \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"<tenant-id>","email":"ops@acme.example","password":"<password>","role":"tenant_admin","status":"active"}'

curl -sS -X POST http://127.0.0.1:18080/api/v1/networks \
  -H "Authorization: Bearer <tenant-access>" -H 'Content-Type: application/json' \
  -d '{"name":"home","cidr":"100.64.250.0/24"}'
```

### 接入令牌、注册、心跳、配置

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/enroll-tokens \
  -H "Authorization: Bearer <tenant-access>" -H 'Content-Type: application/json' \
  -d '{"name_hint":"nas","network_id":"<network-id>","expires_in_seconds":86400,"max_uses":1}'
# token 与 commands.{linux,macos,windows,docker} 只返回一次；库里只存完整令牌的 SHA-256。
# commands.windows: powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm <server>/install.ps1))) -Token <TOKEN>"
# commands.macos 与 commands.linux 可同串，但必须分别返回且包含 sudo。

curl -sS -X POST http://127.0.0.1:18080/api/v1/nodes/enroll \
  -H 'Content-Type: application/json' \
  -d '{"token":"neilico-enroll.<payload>.<signature>","name":"nas-01","os":"linux","arch":"amd64","version":"dev","capabilities":{"mesh":"ready","subnet_routes":"ready","tunnel":"unavailable","reason":"未检测到可用的隧道/代理客户端"}}'
# 响应含 node_id/agent_token/private_key/server；同一 jti+请求重放只返回同一 node，不重发凭据。

curl -fsSL http://127.0.0.1:18080/install.sh | sudo bash -s -- --token "$ENROLL_TOKEN"
curl -fsSL http://127.0.0.1:18080/install.sh | sudo bash -s -- --token "$ENROLL_TOKEN"  # macOS 同一脚本、Darwin 分支
powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm http://127.0.0.1:18080/install.ps1))) -Token $ENROLL_TOKEN"

curl -sS -X POST http://127.0.0.1:18080/api/v1/nodes/register \
  -H "Authorization: Bearer <tenant-access>" -H 'Content-Type: application/json' \
  -d '{"name":"nas-01","os":"linux","arch":"amd64","version":"dev","tags":["home"],"capabilities":{"mesh":"degraded","subnet_routes":"unavailable","tunnel":"ready","reason":"需管理员权限"}}'
# 响应包含 node_id、agent_token、public_key、private_key；private_key 只应保存到 Agent state。

curl -sS -X POST http://127.0.0.1:18080/api/v1/nodes/<node-id>/heartbeat \
  -H "Authorization: Bearer <agent-token>" -H 'Content-Type: application/json' \
  -d '{"version":"dev","capabilities":{"mesh":"unavailable","subnet_routes":"unavailable","tunnel":"unavailable","reason":"缺少 WireGuard 系统组件 / 平台不支持"}}'

curl -sS http://127.0.0.1:18080/api/v1/agent/config?node_id=<node-id>\&version=0 \
  -H "Authorization: Bearer <agent-token>"
```

### 公开安装与下载

| Endpoint | Content-Type / 行为 |
| :--- | :--- |
| `GET /install.sh` | `text/x-shellscript`；Linux systemd 与 macOS launchd，同一脚本按 `uname -s` 分支 |
| `GET /install.ps1` | `text/plain; charset=utf-8`；Windows PowerShell 5.1/7+ 安装器 |
| `GET /downloads/{filename}` | `application/octet-stream`，响应头 `X-Neilico-Sha256` |

下载白名单严格限定为 `neilico-agent-linux-{amd64,arm64,armv7}`、`neilico-agent-darwin-{amd64,arm64}` 和 `neilico-agent-windows-amd64.exe`。未知文件名返回非 HTML 的 404。两个安装脚本和下载路径公开且限流豁免，但安装器自身仍校验令牌、下载哈希和权限。

`POST /api/v1/nodes/register`、`POST /api/v1/nodes/enroll` 与心跳都接受：

```json
{
  "capabilities": {
    "mesh": "ready | unavailable | degraded",
    "subnet_routes": "ready | unavailable",
    "tunnel": "ready | unavailable",
    "reason": "任一能力非 ready 时必填"
  }
}
```

能力写入 `nodes.capabilities` JSONB。`GET /api/v1/nodes` 的每一项和 `GET /api/v1/nodes/{id}` 都返回 `capabilities`；详情还返回该节点当前成员关系对应的 `virtual_ip` 与 `network_id`。能力不足不会令注册/心跳失败，Agent 会继续上报并记录不可用原因。


### ACME 自动签发

手工 PEM 导入继续使用 `{cert_pem,key_pem}`。ACME 使用 `issuer:"acme"` 和域名，服务端异步执行 order：

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/certificates \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"issuer":"acme","domain":"nas.example.com"}'
# 202 {"id":"<certificate-id>","status":"pending"}

curl -sS "http://127.0.0.1:18080/api/v1/certificates/<certificate-id>" \
  -H "Authorization: Bearer $TOKEN"
# 可轮询 status=pending|active|failed；响应永不包含 key_pem

curl -sS -X POST "http://127.0.0.1:18080/api/v1/certificates/<certificate-id>/renew" \
  -H "Authorization: Bearer $TOKEN"
# 202；同证书已有 order 时返回 409 order_in_flight
```

证书资源包含 `issuer`、`status`、`cert_pem`、`expires_at`、`renewed_at`、`renew_count`、`challenge_type`、`auto_renew`、`last_error`、`next_attempt_at`。`key_pem` 永远使用 `json:"-"`，不会出现在列表、详情、创建或续期响应中。手工导入记录为 `status=active`、`auto_renew=false`；ACME 记录初始为 `status=pending`。

### 告警

```bash
# 只读列表；platform_admin 可追加 &tenant_id=<uuid>
curl -sS "http://127.0.0.1:18080/api/v1/alerts?state=firing&severity=critical&page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"

curl -sS http://127.0.0.1:18080/api/v1/alerts/rules \
  -H "Authorization: Bearer $TOKEN"

curl -sS -X POST http://127.0.0.1:18080/api/v1/alerts/evaluate \
  -H "Authorization: Bearer $TOKEN"
# {"items":[...],"total":N,"insufficient_data":[...],"evaluated_at":"..."}

curl -sS http://127.0.0.1:18080/api/v1/alerts/summary \
  -H "Authorization: Bearer $TOKEN"
# {"firing":{"critical":0,"warning":0,"info":0},"resolved_recent":0,"by_rule":{...}}

curl -sS http://127.0.0.1:18080/api/v1/alerts/<alert-id> \
  -H "Authorization: Bearer $TOKEN"
# {"alert":{...},"events":[...]}
```

`state` 只接受 `firing|resolved`。Alert 本身只使用这两种状态；采集源缺失通过额外字段 `data_status=insufficient_data` 表达，手工评估响应单列且不会伪造告警。readonly 的 evaluate 返回 403。

### 域名反代

```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/domains \
  -H "Authorization: Bearer <tenant-access>" -H 'Content-Type: application/json' \
  -d '{"domain":"nas.example.com","status":"active"}'

curl -sS -X POST http://127.0.0.1:18080/api/v1/proxy-rules \
  -H "Authorization: Bearer <tenant-access>" -H 'Content-Type: application/json' \
  -d '{"domain_id":"<domain-id>","path":"/","target_type":"node","target":"<node-id>:8080","access_control":{"ip_whitelist":[],"basic_auth":false,"require_jwt":false},"enabled":true}'
```

## 错误码

| HTTP | code | 常见原因 |
| :--- | :--- | :--- |
| 400 | `invalid_request` / `invalid_id` | JSON、UUID、CIDR、分页或必填字段错误 |
| 401 | `unauthorized` / `invalid_token` / `token_expired` / `token_revoked` / `invalid_agent_token` | 凭据缺失/无效；API Token 过期或撤销使用独立错误码 |
| 403 | `forbidden` / `insufficient_scope` | 角色、scope 或 tenant scope 不足；`detail.required_scope` 指出缺失项 |
| 429 | `rate_limited` | API Token/JWT user 超过令牌桶速率，响应含 `Retry-After` |
| 404 | `not_found` | 资源不存在或不属于当前 tenant |
| 405 | `method_not_allowed` | 路由不支持该方法，响应含 `Allow` |
| 409 | `conflict` / `order_in_flight` / `acme_disabled` / `acme_tos_not_accepted` | 资源引用、并发 order，或 ACME 未启用/未同意 TOS |
| 422 | `unprocessable_entity` / `acme_order_failed` | 代理目标无法解析、节点没有虚拟 IP、ACME order 失败 |
| 500 | `internal_error` | 服务端错误，查看控制面日志 |
| 501 | `not_implemented` / `acme_revoke_not_implemented` | DNS-01、EAB 或证书撤销接口位尚未实现 |

`GET /api/v1/agent/config` 在客户端 `version` 等于服务端最新版本时返回 `304 Not Modified`，响应体为 `{"not_modified":true,"version":N}`；落后、超前或未知版本会返回 `200` 最新期望配置。

## 传输安全接口（V1-S）

* `GET /api/v1/pki/ca`：公开返回 `ca_cert_pem`，永不返回私钥。
* `POST /api/v1/pki/ca/rotate`：platform_admin 轮换内置 CA；响应只有元数据。
* `POST /api/v1/nodes/{id}/mtls`：签发/续签 Agent 客户端证书，首次响应包含 `client_cert_pem`/`client_key_pem`，私钥加密落库且列表/详情不回显。
* `GET /api/v1/nodes/{id}/mtls`：只返回序列号、有效期、指纹等元数据。
* `POST /api/v1/networks/{id}/psk/rotate`：轮换 WireGuard PSK 并递增节点配置版本。

`server.tls.client_auth=require` 时除探针、CA 下载和最小化注册引导外均需客户端证书；401 表示缺少/无效证书，不代表认证 token 可绕过。

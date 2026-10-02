# UMPP 运维手册

## 架构

```mermaid
flowchart LR
  U[用户/浏览器] --> D[Dashboard nginx :13000]
  U --> P[Builtin HTTP proxy :18081]
  D -->|/api| A[control-api :18080]
  A --> PG[(PostgreSQL :15432)]
  A --> R[(Redis :16379)]
  A --> N[(NATS JetStream :14222)]
  A -->|版本化 config| AG1[Agent A]
  A -->|心跳/流量| AG2[Agent B]
  AG1 <-->|WireGuard UDP :51820| AG2
  P --> AG2
  REL[relay placeholder wg-easy :51820/:3478] -. future data plane .- AG1
```

M2b 当前只生成 WireGuard 配置和维护 relay 元数据；Compose 的 `relay` 是 wg-easy 占位容器，不是 UMPP 自研中继协议实现。3478/udp 为未来 TURN/ICE 预留。

## 端口表

| 组件 | 容器端口 | 默认宿主端口 | 用途 |
| :--- | :---: | :---: | :--- |
| control-api | 8080/tcp | 18080/tcp | REST API、healthz、metrics |
| control-api builtin proxy | 8081/tcp | 18081/tcp | Host-based HTTP 反代 |
| control-api builtin TLS | 8443/tcp | 18443/tcp（ACME overlay） | SNI TLS 终结 |
| control-api HTTP-01 | 80/tcp（生产默认） | 18082->5002/tcp（Pebble overlay） | ACME 挑战专用 listener |
| Pebble ACME directory | 14000/tcp | 14000/tcp（测试 overlay） | RFC 8555 目录 |
| Pebble management | 15000/tcp | 8055/tcp（测试 overlay） | `/roots/0` 等管理端点 |
| dashboard | 8080/tcp | 13000/tcp | nginx 静态资源 + /api 反代 |
| postgres | 5432/tcp | 15432/tcp | GORM 数据库 |
| redis | 6379/tcp | 16379/tcp | 预留缓存/队列 |
| nats | 4222/tcp | 14222/tcp | JetStream |
| relay placeholder | 51820/udp | 51820/udp | WireGuard 占位 |
| relay placeholder | 3478/udp | 3478/udp | TURN 预留 |

生产环境只暴露 Dashboard/API/代理和必要 UDP，数据库、Redis、NATS 应绑定内网或私有 Docker network。

## Kubernetes 部署

Kubernetes 部署使用 [`deploy/helm/umpp`](../deploy/helm/umpp/README.md)，与 Docker Compose 保持相同组件/端口/环境变量语义：`control-api` 为 Deployment（8080 API、8081 反代、8443 TLS），`dashboard` 为 Deployment，PostgreSQL/Redis/NATS 默认使用外部实例，relay 为 UDP 51820/3478 的 wg-easy 占位 DaemonSet。

### 安装、升级与卸载

```bash
export PATH="$HOME/.local/bin:$PATH"
cd deploy/helm
helm lint ./umpp --values ./umpp/ci/default-values.yaml
helm upgrade --install umpp ./umpp \
  --namespace umpp --create-namespace \
  --values /secure/umpp-values.yaml
helm status umpp --namespace umpp
helm history umpp --namespace umpp
helm uninstall umpp --namespace umpp
```

Chart 的 `values.yaml` 只包含 `CHANGE_ME` 占位符。生产优先设置 `secrets.existingSecret`，或使用 `secrets.create=true` + `--set-file` 注入 `POSTGRES_PASSWORD`、`UMPP_AUTH_JWT_SECRET`、`UMPP_BOOTSTRAP_ADMIN_PASSWORD`。外部数据库密码可用 `externalDatabase.passwordSecret.name/key` 单独引用。ConfigMap 和 Chart 生成的 Secret 带 checksum annotation，值变化会滚动 Pod。

### 外部依赖

- 默认 `postgres.enabled=false`，安装前准备 PostgreSQL 16，并设置 `externalDatabase.host/port/user/database/sslmode/passwordSecret`。控制面启动时自动迁移 schema，账号需要 DDL 权限。
- `redis.enabled=false`、`nats.enabled=false` 时由外部实例提供。生产 Redis 使用 Sentinel/Cluster，NATS 使用独立 StatefulSet/Operator；`postgres/redis/nats.enabled=true` 创建的单副本 StatefulSet + PVC **仅供开发**。
- `control-api` 当前实现没有 Redis/NATS 连接环境变量，Chart 不添加 compose 中不存在的变量。

### Ingress/TLS 二选一

1. **cert-manager**：设置 `ingress.enabled=true`、IngressClass、hosts、issuer annotation 和 `ingress.tls`，由 cert-manager 终止 Dashboard/API TLS。
2. **UMPP 自带 ACME**：设置 `acme.enabled=true/agreeTos=true` 与 directory/email，并启用 `controlApi.proxy.tls.*`；把 ACME HTTP-01 端口（生产通常 80）和 8443 以 LoadBalancer/四层入口暴露。V1 仅 HTTP-01。

同一域名不要让两套机制竞争证书。生产反代/ACME/UDP 需要分别规划 L4 入口；不连接测试集群时，`verify.sh` 只做 Helm 离线渲染校验。

### 生产检查

- 至少两个 control-api/dashboard 副本，启用 HPA/PDB、资源限制、PodSecurity、NetworkPolicy、可信镜像仓库与反亲和。
- Prometheus 抓取 Service `8080/metrics`；V1-R2 的 P2P/中继/heartbeat latency 指标仍无真实采集源，保持 0。
- PostgreSQL 备份采用托管快照或 `pg_dump`，升级前同时备份 Secret/TLS/Agent state。卸载不会自动删除开发 StatefulSet PVC。
- relay 是占位实现，不是 UMPP 中继数据面；不要把 UDP 3478 当作已实现 TURN。

## API Token 签发、轮换与泄露应急

### 日常签发

1. 使用 platform_admin/tenant_admin 登录 Dashboard，在「用户与权限 → API Token」创建；scope 遵循最小权限，脚本只读时不要勾选 `*:write`。
2. CLI 等价操作：`umppctl token create --name=ci --scopes=nodes:read --expires-in-days=90`。完整 Token 只打印一次，立即写入 secrets manager；凭据文件 `~/.umppctl/config.yaml` 权限保持 0600。
3. 列表只显示 `token_prefix + "…"`。不要把 Token 传入命令行历史、工单、聊天、审计 detail 或日志；CI 用 masked secret/environment。
4. 为长期服务设置 `expires_in_days`。无过期时间只用于受控服务账号，并纳入定期轮换。

### 撤销与轮换

```bash
umppctl token list
umppctl token revoke --id <token-id>
umppctl token create --name=ci-next --scopes=nodes:read --expires-in-days=90
# 或在 Dashboard/API 使用 rotate：旧 Token 立即失效，新 Token 只显示一次。
```

撤销是幂等的：重复 DELETE 返回 200 且 `already_revoked=true`。轮换会在同一事务中撤销旧行并创建新行；旧明文立即返回 `401 token_revoked`。`api_tokens.revoked_at` 保留审计线索，不物理删除。

### 泄露应急

1. **立即撤销**对应 Token；若不确定 ID，按 `token_prefix` 在 Dashboard 列表定位。不要尝试从哈希还原明文。
2. 在 secrets manager 轮换所有引用，检查 `api_token.create/revoke/rotate` 审计、`last_used_ip`、相关资源审计和反代日志。审计/日志只保留前 8 字符，可据此关联但不会泄露明文。
3. 若 Token 可能有 `nodes:write/networks:write`，检查节点注册、密钥轮换、ACL/路由和配置版本；必要时轮换节点密钥并撤销异常配置。
4. 若 `tokens:write/admin` 泄露，审计该 Token 创建的所有子 Token 并逐一撤销；检查用户/租户/relay 变更。完成后从最小权限新 Token 恢复自动化。
5. 保存事件时间线、Token ID/prefix、影响租户和处置记录。不要在事件报告中粘贴完整 Token、JWT、密码或私钥。

### 限流与容量

`ratelimit.enabled/rps/burst` 默认 `true/20/40`，按 API Token 或 JWT user 独立计数。单实例内存实现在重启后重新填满；多副本各自限流，不提供集群共享额度。`/healthz`、`/metrics` 与 ACME HTTP-01 豁免。持续 429 时先降低客户端并发/增加退避，再评估提高 `rps` 或 `burst`，不要关闭审计。

## 备份与恢复

```bash
cd deploy/docker-compose
docker compose exec -T postgres pg_isready -U umpp -d umpp
docker compose exec -T postgres pg_dump -U umpp -d umpp --format=custom > umpp-$(date +%Y%m%d-%H%M).dump
# 恢复到空数据库：
docker compose exec -T postgres pg_restore -U umpp -d umpp --clean --if-exists < umpp-YYYYmmdd-HHMM.dump
```

同时备份 `.env`（放入 secrets manager，不要提交仓库）、TLS 证书和 Agent `state.json`。恢复后重建控制面：`docker compose up -d --build`，再检查 `/healthz`、`/metrics` 和审计日志。

## 升级流程

1. 阅读 release notes，备份 PostgreSQL 和 `.env`。
2. 在测试环境执行 `docker compose config -q && bash scripts/smoke.sh`。
3. 拉取新镜像/代码，运行 `docker compose build --pull`。
4. `docker compose up -d`，观察 `docker compose ps`、控制面日志、`umpp_nodes_online`。
5. 任一节点配置版本异常时使用 `POST /api/v1/configs/{target_type}/{target_id}/rollback` 回滚到已知版本。
6. 回滚使用上一步备份的 dump 和镜像 tag；不要删除 pgdata，除非确认恢复成功。

## V1-R2 告警规则

评估器随 `cmd/api` 启动，默认每分钟扫描全部租户；context 取消时停止。`POST /api/v1/alerts/evaluate` 可手工触发。规则和阈值只读查询：`GET /api/v1/alerts/rules`。

| 规则 ID | 生效条件 | 默认阈值 | 严重级 | 当前数据源 | 配置覆盖键 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `node_offline` | `now - node.last_seen` **大于**阈值 | 5 分钟 | `warning` | `nodes.last_seen` | `alerts.node_offline_after` / `UMPP_ALERTS_NODE_OFFLINE_AFTER` |
| `certificate_expiring` | `status=active` 且 `expires_at - now` **小于**阈值 | 30 天；剩余 **≤7 天** 为 `critical` | `warning/critical` | `certificates.expires_at/status` | `alerts.certificate_expiring_in`、`alerts.certificate_critical_in` |
| `p2p_success_rate_low` | P2P 成功率 `< 60%` | 60% | `warning` | **未接入**，返回 `data_status=insufficient_data`，不生成假告警 | `alerts.p2p_success_rate_minimum` |
| `relay_traffic_spike` | 当前中继流量 `>= 24h 均值 × 3` | 3 倍 | `warning` | **未接入**，返回 `data_status=insufficient_data`，不生成假告警 | `alerts.relay_spike_multiplier`、`alerts.relay_baseline_window` |
| `config_dispatch_failed` | 配置交付失败，或证书 `status=failed` / `last_error != ""` | 立即 | `critical`（失败交付）/ `warning`（active 证书续期错误） | `config_dispatch_failures`、`certificates.last_error/status` | 无阈值 |

边界按规格文字实现：节点恰好 5 分钟不触发、超过才触发；证书恰好 30 天不触发、小于才触发；P2P 恰好 60% 不触发；中继恰好 3 倍触发。

### 状态机与持久化

- 新条件触发写 `alerts.state=firing` 和一条 `alert_events(state=firing)`。
- 同一 `(tenant,rule,target_type,target_id)` 的当前 firing 唯一。持续评估只刷新 `since/value/last_evaluated_at`，不重复写事件；`started_at` 保持首次触发时间，Dashboard 的持续时长使用它。
- 条件消失写 `state=resolved`、`resolved_at` 和一条 resolved 事件。再触发复用当前记录并重置 `started_at`，形成完整时间线。
- resolved 当前行默认保留 7 天（`alerts.resolved_retention`），支持「当前 firing」「最近 resolved」「目标历史」查询。
- 无数据源规则用 `data_status=insufficient_data` 明示；该结果不落库、不通知、不计入 firing。

### 通知

默认 `log`，向 stdout 写结构化日志。配置 `alerts.webhook_url` 后同时 POST JSON；单次超时默认 5 秒，失败最多重试 3 次（共最多 4 次请求），最终失败只记日志，不影响评估/API 主流程。不提供邮件或短信。

```json
{
  "event": "alert.firing",
  "schema_version": "umpp.alert.v1",
  "alert": {
    "id": "<uuid>",
    "rule": "node_offline",
    "severity": "warning",
    "target_type": "node",
    "target_id": "<uuid>",
    "title": "节点离线",
    "detail": "...",
    "value": 601.2,
    "threshold": 300,
    "since": "2026-10-02T12:00:00Z",
    "state": "firing",
    "started_at": "2026-10-02T12:00:00Z"
  },
  "timestamp": "2026-10-02T12:00:01Z"
}
```

## V1-R2 指标与数据来源

Prometheus 抓取 `GET :18080/metrics`。**无采集来源的指标保持 0，不模拟增长；待 V2 接入真实采集。**

| 指标 | 类型/标签 | V1-R2 数据来源 |
| :--- | :--- | :--- |
| `umpp_nodes_online` | gauge | PostgreSQL `nodes.status=online` 实时计数 |
| `umpp_tunnel_up{network_id,node_id}` | gauge | 由 network member + node online 状态推导；不是 UDP 遥测 |
| `umpp_p2p_success_rate` | gauge | **数据源未接入，当前恒为 0；V2 接入真实采集** |
| `umpp_relay_bytes` | gauge | **中继吞吐数据源未接入，当前恒为 0；V2 接入真实采集** |
| `umpp_proxy_requests` | gauge | 内置反代真实请求计数；标签明细另有 `umpp_proxy_requests_total{domain,status}` |
| `umpp_config_version{target_type,target_id}` | gauge | `config_versions` 每目标最新版本 |
| `umpp_agent_heartbeat_latency` | gauge | 当前只记录 heartbeat 成功时间，没有请求耗时样本；**恒为 0，V2 接入真实采集** |
| `umpp_alerts_firing{severity,rule}` | gauge | `alerts.state=firing` 实时计数，所有已知组合均暴露 |

原有 ACME、TLS、证书到期和 HTTP 指标继续保留。空数据集的 `_none` 样本仅为保证 metric family 存在，数值为 0，不代表真实目标。

## ACME 自动续期处置

- 调度器每 `acme.check_interval` 扫描 `issuer=acme`、`auto_renew=true`、`status=active` 且进入 `renew_before_days` 的证书。
- 续期失败只写 `last_error`、递增失败计数和 `next_attempt_at`，`status` 保持 `active`；指数退避从 30 分钟开始，最长 24 小时。
- 成功后原子更新 `cert_pem`/`expires_at`/`renewed_at`/`renew_count`，失效内置代理证书缓存，并递增引用证书的 proxy/node 配置版本。
- 手工 `POST /api/v1/certificates/{id}/renew` 不等待退避窗口；同一证书已有 order 时返回 `409 order_in_flight`。
- `revoke` 路由当前返回 `501 acme_revoke_not_implemented`。删除被 domain 引用的证书返回 `409`。

## Pebble 真实 ACME 测试栈

只允许 Pebble/staging，不得用 Let's Encrypt 生产目录做验收。测试 overlay 会启动 Pebble 和一个 **仅提供 DNS A 记录** 的 challenge test DNS 服务；Pebble 仍通过真实 HTTP-01 GET 请求访问 control-api 的挑战响应，`PEBBLE_VA_ALWAYS_VALID=0`，没有使用 always-valid。

```bash
cd deploy/docker-compose
docker compose -f docker-compose.yml -f docker-compose.acme.yml config -q
cd ../..
bash scripts/smoke-acme.sh
```

测试端口：API 18080、HTTP 反代 18081、TLS 18443、HTTP-01 18082、Pebble directory 14000、Pebble management 8055。`scripts/smoke-acme.sh` 会执行真实签发、Pebble 链验证、SNI 指纹比对和二次 order 续期，并在失败时打印全部容器日志尾 50 行。overlay 中 `UMPP_ACME_CA_CERT_FILE=/pebble-certs/pebble.minica.pem` 用于信任 Pebble 的目录 TLS；链验证使用 management API 的 `/roots/0` 签发根。

## 日志

- 控制面：`docker compose logs -f control-api`，JSON 输出 stdout，json-file driver 限制 10m × 3。
- Dashboard/nginx：`docker compose logs -f dashboard`。
- PostgreSQL/Redis/NATS：`docker compose logs -f postgres redis nats`。
- Agent：stdout + journald；`state.json` 为敏感文件权限 0600。
- 审计：PostgreSQL `audit_logs`，通过 `GET /api/v1/audit-logs` 查询。
- 代理访问日志：MVP 记录到控制面 stdout/指标，ClickHouse 集成为 V1。

## 故障排查清单

### 容器不健康

```bash
docker compose ps
docker compose logs --tail=50 control-api postgres
curl -fsS http://127.0.0.1:18080/healthz
docker compose exec postgres pg_isready -U umpp -d umpp
```

常见原因：`.env` 占位符未替换、Postgres 密码不一致、端口被占用、`UMPP_DATABASE_DSN` 字段写错。

### 节点不上线

检查 Agent 日志、`state.json` 权限、API 网络可达性、UDP 51820、系统时间。连续 60 秒无心跳会标记 offline。

### 代理 502

检查域名 active、proxy rule enabled、node virtual IP、目标端口、从 control-api 容器到目标的连通性，以及 `control-api` 中 `builtin proxy request failed`。

### 证书过期/续期失败

先看 `GET /api/v1/certificates/{id}` 的 `status/last_error/next_attempt_at` 和 `/metrics` 的 expiry/order/renewal 指标。DNS、公网 80、CA 信任或挑战端口异常时修复后手工 `/renew`；不要因一次续期失败删除当前 active 证书。手工证书仍可重新导入匹配 cert/key 并绑定域名，客户端需要信任新证书链。

### 磁盘/日志增长

确认 json-file `max-size=10m`、`max-file=3`，归档并清理旧 `pg_dump`，监控 Docker 磁盘水位。

## 传输安全（V1-S / §10.3）

### 内置 CA 与保管

设置 `pki.enabled=true` 后，控制面首次启动会生成 ECDSA P-256 CA。CA 私钥以现有 `cert.Crypto`（AES-GCM，密钥由 `jwt_secret` 派生）加密存入 `cas.encrypted_key_pem`，不会通过 API 回显。`GET /api/v1/pki/ca` 只返回 CA 证书 PEM，供 Agent/CLI 配置信任锚。CA 轮换由 platform_admin 调用 `POST /api/v1/pki/ca/rotate`；旧 CA 行继续留在 `cas` 作为信任锚，便于旧证书在有效期内继续校验。生产环境应备份数据库和 `jwt_secret`，二者同时丢失无法恢复私钥。证书有效期由 `pki.server_cert_days`、`pki.node_cert_days` 控制，`pki.renew_before_days` 用于提前续期；节点客户端证书通过 `POST /api/v1/nodes/{id}/mtls` 签发/续签，响应中的私钥只出现一次。

### 控制面 TLS 与 mTLS

`server.tls.enabled=true` 时 API 使用 TLS；`cert_file/key_file` 与 `pki.enabled` 二选一，均为空且启用 PKI 时自动签发服务端证书。`client_auth=require` 要求客户端证书，`client_ca_file` 或 PKI CA 作为信任锚；缺少信任锚会启动失败并给出错误。`/healthz`、`/metrics`、CA 下载和最小化注册引导不需要客户端证书，以便探针和首次 enrollment 工作；管理接口、Agent 配置和心跳仍需通过认证/mTLS。`redirect_http=true` 时 `server.tls.http_port` 的明文监听器对 `/healthz`、`/metrics`、ACME challenge 直通，其它 GET/HEAD 301、其它方法 308 到 HTTPS。TLS 握手指标为 `umpp_tls_handshakes_total{result,listener}`。

回退步骤：先把 `server.tls.client_auth` 改为 `none`（或 `request`）并重启，确认健康检查和现有 token；再把 `server.tls.enabled=false` 恢复明文监听。不要在未准备好 CA/客户端证书时直接启用 require。

### HSTS 与 HTTPS 上游

代理 `proxy.tls.enabled=true` 时 `proxy.tls.redirect_http`（默认 true）让明文端口只服务 `/healthz`、`/metrics`、ACME challenge，其它请求按 GET/HEAD 301、其它方法 308 跳转；HTTPS 响应带 `Strict-Transport-Security: max-age=proxy.tls.hsts_max_age`（默认 31536000，0 不发送）。`proxy_rules.upstream_scheme` 默认 `http`，可设 `https`；`upstream_ca_file` 用于自签/私有 CA，`upstream_insecure_skip_verify` 只能显式开启且会跳过证书校验，生产不应使用。`upstream_scheme=http` 的既有行为完全不变。

### NPS 与 WireGuard

NPS 配置默认生成 `crypt: true`、`compress: true`（由 `proxy.nps.crypt/compress` 配置）。NPS 的 crypt 是隧道内对称加密，**不是端到端 AEAD**；真正的端到端加密由 WireGuard 承载。WireGuard 网络创建时生成网络级 `preshared_key`，以 `cert.Crypto` 加密落库，同一网络的所有 `[Peer]` 下发同一个 PSK；创建响应和 Agent 下发才包含明文。`POST /api/v1/networks/{id}/psk/rotate` 轮换并递增相关节点配置版本。

### 仍然未加密/未实现的链路

* `internal_ip` 直连上游由调用方网络路径决定，UMPP 不提供传输加密。
* relay 数据面仍未实现（V2）；不能把 relay 视为已加密。
* NPS crypt/compress 只保护 NPS 隧道，不是端到端加密。
* `upstream_insecure_skip_verify=true` 的 HTTPS 上游会跳过证书验证，只适合隔离测试。
* 默认配置（PKI、API TLS、proxy TLS 均关闭，`upstream_scheme=http`）保持历史明文行为，升级前必须按上述步骤启用。

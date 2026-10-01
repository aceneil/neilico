# NOTES.md — umpp 目录导览

> 用户约定：进入本目录前先读本文件；结构变化后**随时更新**，保证新会话只凭本文件即可开工。

## 这是什么
UMPP（Unified Mesh & Proxy Platform）：统一「内网穿透 + Mesh 组网」后台管理系统。三平面：控制面（管理/下发/审计）、穿透代理面（域名反代/隧道）、Mesh 组网面（WireGuard 虚拟网）。

## 文件地图
| 路径 | 作用 |
| :--- | :--- |
| `docs/UMPP_SPEC.md` | **上游需求规格**（用户提供，勿擅改；只可追加「裁决/偏差」附录） |
| `PLAN.md` | **执行计划**：里程碑 M1–M5、验收标准、技术裁决 D1–D6 |
| `docs/API.md` | 真实 REST 路由、认证、curl、错误码 |
| `docs/USER_GUIDE.md` | 部署、Agent、网络、域名、FAQ |
| `docs/OPS.md` | 架构、端口、备份恢复、升级、监控告警、排障 |
| `control-plane/` | Go 控制面 API（stdlib HTTP + GORM + PostgreSQL16；含 ACME 生命周期和 SNI TLS） |
| `agent/` | Go Agent（注册/心跳/拉配置/应用 WireGuard/子网路由） |
| `cli/` | `umppctl` 命令行 |
| `dashboard/` | Vue3 + Vite + Ant Design Vue 管理后台 |
| `deploy/docker-compose/` | 单机一键部署栈 |
| `deploy/helm/umpp/` | Kubernetes Helm Chart（默认外部 PostgreSQL，含开发依赖/relay 占位） |
| `scripts/` | `smoke.sh`、`smoke-down.sh`，退出码即判据 |

## 关键决策（详见 PLAN.md §2）
- 前端 = **Vue3 + Ant Design Vue**（规格 §4.4.2 React 与 §6 vue3 矛盾，取 Vue3）。
- 测试用**纯 Go sqlite 驱动**（`glebarez/sqlite`），生产用 PostgreSQL。
- Mesh 走 `MeshProvider` 接口：MVP = 自研 WireGuard 配置生成；EasyTier/Headscale 仅配置导出。
- 代理走 `ProxyProvider` 接口：MVP = NPS 配置生成 + 内置 `httputil.ReverseProxy` 直连。
- M5 起 compose 默认使用偏移宿主端口（本机 8080/3000/5432/6379/4222 已占用）：API 18080、Dashboard 13000、Proxy 18081、Postgres 15432、Redis 16379、NATS 14222；WireGuard 51820/udp 与 TURN 3478/udp 未被占用。

## 当前状态
| 里程碑 | 状态 | 提交 | 关键事实 |
| :--- | :--- | :--- | :--- |
| M0 骨架/计划 | ✅ | `643786d` | 仓库、规格入库、PLAN（里程碑 + D1–D6 裁决） |
| M1 控制面骨架+身份/节点 | ✅ | `c9c8372` | 14 张核心表、JWT/RBAC/租户隔离、节点注册/心跳/离线 sweeper、审计、`/metrics` |
| M2a 域名/证书/代理面 | ✅ | `c3c4af6` | 域名/证书（私钥 AES-GCM 加密）/代理规则 CRUD、NPS Provider、内置 httputil 反代、流量上报 |
| M2b Mesh + 配置版本化下发 | ✅ | `a9b753b` | 虚拟网络/成员（VIP 分配）/ACL/子网路由、WireGuard 与 EasyTier Provider、密钥轮换、304/回滚 |
| M3 Agent + CLI | ✅ | `c1aca8d` | Agent 注册/心跳/端点/配置轮询/wgctrl→shell/dry-run/路由/流量/指标，`umppctl` |
| M4 Dashboard | ✅ | `a10556d` | 登录、仪表盘、设备、域名、网络、用户、日志、设置；npm build |
| M4b 补齐读接口 | ✅ | `ce43379` | 新增 `GET /api/v1/audit-logs`、`GET /api/v1/nodes/{id}/metrics`、`relay-servers` CRUD（M4 据实报告这些接口后端从未实现，避免前端造假数据）；Dashboard 对应页面接通 |
| **M5 部署+文档+冒烟** | ✅ | `8e54d34` | Compose + Dockerfile + `scripts/smoke.sh`（12/12 PASS）+ API/USER/OPS/README；smoke 用真实 Agent dry-run |
| **V1-R2 告警体系+指标补全** | ✅ | （未提交） | 五条规则、状态机/事件、000007、log/webhook notifier、告警 API、Dashboard `/alerts`、指标数据源说明 |
| **V1-R3 Helm Chart** | ✅ | （未提交） | `deploy/helm/umpp`：control-api/dashboard Deployment、外部 PG 默认、开发 PG/Redis/NATS StatefulSet、relay 占位、Secret/Ingress/HPA/PDB/NetworkPolicy/ServiceMonitor；`ci/verify.sh` 离线断言 |
| **V1-R4 API Token/Scope/限流** | ✅ | （未提交） | migration `000008`、API Token 哈希/轮换/撤销、角色→scope 兼容表、`RequireScope`、按 Token/user 令牌桶、CLI token 命令、Dashboard 真实 Token 页面 |

### 当前运行状态（2026-10-02 01:5x 实测）
- **整个栈仍在运行**（`Up 3 hours (healthy)`）：API `:18080`、内置反代 `:18081`、Dashboard `:13000`、PG `:15432`、Redis `:16379`、NATS `:14222`、relay 占位 UDP `:51820/:3478`
  （8080/3000/5432/6379/4222 被本机既有容器占用，故整体改端口）
- **V1-R1 ACME/Pebble overlay 额外端口**：TLS `18443->8443`、HTTP-01 `18082->5002`、Pebble directory `14000`、Pebble management `8055`；挑战测试 DNS 只在 Compose 网络内提供 A 记录，HTTP-01 响应来自 control-plane。
- 独立在线验证脚本：`bash scripts/verify-live.sh`（只打印状态与计数，不回显任何密钥）
  实测结果：healthz `status=ok db=up`；8 节点注册过（smoke 结束后 offline，属预期）；4 网络 / 3 域名 / 3 代理规则；`audit-logs total=61`；
  `umpp_proxy_requests_total{domain="smoke-…",status="200"} 1` ← **反代真的服务过 200**；Dashboard 13000 → 200
- 收尾：`bash scripts/smoke-down.sh --yes` 停栈

### 流水线自动化（本项目沉淀，可复用）
- 自主驱动器：`~/.hermes/scripts/umpp-autodrive.sh` + systemd 用户定时器 `umpp-autodrive.timer`（每 5 分钟）
  状态机 `~/.hermes/cache/umpp-pipeline.state`（`ROUND/PID/HANDLED`）；轮次链 `M1→M2a→M2b→M3→M4→M4b→M5→DONE`。
  **它自己跑完了 M4→M4b→M5（22:20–23:32），无需人干预。**
- 通知双通道：桌面 `notify-send` + 飞书推送（`hermes -p chatrob send -t feishu:oc_…`，**不需要 gateway 常驻**）。
- ⚠️ 教训：**不要依赖 Hermes 后台进程退出通知来唤醒 manager**——实测会被 SIGTERM（`process-results/*.json` 里 `exit_code=-15`）；
  默认 profile 无 gateway 时 cron 也不会触发。

### 关键接口事实（Agent/Dashboard 对接必读）
- 登录：`POST /api/v1/auth/login`，返回 `token`、`refresh_token`、`user`。
- Agent 注册：`POST /api/v1/nodes/register`（管理 token），响应一次性 `agent_token`、`private_key`；心跳使用 `POST /api/v1/nodes/{id}/heartbeat`。
- `GET /api/v1/agent/config?node_id=&version=`：**`version` 是客户端状态提示，不是资源 ID** —— 与最新版本相等返回 **HTTP 304**；落后 / 超前 / 未知版本一律返回 **200 + 最新期望配置**（保证任何客户端都收敛）。历史快照走 `GET /api/v1/configs` 与 `POST /api/v1/configs/{target_type}/{target_id}/rollback`。
- 节点端点上报 `POST /api/v1/nodes/{id}/network-report`（agent_token 认证），成功后同网络 peer 版本递增。
- 私有数据保护约定：证书 `key_pem`、节点密钥 `private_key` 一律 `json:"-"` 或一次性返回；agent_token 只存 SHA-256。
- 告警：`GET /api/v1/alerts`（firing/resolved 筛选）、`/alerts/rules`、`/alerts/summary`、`GET /alerts/{id}` 时间线；`POST /api/v1/alerts/evaluate` 仅 platform_admin/tenant_admin/ops。platform_admin 列表可传 `tenant_id`。
- `Alert.state` 只有 `firing|resolved`；P2P/中继采集缺失用 `data_status=insufficient_data` 且不落库/不通知。`since` 是最近观测时间，`started_at` 是首次触发时间（Dashboard 持续时长使用后者）。
- `/metrics` 的 `umpp_p2p_success_rate`、`umpp_relay_bytes`、`umpp_agent_heartbeat_latency` 当前无真实采集，恒为 0，V2 接入；其余 V1-R2 必需指标有数据库或请求真实来源。
- API Token 明文 `umpp_<32-byte base64url>` 只在 create/rotate 响应出现一次；数据库只存 SHA-256，展示/审计最多 `token_prefix + "…"`。撤销幂等，rotate 旧值立即失效。
- API Token scope：`nodes/networks/proxy/certs/tokens/alerts` 的 read/write + `admin`；API Token 严格按自身 scopes 且不能创建更大 scopes 的子 Token。JWT 维持 RBAC，映射表见 `internal/auth/scopes.go` 与 `docs/API.md`。
- 限流 `ratelimit.enabled/rps/burst`（默认 true/20/40）按 API Token ID 或 JWT user 使用并发安全内存令牌桶；healthz/metrics/ACME challenge 豁免。
- `target_type=node` 的反代目标是 `<node UUID>:<port>`，节点虚拟 IP 来自 network member；M5 冒烟用临时 echo 容器挂到该 VIP 验证 Host 反代。

## 常用命令
```bash
# 控制面（开发）
cd control-plane && go test ./... && go run ./cmd/api -c configs/config.yaml
# Dashboard
cd dashboard && npm ci && npm run dev
# 全栈（默认偏移端口）
cd deploy/docker-compose && docker compose up -d --build
# 端到端冒烟
bash scripts/smoke.sh
# ACME/Pebble 真实协议冒烟（只用 Pebble，不用 LE 生产）
bash scripts/smoke-acme.sh
# 清理（会删 pgdata，必须显式确认）
bash scripts/smoke-down.sh --yes
# Helm 离线 lint/render/schema/secret 断言
export PATH="$HOME/.local/bin:$PATH"
cd deploy/helm && bash umpp/ci/verify.sh
```

## 坑与注意
- Go 依赖代理：若 `go mod tidy` 卡住，设 `GOPROXY=https://goproxy.cn,direct`。
- npm 用 `npm ci`（有 lock 时），无 lock 时 `npm install`。
- Agent 应用 WireGuard 需要 root + `wg` 或 wgctrl；无权限环境下必须能「dry-run 打印配置」而不报错。
- Compose relay 是 **wg-easy 占位**，不是中继数据面；不要把 3478/udp 当作已实现 TURN。当前宿主缺少 iptables NAT 模块时 wg-easy 会记录接口启动错误，健康检查只验证占位 Web 监听。
- `.env` 已在 `.gitignore`；只提交 `.env.example` 占位符，不要提交真实 JWT/密码/Agent 私钥。
- ACME 配置默认关闭；只有 `enabled=true` 且显式 `agree_tos=true` 才允许 order。`dns-01` 与 EAB 保留接口位，当前返回 `ErrNotImplemented`。
- Helm 默认必须接外部 PostgreSQL；`postgres/redis/nats.enabled=true` 只供开发。`secrets.existingSecret` 启用时不渲染 Secret。Chart 详细字段、TLS 二选一和生产限制见 `deploy/helm/umpp/README.md`。
- Helm `verify.sh` 只验证离线渲染，不连接集群；预发布仍需验证 LB/Ingress、UDP、滚动升级、PVC/备份恢复与 NetworkPolicy。

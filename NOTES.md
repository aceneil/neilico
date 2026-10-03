# NOTES.md — neilico 目录导览

> 用户约定：进入本目录前先读本文件；结构变化后**随时更新**，保证新会话只凭本文件即可开工。

## 这是什么
NEILICO（Unified Mesh & Proxy Platform）：统一「内网穿透 + Mesh 组网」后台管理系统。三平面：控制面（管理/下发/审计）、穿透代理面（域名反代/隧道）、Mesh 组网面（WireGuard 虚拟网）。

## 文件地图
| 路径 | 作用 |
| :--- | :--- |
| `docs/NEILICO_SPEC.md` | **上游需求规格**（用户提供，勿擅改；只可追加「裁决/偏差」附录） |
| `PLAN.md` | **执行计划**：里程碑 M1–M5、验收标准、技术裁决 D1–D6 |
| `docs/API.md` | 真实 REST 路由、认证、curl、错误码 |
| `docs/USER_GUIDE.md` | 部署、Agent、网络、域名、FAQ |
| `docs/OPS.md` | 架构、端口、备份恢复、升级、监控告警、排障 |
| `control-plane/` | Go 控制面 API（stdlib HTTP + GORM + PostgreSQL16；含 ACME 生命周期和 SNI TLS） |
| `agent/` | Go Agent（注册/心跳/拉配置/应用 WireGuard/子网路由） |
| `cli/` | `neilicoctl` 命令行 |
| `dashboard/` | Vue3 + Vite + Ant Design Vue 管理后台 |
| `deploy/agent/` | Agent 最小 Docker 镜像与 README（入口 `neilico-agent run`，读 `NEILICO_TOKEN`） |
| `deploy/allinone/` | **现行形态**：单容器（PostgreSQL + 控制面 API + 内置反代 + Dashboard 同容器），构建上下文=仓库根 |
| `deploy/docker-compose/` | 历史测试栈（6 容器 postgres/redis/nats/control-api/dashboard/relay），**非现行形态** |
| `deploy/helm/neilico/` | Kubernetes Helm Chart（默认外部 PostgreSQL，含开发依赖/relay 占位） |
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
| M3 Agent + CLI | ✅ | `c1aca8d` | Agent 注册/心跳/端点/配置轮询/wgctrl→shell/dry-run/路由/流量/指标，`neilicoctl` |
| M4 Dashboard | ✅ | `a10556d` | 登录、仪表盘、设备、域名、网络、用户、日志、设置；npm build |
| M4b 补齐读接口 | ✅ | `ce43379` | 新增 `GET /api/v1/audit-logs`、`GET /api/v1/nodes/{id}/metrics`、`relay-servers` CRUD（M4 据实报告这些接口后端从未实现，避免前端造假数据）；Dashboard 对应页面接通 |
| **M5 部署+文档+冒烟** | ✅ | `8e54d34` | Compose + Dockerfile + `scripts/smoke.sh`（12/12 PASS）+ API/USER/OPS/README；smoke 用真实 Agent dry-run |
| **V1-R2 告警体系+指标补全** | ✅ | （未提交） | 五条规则、状态机/事件、000007、log/webhook notifier、告警 API、Dashboard `/alerts`、指标数据源说明 |
| **V1-R3 Helm Chart** | ✅ | （未提交） | `deploy/helm/neilico`：control-api/dashboard Deployment、外部 PG 默认、开发 PG/Redis/NATS StatefulSet、relay 占位、Secret/Ingress/HPA/PDB/NetworkPolicy/ServiceMonitor；`ci/verify.sh` 离线断言 |
| **V1-R4 API Token/Scope/限流** | ✅ | （未提交） | migration `000008`、API Token 哈希/轮换/撤销、角色→scope 兼容表、`RequireScope`、按 Token/user 令牌桶、CLI token 命令、Dashboard 真实 Token 页面 |
| **改名 UMPP→NEILICO** | ✅ | `1e42e4f` | Go 模块、容器名、compose 项目名、`UMPP_→NEILICO_` 环境变量前缀、文档全量改名 |
| **V2A1 单容器打包** | ✅ | `21c8450` | 6 容器 → 1（`deploy/allinone/`）；Go 二进制同源直接服务前端，去掉 nginx |
| **E1 节点一键接入** | ✅ | （未提交） | 自包含 enroll token、自注册/幂等/VIP/审计、公开二进制与 `install.sh`、Agent 零配置 run、`docs/AGENT_ENROLL.md` |
| **上线后缺陷修复** | ✅ | `d12230a` `ece5629` `2c3cefb` | ①登录表单点击无反应（AntDV `<a-form>` 缺 `:model`）②侧栏/header 深底深字（对比度 1.13:1） |

### 常驻部署（生产用 Docker 目录那份；2026-10-03 改为**单容器**）
> **仓库内 `deploy/docker-compose/`（6 容器：postgres/redis/nats/control-api/dashboard/relay）仅供历史上的测试栈**（项目名 `neilico-m5`、命名卷、固定端口），**不是现行形态**。
> **现行形态 = `deploy/allinone/`（单容器）**，由 `/home/neil/Documents/Docker/docker-compose.neilico.yaml`（顶层 `name: neilico`）常驻，按本机目录约定维护。

- 形态：**1 个容器 `neilico`** = PostgreSQL 16 + 控制面 API + 内置反代 + Dashboard 静态资源（Go 二进制直接服务前端，**同源、无 nginx**）
- 部署文件：`/home/neil/Documents/Docker/docker-compose.neilico.yaml`
  （`build.context` = 仓库根 `/home/neil/Documents/Projects/neilico` 绝对路径，`dockerfile: deploy/allinone/Dockerfile`，`image: neilico-allinone:local`）
- 持久数据：`/home/neil/Documents/Docker/data/neilico/pg`（宿主目录绑定到容器 `/var/lib/postgresql/data`）
- 秘密：`/home/neil/Documents/Docker/data/neilico/neilico.env`（mode 600，非仓库；管理员邮箱 `admin@neilico.local`）
  查看管理员密码（值不入文档）：`/home/neil/Documents/Docker/data/neilico/show-admin-password.sh`
- 端口：`13000 -> 8080`（Dashboard + API **同端口/同源**，LAN）、`18081 -> 8081`（内置反代，LAN）；PostgreSQL **只在容器内**（不映射宿主）
- `restart: unless-stopped`；TLS/mTLS 与 ACME 本轮关闭
- 启停：`cd /home/neil/Documents/Docker && docker compose -f docker-compose.neilico.yaml up -d|stop|restart`
  （源码更新后 `up -d --build` 重建镜像；**禁止 `down -v`**）
- **已移除的 3 个无用容器及理由**：
  - `redis` —— 代码里**零引用**（纯装饰容器，REDIS 相关配置项无任何调用点）
  - `nats` —— 代码里**零引用**（纯装饰容器）
  - `relay` —— wg-easy **占位**，无 NEILICO relay 数据面（TURN 3478 从未实现）；V2 实现真实中继后再加独立进程
  （`nginx` 也一并去掉：其唯一作用是静态托管 + 反代，现由 Go 二进制同源直接提供）
- Homepage 导航卡片：`/home/neil/Documents/Docker/data/homepage/config/services.yaml` 的 `- 业务:` 组
  `neilico`（原 umpp 卡片原地改名），href `http://192.168.123.90:13000`，container `neilico`
- **2026-10-03 实测（单容器）**：`docker ps` 恰好 1 行 `neilico (healthy)`；
  容器内同时有 `postgres` 与 `neilico-control` 两个进程；LAN `13000` → **200** 且 body 含 `<div id="app"`；
  `/healthz` = `{"db":"up","status":"ok","version":"v1-single-20261003"}`；`/metrics` **67 条 `neilico_*`**（2026-10-03 19:1x 实测，随 V1-R2/R4 增长）；
  `POST /api/v1/auth/login` → **200 + token（长度 443）**，错密码 → **401**；带 token `GET /api/v1/nodes` → 200；
  深链 `/nodes` `/certificates` `/pki` `/alerts` → 200、`/nope.js` → 404；
  `docker restart` 后仍 healthy 且可登入（日志走「已有数据目录」分支、无 `already exists`）；
  `docker stop -t 30` → 退出码 **0**、日志含 `shutdown complete`、无 recovery 痕迹。

> 旧的常驻 6 容器 UMPP 栈（项目名 `umpp`）已于同日下线，数据改名保留在
> `/home/neil/Documents/Docker/data/umpp-legacy-20261003/`（取证：业务表全空，仅 bootstrap 管理员+租户+6 条登录审计）。

### 历史运行状态（2026-10-02 01:5x 测试栈实测，现已被上节替代）
- 测试栈曾整体运行（`Up 3 hours (healthy)`）：API `:18080`、内置反代 `:18081`、Dashboard `:13000`、PG `:15432`、Redis `:16379`、NATS `:14222`、relay 占位 UDP `:51820/:3478`
  （8080/3000/5432/6379/4222 被本机既有容器占用，故整体改端口）
- **V1-R1 ACME/Pebble overlay 额外端口**：TLS `18443->8443`、HTTP-01 `18082->5002`、Pebble directory `14000`、Pebble management `8055`；挑战测试 DNS 只在 Compose 网络内提供 A 记录，HTTP-01 响应来自 control-plane。
- 独立在线验证脚本：`bash scripts/verify-live.sh`（只打印状态与计数，不回显任何密钥）
  实测结果：healthz `status=ok db=up`；8 节点注册过（smoke 结束后 offline，属预期）；4 网络 / 3 域名 / 3 代理规则；`audit-logs total=61`；
  `neilico_proxy_requests_total{domain="smoke-…",status="200"} 1` ← **反代真的服务过 200**；Dashboard 13000 → 200
- 收尾：`bash scripts/smoke-down.sh --yes` 停栈

### 流水线自动化（本项目沉淀，可复用）
- 自主驱动器：`~/.hermes/scripts/neilico-autodrive.sh` + systemd 用户定时器 `neilico-autodrive.timer`（每 5 分钟）
  状态机 `~/.hermes/cache/neilico-pipeline.state`（`ROUND/PID/HANDLED`）；轮次链 `M1→M2a→M2b→M3→M4→M4b→M5→DONE`。
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
- `/metrics` 的 `neilico_p2p_success_rate`、`neilico_relay_bytes`、`neilico_agent_heartbeat_latency` 当前无真实采集，恒为 0，V2 接入；其余 V1-R2 必需指标有数据库或请求真实来源。
- 接入令牌格式 `neilico-enroll.<base64url payload>.<base64url HMAC>`，payload 自带 server/tenant/network/jti/exp；DB 只存完整串 SHA-256。创建响应的 `commands.{linux,docker}` 与 token 都只出现一次；同 jti+同请求重放不重发凭据。
- 公开引导：`GET /install.sh`（shell script）与 `GET /downloads/neilico-agent-{os}-{arch}`（`X-Neilico-Sha256`）；enroll 走来源 IP 令牌桶，install/download 明确限流豁免。
- Agent 可用 `neilico-agent enroll --token` 或 `run --token/--token-file/NEILICO_TOKEN`；state 目录取 `NEILICO_STATE_DIR`/`--state-dir`。
- API Token 明文 `neilico_<32-byte base64url>` 只在 create/rotate 响应出现一次；数据库只存 SHA-256，展示/审计最多 `token_prefix + "…"`。撤销幂等，rotate 旧值立即失效。
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
cd deploy/helm && bash neilico/ci/verify.sh
```

## 坑与注意
- Go 依赖代理：若 `go mod tidy` 卡住，设 `GOPROXY=https://goproxy.cn,direct`。
- npm 用 `npm ci`（有 lock 时），无 lock 时 `npm install`。
- Agent 应用 WireGuard 需要 root + `wg` 或 wgctrl；无权限环境下必须能「dry-run 打印配置」而不报错。
- Compose relay 是 **wg-easy 占位**，不是中继数据面；不要把 3478/udp 当作已实现 TURN。当前宿主缺少 iptables NAT 模块时 wg-easy 会记录接口启动错误，健康检查只验证占位 Web 监听。
- `.env` 已在 `.gitignore`；只提交 `.env.example` 占位符，不要提交真实 JWT/密码/Agent 私钥。
- ACME 配置默认关闭；只有 `enabled=true` 且显式 `agree_tos=true` 才允许 order。`dns-01` 与 EAB 保留接口位，当前返回 `ErrNotImplemented`。
- Helm 默认必须接外部 PostgreSQL；`postgres/redis/nats.enabled=true` 只供开发。`secrets.existingSecret` 启用时不渲染 Secret。Chart 详细字段、TLS 二选一和生产限制见 `deploy/helm/neilico/README.md`。
- Helm `verify.sh` 只验证离线渲染，不连接集群；预发布仍需验证 LB/Ingress、UDP、滚动升级、PVC/备份恢复与 NetworkPolicy。
- **登录表单必须保留 `:model="form"`**（`dashboard/src/pages/LoginPage.vue`）：AntDV 的 `Form.js handleSubmit` 只在 `props.model` 存在时才 `validateFields().then(emit('finish'))`；缺了它 → 原生 submit 被 preventDefault、`finish` 永不触发 → **点击登录零请求、零报错、按钮不进 loading**（看着像后端挂了）。改这里务必在真浏览器点一次表单复验。
- **AntDV 的 `Layout.Sider` / `Layout.Header` 默认背景是硬编码深色 `#001529`，必须显式覆盖且要 `!important`**（AntDV 的运行时注入样式排在 main.css 之后，同权重时它赢）。我们踩的坑：侧栏和 header 都露着深色底，而文字用 `var(--text)`（浅色主题下是深墨色）→ **对比度 1.13:1，肉眼看不见**。
  - 约定：**侧栏 = 深色面**（`--sider-bg`，日/夜一致，菜单恒 `theme="dark"`，文字一律 `rgba(255,255,255,.88)`/白）；**header 属于内容区**，跟随主题（`background: var(--surface) !important`）。`.ant-layout` 的默认灰底 `#f5f5f5` 也要归位成 `--page-bg`。
  - 验证方式：逐元素读 `getComputedStyle(el).color/backgroundColor` 算对比度，**两套主题都要量**（header 那条只在浅色模式暴露，深色模式看不出来）。当前实测：侧栏文字 13–18:1、header 标题 14–16:1；选中项 4.1:1（AntDV 蓝底白字）。

## 验收方法论教训（本项目实测踩到）

- **验「API 能登录」≠ 验「用户能登录」**：`curl POST /api/v1/auth/login` 返回 200 曾让我误判登录可用；而 UI 表单从 M4 起就是哑的（缺 `:model`），一路躲过所有验收。
- **截图脚本注入 token 会掩盖坏掉表单**：V1F 那 18 张「已登录」截图是往 localStorage 写 token 拿到的，没走表单。凡交付含交互（表单/按钮/参数提交），验收必须**真点一遍**。
- **权威网络证据用 `performance.getEntriesByType('resource')`**（页面内 patch fetch/XHR 可能被绕过）：点击后看有无**新增** `/api/` 条目。
- 复验「登录后」而不碰真密码：`curl` 取 token → 注入 localStorage（键 `neilico.access_token/refresh_token/user/remember`）→ 断言 URL 不回落 `/login` + 已鉴权 API 全通 + 侧栏渲染。
- 默认浏览器视口可能只有 800×479：按 `getBoundingClientRect()` 算的坐标常落在视口外，`elementFromPoint` 返回 null；且 `overflow:auto` 在内容容器上时 `window.scrollTo` 无效、`documentElement.scrollHeight` 也不变——**别据此报「按钮被裁掉」的假 bug**（本机 820px 断点已有 `display:block; overflow:auto`）。

## V1-S 传输安全记录

内置 CA、API TLS/mTLS、代理 HTTPS/HSTS/HTTPS 上游、NPS crypt/compress、WireGuard PSK 已实现；默认值保持关闭或 http。端到端加密仅 WireGuard 保证，relay 未实现，NPS crypt 不是端到端 AEAD，`internal_ip` 上游仍可能明文。

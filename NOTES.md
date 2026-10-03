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
| `docs/AGENT_ACCEPTANCE.md` | **四种接入方式（Docker/Linux/macOS/Windows）的真机验收清单**：逐步命令、判定标准、回收步骤、错误对照、能力边界（Windows/macOS 的 Mesh 目前如实报 degraded） |
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
| **E1 节点一键接入（CF Tunnel 式）** | ✅ | `9abbb7f` + `25dfc15` | 自包含签名令牌、无鉴权自注册、`/install.sh`、`/downloads/agent-*`、Agent `enroll`/`NEILICO_TOKEN`、`deploy/agent/Dockerfile`；**manager 实测 26/26 通过**（含原样执行接口给的 docker 命令 → 节点 online + VIP + wg0 + 心跳） |
| **E2 跨平台连接器 + 能力上报** | ✅ | `4a75dc4` | `install.ps1`（New-Service/sha256/管理员检查/-DryRun）、`install.sh` 增 Darwin/launchd 分支、6 个平台二进制分发、`nodes.capabilities` 如实上报、补上节点详情 VIP；**过程中修掉一个升级崩溃 bug** |
| **E3 四平台接入界面** | ✅ | `ef57a82` | 设备页「接入设备」两步弹窗（Docker/Linux/macOS/Windows 四页签 + 一键复制 + 倒计时 + 重新生成）、令牌管理（状态/撤销）、列表与详情显示 VIP + 能力徽标 + 原因；命令严格取自接口 `commands.*`。manager 真浏览器 3840×2160 复验通过 |
| **上线后缺陷修复** | ✅ | `d12230a` `ece5629` `2c3cefb` `ddf34c1` | ①登录表单点击无反应（AntDV `<a-form>` 缺 `:model`）②侧栏/header 深底深字（1.13:1）③页头标题被裁（AntDV `Layout.Header` 的 64px 高/行高盖掉我们的 76px） |

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
- 接入令牌格式 `neilico-enroll.<base64url payload>.<base64url HMAC>`，payload 自带 server/tenant/network/jti/exp；DB 只存完整串 SHA-256。创建响应的 `commands.{linux,macos,windows,docker}` 与 token 都只出现一次；同 jti+同请求重放不重发凭据。
- 公开引导：`GET /install.sh`（Linux systemd/macOS launchd）与 `GET /install.ps1`（Windows Service），下载固定六名且带 `X-Neilico-Sha256`；enroll 走来源 IP 令牌桶，install/download 明确限流豁免。
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
# 前端 e2e 冒烟（浏览器真跑；含「登录表单哑火」回归护栏）
#   默认打常驻部署，可用 E2E_BASE_URL 覆盖；用系统 Chrome，无需下浏览器
cd dashboard && npm run e2e
cd dashboard && E2E_BASE_URL=http://127.0.0.1:5175 npm run e2e
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
- **装配漏传 option 会让公开端点静默 404（单测抓不到）**：`cmd/api/main.go` 构造 `api.ProxyOptions` 时漏了 `Downloads`/`Enroll` → `downloadsDir` 为空 → `/downloads/*` 全 404（尽管镜像里二进制齐全）。**单测自己构造 options 传了临时目录，所以全绿**。已在 main.go 补齐并加启动告警（`agent downloads ready` / `warns when unusable`）——凡"只在真机部署才暴露"的装配项，都要留一条启动日志或部署级断言。
- **E1 的节点详情展示缺口已由 E2 后端补齐**：列表/详情返回成员关系对应的 `virtual_ip`/`network_id`，同时返回 `capabilities`；Dashboard 展示仍留给 E3，前端本轮未改。
- **E1 一键接入的实测口径**（`/tmp/neilico-e2e-docker.sh`，26 PASS）：公开端点 200+sha256/404、负向对照（受限 API Token 建接入令牌 → 403）、原样执行接口给的 docker 命令 → 容器自注册 online、成员表分到 VIP、心跳 30s 推进、容器内 `wg0` 公钥与 state.json 一致、用尽 410/撤销 401/篡改 401、同请求重放返回同一节点且 `replayed=true`。
  - 踩过的测试坑：**时间窗必须 > 心跳间隔 30s**（我一开始只等 12s，误判"心跳不动"）；**Mesh 接口要等 20s+ 再断言**（刚 enroll 完 wg0 还没建）；测试要**可重入**（API Token 名与网络 CIDR 都要唯一，否则 409）。
- **🔴 AutoMigrate 给已有数据的表加 NOT NULL 列必须带 default，否则升级必崩**：`nodes` 新增 `capabilities` 时 tag 写成 `type:jsonb;not null` 无 default → 生成 `ALTER TABLE nodes ADD capabilities JSONB NOT NULL` → PostgreSQL 报 `column "capabilities" ... contains null values` → API exit 1 → **容器无限重启**（真机实测，库里只有 1 行历史数据就触发）。修法：tag 加 `default:'{}'`，空值在读取边界用 `Capabilities.Normalize()` 补成明确状态。
  - **注意：`migrations/*.sql` 在启动时根本不会被执行**（`cmd/api`、`db.go`、entrypoint 都没引用），**AutoMigrate 是生产唯一的 schema 路径**，所以**模型 tag 才是唯一真相**，SQL 文件只是文档/外部工具用。
  - **这类 bug 单测抓不到**：单测迁移的是**空库**，没有行就永远不会报 `contains null values`。已补回归测试 `internal/db/migrate_upgrade_test.go`（建表 → 删新列 → 用原始 SQL 插历史行 → 再 AutoMigrate），并做过**变异验证**（去掉 default 会红，报 `Cannot add a NOT NULL column with default value NULL`）。**今后凡新增 NOT NULL 列，必须先跑这条测试。**
- **删除节点曾返回 500（已修 `Delete` + 外键冲突兜 409）**：`network_members` / `subnet_routes` / `traffic_logs` 对 `nodes` 都是 **ON DELETE RESTRICT**（`node_enrollments` 是 CASCADE），直接删节点会撞外键 → 500。现在 `NodeService.Delete` 在**事务内**先清节点级附着与遥测（网络成员/子网路由/流量日志）再删节点；审计留痕在 `audit_logs`（无外键）不受影响。**真机验证**：删一个有成员的节点 → **204**，成员行同时消失，无外键报错。回归测试用 `_pragma=foreign_keys(1)` 的 sqlite 复现该约束（默认单测不启用外键，抓不到），并做过变异验证。
- **浅色主题下 AntDV 预设 tag 文字对比度不足**（实测 12px 小字：绿 3.37 / 橙 3.34 / 蓝 ≈3.7，均 < 4.5）。已用 `[data-theme='light'] .ant-tag-{green,orange,red,blue}` 压到同色系更深一档（现 5.09–7.04）。**新加任何 tag 色都要量对比度**。
- **验前端不必先部署**：`VITE_API_BASE` 同时被当作 dev 代理目标**和**客户端 API base（Vite 会把 `VITE_*` 注入前端），所以给 dev server 设它会让浏览器跨域直连后端 → **CORS 失败（Network Error）**。可靠做法：`npm run build` 后用 `/tmp/serve-dist.py`（静态服务 dist + 同源 `/api` 反代到真后端）——验的就是待部署的那个 bundle，且同源无 CORS。
- **Agent 跨平台现状（E2）**：六目标 `windows/amd64`、`darwin/{amd64,arm64}`、`linux/{amd64,arm64,armv7}` 均已本地 `go build` 通过；能力探测按工具/TUN/系统组件/管理员权限真实上报。Windows/macOS 真机安装、WireGuardNT/系统扩展和 launchd/Windows Service 生命周期尚未在真机执行，不能据脚本语法通过推断真机已组网。
- **令牌只在首次需要，但历史实现会让「文件里留着废令牌」的 agent 崩溃重启（已修）**：`applyEnrollFlags` 过去只从令牌推导服务器地址——令牌不可解析就直接退出；更隐蔽的是 state 里保存的 `Server` **从不被采纳**，于是 `cfg.Server` 会停在 `config.Default()` 的占位地址 `https://api.neilico.example.com`。触发场景：compose 文件里留了个占位/过期/贴错的令牌 → 容器 `Restarting (1)`（实测日志 `cannot read server from enrollment token; pass --server`），而 state.json 里凭据一应俱全。现优先级明确为 **`--server` > 配置文件里的非默认 server > 令牌载荷 > state.json > 默认值**；令牌不可解析但 state 可用 → 打警告并沿用 state（无 state 时仍明确报错，因为首次接入确实需要令牌）。回归测试 `agent/cmd/agent/enroll_flags_test.go`（含 6 个子场景）。
- **compose 与 `docker run` 的卷语义不同（本机 agent 部署踩到）**：`docker run -v 名字:/路径` 会自动创建命名卷并隐式声明；**compose 必须在顶层写 `volumes:`**，否则报 `service "..." refers to undefined volume ...: invalid compose project`。本机 agent 的 compose 用**绑定挂载** `./data/neilico-agent` 绕开该坑，见 `/home/neil/Documents/Docker/docker-compose.neilico-agent.yaml`（也用 `docker-compose.neilico.yaml` 定义控制面）。
- **设备列表曾需手动刷新才看到新设备（已加自动刷新）**：`NodesPage` 现在每 **10s** 静默轮询（`load({silent:true})`：不显示 loading、失败不覆盖内容），页面不可见时**暂停**、切回前台**立即刷一次**；对比前后列表**发现新设备时弹提示**（`发现 N 台新设备：xxx`）；页头有开关（默认开）+ 上次刷新时间。另外「接入设备」弹窗在展示命令期间每 4s 探测一次，设备一 enroll 就在弹窗里直接显示**成功横幅**（设备名/平台/虚拟IP）并 `emit('enrolled')` 让列表立刻刷新——**无需关窗、无需 F5**。
  - 验收（真实浏览器 + 真实设备）：页面零操作下 4 台 → 5 台自动更新，并抓到提示 `发现 1 台新设备：autorefresh-check-3`；轮询请求数按 10s 递增。
  - **测这类"自动刷新"要小心**：无头浏览器里 `document.visibilityState === 'hidden'`，我的"不可见则暂停"逻辑会正确地不发请求 → 看起来像"没生效"。必须先用 CDP `Page.bringToFront` 让页面变 visible 再测；且**时序要可控**（先开观察窗口、再让设备延迟 enroll），否则提示（3 秒）会在两次工具调用的空隙里弹出又消失，误判成"没提示"。
- **Docker 接入命令的镜像地址必须是「目标机真能拉到」的**：命令历史上写的是本机 tag `neilico-agent:local`，别的机器执行会得到 `pull access denied for neilico-agent, repository does not exist`（实测）。现已由**公开仓库 `aceneil/neilico-agent`** 的 GitHub Actions 构建发布为 **public 包**；地址可配（`enroll.agent_image` / `NEILICO_ENROLL_AGENT_IMAGE`，代码默认值见 `config.DefaultAgentImage`）。回归测试 `internal/api/enroll_commands_test.go` **独立断言不含 `neilico-agent:local`**。
  - **命令形态**：**单条 `docker run`**（隐式拉取）。曾短暂加过显式 `docker pull … && \` 前缀（为了让"往哪拉"一目了然），后按用户要求去掉——"镜像来源"改由界面单独标注（响应字段 `agent_image` → Docker 页签顶部的「镜像来源」行）。
  - **公开包的机制（已验证）**：GitHub 规则是**包继承发布它时所用仓库的可见性**——用 `GITHUB_TOKEN` 从**公开仓库**发布 → 包自动 public。用户命名空间下的容器包**无法用 REST API 改可见性**（`PATCH /user/packages/container/<name>` 对私有和已公开的包一律 404，实测含对照包），只能网页 UI 或走"公开仓库发布"。
  - **发布流程**：改 `agent/` 代码 → 同步到公开仓库（`/tmp/public-repo` 的组装方式：`agent/` 全量 + `deploy/agent/Dockerfile` + `control-plane/{go.mod,go.sum,pkg/capabilities,pkg/enrolltoken,testkit}`）→ push 即触发 `.github/workflows/publish-agent.yml` 构建推送（用仓库自带 token，**公开库内不存任何密钥**）。
  - **推私有包（备用）**：`docker tag neilico-agent:local ghcr.io/aceneil/neilico-agent:latest && docker push …`。凭据在 `~/.hermes/.env` 的 `GITHUB_TOKEN`（classic PAT，账号 `aceneil`，scopes `repo, workflow, write:packages, delete:packages`；**无 `delete_repo`，所以删不了仓库**）。**值不要回显**。
- **HTTP + 局域网 IP 访问时 `navigator.clipboard` 根本不存在（已修）**：浏览器只在**安全上下文**（HTTPS 或 localhost）提供剪贴板 API。本次部署是 `http://192.168.123.90:13000` → `isSecureContext=false`、`navigator.clipboard === undefined`，于是**全应用 5 处复制**（接入命令 / 注册凭据 / 用户 Token / API Token / 敏感值）在真机**全部失效**，而提示还是误导性的「请检查浏览器剪贴板权限」（不是权限问题）。已加 `dashboard/src/utils/clipboard.ts::copyText()`：优先异步剪贴板 API，失败回退 `textarea + document.execCommand('copy')`（HTTP 下可用，但**必须由真实用户手势触发**），两者都失败才提示手动复制。**真机验证**：在真实 origin 上真实点击 → 出现成功提示；再用 CDP 发 Ctrl+V 粘回输入框，内容与页面显示的命令**逐字一致**（471 B）。

## 验收方法论教训（本项目实测踩到）

- **验「API 能登录」≠ 验「用户能登录」**：`curl POST /api/v1/auth/login` 返回 200 曾让我误判登录可用；而 UI 表单从 M4 起就是哑的（缺 `:model`），一路躲过所有验收。
- **验收必须在「用户实际访问的 origin」上做，localhost 会骗你**：`127.0.0.1` / `localhost` 属**安全上下文**，而 `http://<局域网IP>` 不是——两者浏览器能力不同（前者有 `navigator.clipboard`，后者 `undefined`）。我曾用 127.0.0.1 验过「复制命令可用」（绿），真机 HTTP 下却是全线失效。同类受影响的还有 `crypto.subtle`、Service Worker、摄像头/麦克风/地理定位。**规矩：凡涉这些 API，先读 `window.isSecureContext`，并一律加非安全上下文的兜底。**
- **截图脚本注入 token 会掩盖坏掉表单**：V1F 那 18 张「已登录」截图是往 localStorage 写 token 拿到的，没走表单。凡交付含交互（表单/按钮/参数提交），验收必须**真点一遍**。
- **权威网络证据用 `performance.getEntriesByType('resource')`**（页面内 patch fetch/XHR 可能被绕过）：点击后看有无**新增** `/api/` 条目。
- 复验「登录后」而不碰真密码：`curl` 取 token → 注入 localStorage（键 `neilico.access_token/refresh_token/user/remember`）→ 断言 URL 不回落 `/login` + 已鉴权 API 全通 + 侧栏渲染。
- 默认浏览器视口可能只有 800×479：按 `getBoundingClientRect()` 算的坐标常落在视口外，`elementFromPoint` 返回 null；且 `overflow:auto` 在内容容器上时 `window.scrollTo` 无效、`documentElement.scrollHeight` 也不变——**别据此报「按钮被裁掉」的假 bug**（本机 820px 断点已有 `display:block; overflow:auto`）。

## V1-S 传输安全记录

内置 CA、API TLS/mTLS、代理 HTTPS/HSTS/HTTPS 上游、NPS crypt/compress、WireGuard PSK 已实现；默认值保持关闭或 http。端到端加密仅 WireGuard 保证，relay 未实现，NPS crypt 不是端到端 AEAD，`internal_ip` 上游仍可能明文。

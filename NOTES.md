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
| **首次登入注册 + 账号管理** | ✅ | （未提交） | `GET /api/v1/setup/status`、`POST /api/v1/setup/register`（无账号可注册，已有账号 409）；`GET /api/v1/account`、`PUT /api/v1/account/email`、`POST /api/v1/account/password/rotate`；轮换后 token_version+1 使旧 refresh token 失效，并把新密码**原子回写** `NEILICO_BOOTSTRAP_ADMIN_PASSWORD`（键不变）；前端 `/register` + `/account`。详见 README「首次登入注册与账号管理」 |

### 常驻部署（生产用 Docker 目录那份；2026-10-03 改为**单容器**）
> **仓库内 `deploy/docker-compose/`（6 容器：postgres/redis/nats/control-api/dashboard/relay）仅供历史上的测试栈**（项目名 `neilico-m5`、命名卷、固定端口），**不是现行形态**。
> **现行形态 = `deploy/allinone/`（单容器）**，由 `$HOME/Documents/Docker/docker-compose.neilico.yaml`（顶层 `name: neilico`）常驻，按本机目录约定维护。

- 形态：**1 个容器 `neilico`** = PostgreSQL 16 + 控制面 API + 内置反代 + Dashboard 静态资源（Go 二进制直接服务前端，**同源、无 nginx**）
- 部署文件：`$HOME/Documents/Docker/docker-compose.neilico.yaml`
  （`build.context` = 仓库根 `$HOME/Documents/Projects/neilico` 绝对路径，`dockerfile: deploy/allinone/Dockerfile`，`image: neilico-allinone:local`）
- 持久数据：`$HOME/Documents/Docker/data/neilico/pg`（宿主目录绑定到容器 `/var/lib/postgresql/data`）
- 秘密：`$HOME/Documents/Docker/data/neilico/neilico.env`（mode 600，非仓库；管理员邮箱 `admin@neilico.local`）
  查看管理员密码（值不入文档）：`$HOME/Documents/Docker/data/neilico/show-admin-password.sh`
- 端口：`13000 -> 8080`（Dashboard + API **同端口/同源**，LAN）、`18081 -> 8081`（内置反代，LAN）；PostgreSQL **只在容器内**（不映射宿主）
- `restart: unless-stopped`；TLS/mTLS 与 ACME 本轮关闭
- 启停：`cd $HOME/Documents/Docker && docker compose -f docker-compose.neilico.yaml up -d|stop|restart`
  （源码更新后 `up -d --build` 重建镜像；**禁止 `down -v`**）
- **已移除的 3 个无用容器及理由**：
  - `redis` —— 代码里**零引用**（纯装饰容器，REDIS 相关配置项无任何调用点）
  - `nats` —— 代码里**零引用**（纯装饰容器）
  - `relay` —— wg-easy **占位**，无 NEILICO relay 数据面（TURN 3478 从未实现）；V2 实现真实中继后再加独立进程
  （`nginx` 也一并去掉：其唯一作用是静态托管 + 反代，现由 Go 二进制同源直接提供）
- Homepage 导航卡片：`$HOME/Documents/Docker/data/homepage/config/services.yaml` 的 `- 业务:` 组
  `neilico`（原 umpp 卡片原地改名），href `http://192.168.1.10:13000`，container `neilico`
- **2026-10-03 实测（单容器）**：`docker ps` 恰好 1 行 `neilico (healthy)`；
  容器内同时有 `postgres` 与 `neilico-control` 两个进程；LAN `13000` → **200** 且 body 含 `<div id="app"`；
  `/healthz` = `{"db":"up","status":"ok","version":"v1-single-20261003"}`；`/metrics` **67 条 `neilico_*`**（2026-10-03 19:1x 实测，随 V1-R2/R4 增长）；
  `POST /api/v1/auth/login` → **200 + token（长度 443）**，错密码 → **401**；带 token `GET /api/v1/nodes` → 200；
  深链 `/nodes` `/certificates` `/pki` `/alerts` → 200、`/nope.js` → 404；
  `docker restart` 后仍 healthy 且可登入（日志走「已有数据目录」分支、无 `already exists`）；
  `docker stop -t 30` → 退出码 **0**、日志含 `shutdown complete`、无 recovery 痕迹。

> 旧的常驻 6 容器 UMPP 栈（项目名 `umpp`）已于同日下线，数据改名保留在
> `$HOME/Documents/Docker/data/umpp-legacy-20261003/`（取证：业务表全空，仅 bootstrap 管理员+租户+6 条登录审计）。

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
- 「远程桌面」（自建 RustDesk）：`GET/PUT /api/v1/remote-desktop/config`、`GET /devices`、`GET /status`（`PUT` 仅 `platform_admin`）。**控制面只读公钥** `NEILICO_RD_PUBLIC_KEY_FILE`（默认 `.../rustdesk/id_ed25519.pub`），接口只返回 `public_key`，私钥任何路径都不读不回显；公钥缺失时返回 `available:false` + `hint`，不报 500。参数 `NEILICO_RD_ID_SERVER/RELAY_SERVER/PORTS` 可覆盖；`PUT` 改动**仅进程内生效**（未落库）。设备的 RustDesk ID 目前靠节点 tag `rustdesk:<id>`；页面见 `dashboard/src/pages/remote-desktop/`，文档 `docs/REMOTE_DESKTOP.md`。

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
- **compose 与 `docker run` 的卷语义不同（本机 agent 部署踩到）**：`docker run -v 名字:/路径` 会自动创建命名卷并隐式声明；**compose 必须在顶层写 `volumes:`**，否则报 `service "..." refers to undefined volume ...: invalid compose project`。本机 agent 的 compose 用**绑定挂载** `./data/neilico-agent` 绕开该坑，见 `$HOME/Documents/Docker/docker-compose.neilico-agent.yaml`（也用 `docker-compose.neilico.yaml` 定义控制面）。
- **设备列表曾需手动刷新才看到新设备（已加自动刷新）**：`NodesPage` 现在每 **10s** 静默轮询（`load({silent:true})`：不显示 loading、失败不覆盖内容），页面不可见时**暂停**、切回前台**立即刷一次**；对比前后列表**发现新设备时弹提示**（`发现 N 台新设备：xxx`）；页头有开关（默认开）+ 上次刷新时间。另外「接入设备」弹窗在展示命令期间每 4s 探测一次，设备一 enroll 就在弹窗里直接显示**成功横幅**（设备名/平台/虚拟IP）并 `emit('enrolled')` 让列表立刻刷新——**无需关窗、无需 F5**。
  - 验收（真实浏览器 + 真实设备）：页面零操作下 4 台 → 5 台自动更新，并抓到提示 `发现 1 台新设备：autorefresh-check-3`；轮询请求数按 10s 递增。
  - **测这类"自动刷新"要小心**：无头浏览器里 `document.visibilityState === 'hidden'`，我的"不可见则暂停"逻辑会正确地不发请求 → 看起来像"没生效"。必须先用 CDP `Page.bringToFront` 让页面变 visible 再测；且**时序要可控**（先开观察窗口、再让设备延迟 enroll），否则提示（3 秒）会在两次工具调用的空隙里弹出又消失，误判成"没提示"。
- **Docker 接入命令的镜像地址必须是「目标机真能拉到」的**：命令历史上写的是本机 tag `neilico-agent:local`，别的机器执行会得到 `pull access denied for neilico-agent, repository does not exist`（实测）。现已由**公开仓库 `aceneil/neilico-agent`** 的 GitHub Actions 构建发布为 **public 包**；地址可配（`enroll.agent_image` / `NEILICO_ENROLL_AGENT_IMAGE`，代码默认值见 `config.DefaultAgentImage`）。回归测试 `internal/api/enroll_commands_test.go` **独立断言不含 `neilico-agent:local`**。
- **🔴 预共享密钥曾用 base64url 生成，导致整个网络的 mesh 起不来（已修 `9a88740`）**：`generateNetworkSecret()` 用的是 `base64.RawURLEncoding`（43 字符、含 `-`/`_`、无 `=` 填充），而**WireGuard 只接受标准 base64**；由于 PSK 会写进每个 peer，**该网络下所有节点**的 `wg setconf` 都失败：`failed to parse base64-encoded key: illegal base64 data at input byte 20`（真机实测：agent 每 30s 重试一次、从未成功，宿主 `wg0` 一直没有地址，节点看似"成员"实际不通）。修法：改用 `base64.StdEncoding`；新增 `mesh.NormalizeKey()` 兼容存量 base64url 数据（**无需数据迁移**）；配置下发与渲染两处都过一遍规整。回归测试：`wireguard/keys_encoding_test.go` **逐行校验渲染出的每个密钥都必须是标准 base64/32 字节**（这正是当初缺的闸门，变异验证：还原 bug 版即红）、`mesh/keys_test.go`、`service/networks_secret_test.go`；golden 同步更新。**教训：凡把密钥写进 wg 配置，必须用 wg 自己的解析器或等价的严格 base64 校验兜底。**
- **跨机 mesh 连不通的第二个真因：同内网却用公网/代理地址互拨（已修：同内网优先内网地址）**：agent 会上报 `ip:port` 公网地址，但在 NAT/代理出口后往往互相不可达——实测本机出口是**云代理 IP**（收不到入站 UDP），NAS 那台**连公网地址都探测不到**（`public_endpoint` 一直为空），于是双方都无法主动建连、隧道永远不握手。修法：agent 用 `net.Interfaces()` 上报本机内网地址（CIDR）与**自己的监听端口**（`local_addresses` / `listen_port`，存 `nodes` 表），控制面随 peer 下发；agent 应用前若发现对端地址落在**本机某个内网网段**内，就把该 peer 的 endpoint 换成 `<对端内网IP>:<对端端口>`（`mesh.PreferLANEndpoints`，结构化 peers 与渲染文本同时改）。实测（dry-run 打印出的真实配置）：三个同内网对端分别写成 `192.168.1.10:51820 / :51830 / :51840`（端口各用对端自己的 ✓），跨网对端与未上报地址的对端**保持原样**。
  - **采集白名单要收紧**：初版 `LocalPrefixes()` 把宿主机上 10 个 docker 网桥 + 隧道自身 VIP 都收了进来，**8 条配额被吃光、真正的 `192.168.1.10/24` 被挤掉**（真机实测）。现在跳过容器/虚拟接口名前缀（`docker*`/`br-*`/`veth*`/`virbr*`/`tun*`/`tap*`/`tailscale*`）与 **CGNAT `100.64.0.0/10`**（那是 NEILICO 自己的虚拟 IP 段，把它当内网地址去拨必然失败），并排除自己的隧道接口。
  - **上报要能"变化即报"**：只按公网地址变化判断会等满 5 分钟节流窗口；现已把上次上报的内网地址列表存进 state 一起比较（换网络/换网段立刻恢复直连）。
  - 未完成：**跨两台真机的完整握手验证**需要 NAS 侧 agent 更新到新镜像（NAS 现为旧二进制，不上报内网地址）；同宿主跑两个 agent 会争抢同一网络栈（`ip link set ...: Address in use`），**属测试环境伪影，不是产品缺陷**（真实部署一台机器一个 agent）。
- **🔴 mesh 数据面不通的两个真缺陷（已修 `a4d8412` / `07e956e`）**——修完上面两条后握手成功了，但**一个字节数据都过不去**：
  - **① 缺"对端 AllowedIPs → 隧道接口"的路由**：WireGuard 的 `AllowedIPs` **只做加密路由**（决定这个包发给哪个 peer），**不会写入内核主路由表**——这正是 `wg-quick` 在 `setconf` 之后还要逐条执行 `ip route add <AllowedIPs> dev <iface>` 的原因。agent 只做了 `wg setconf`，于是发往对端虚拟 IP / 子网路由网段的包按默认路由走物理网卡。**真机症状**：双方 `latest handshake` 非 0、`transfer` 的**发送计数持续增长**，但**接收计数不动**（回包收不到），`ip route get 100.64.0.2` 指向局域网网关而不是 `wg0`。**修复**：新增 `mesh.PeerRouteCommands`（对每个对端的每个 AllowedIPs 生成 `ip route replace <cidr> dev <iface>`，幂等、去重），`ShellApplier` 的计划里包含这些命令（dry-run 可见）且应用顺序固定为 **建链/配地址 → wg setconf → 加路由**，`WGCtrlApplier` 配置设备后同样补路由。回归测试 `mesh/routes_test.go`（含顺序断言），**变异验证**：去掉路由命令即失败。
  - **② 升级 agent 后跳过应用，新增的本地动作永远装不上**：`Reconcile` 在 `AppliedConfigHash` 未变时直接返回，而 `pollOnce` 还有一道更早的短路（`result.Delivery.Version <= identity.AppliedVersion` 就 `return nil`，根本走不到 `Reconcile`）。于是**服务端配置没变时，升级 agent 不会触发应用**——真机踩到：换到带路由修复的镜像后，`wg0` 上依然一条路由都没有。**修复**：把 `mesh.ApplicationSchemaVersion`（本地应用逻辑版本）纳入 `ConfigHash`，并在 state 里记录 `application_schema`，与二进制常量不一致时**强制拉取最新配置并重新应用**，成功应用后写回。**规则：凡改动 applier/本地应用行为，必须把 `ApplicationSchemaVersion` +1**（否则升级不生效）。回归测试 `mesh/hash_schema_test.go` + `cmd/agent/schema_reapply_test.go`。
  - 端到端验证口径（本机侧）：路由出现 `100.64.0.2 scope link`、`ip route get 100.64.0.2` → `dev wg0 src 100.64.0.3`、日志出现 `local application schema changed; re-applying configuration (applied_schema=0 agent_schema=3)`；此时对接 NAS 的包**确实进了隧道**（`transfer` 发送计数从 244B 涨到 1204B），而接收计数不变——因为对端还是旧 agent、没有回程路由。
  - 附带发现的运维要点：**agent 升级后必须能看到"重新应用"的日志**，否则很可能被这两道短路静默跳过（这也是为什么"容器 healthy + 节点 online"完全不能证明数据面可用）。
- **🌐 NAS 节点（`nas-1`, 192.168.1.20）实际打通记录与三个运维坑**（2026-10-04，manager 亲自进场操作）：
  - **打通结果**（穿隧道的真实流量，非握手计数）：本机 → NAS `ping 100.64.0.2` 1.4ms、`http://100.64.0.2:9100/metrics` HTTP 200；NAS → 本机 `http://100.64.0.3:9100/metrics` HTTP 200、容器内 `ping 100.64.0.3` 1.9ms；双方 `wg show` 握手 1 秒前、收发计数持续增长。四个修复在同一次重建后**同时生效**：日志里能逐条对上 `local application schema changed; re-applying configuration (0 → 3)`、`mesh endpoint preference: 改用内网地址 192.168.1.10:51820`、宿主 `ip route` 出现 `100.64.0.3 scope link`。
  - **坑①：`:latest` 标签更新了 ≠ 容器换了镜像**。该机 `docker image inspect ...:latest` 早就是含全部修复的新镜像，但容器跑的是**3 小时前的旧镜像**（compose 没真正重建）。判定口径：比 `docker inspect <ctr> --format '{{.Image}}'` 与 `:latest` 的 ID，以及看 `StartedAt` 有没有变——**"我更新了"必须以容器镜像 ID 变化为准，不能只看 tag/pull 输出**。
  - **坑②：compose 项目名不一致会静默换卷（丢凭据）**。容器标签里的项目名是 `neilco`，而仓库里的 `docker-compose.yml` 顶部写着 `name: neilico-network` → 直接在该目录 `docker compose up` 会按新项目名解析，生成**空卷** `neilico-network_neilico-agent-state`（那次侥幸因容器名冲突失败才没造成损失）。正确做法：`docker compose -p neilco up -d --force-recreate` 显式复用原项目名与原卷（`neilco_neilico-agent-state`）；或把文件里的 `name:` 改成 `neilco`。**凡涉及卷的 compose 操作，动手前先 `docker inspect <ctr> --format '{{range .Mounts}}{{.Name}}{{end}}'` 确认卷名。**
  - **坑③：容器重启会把 wg0 删掉，而旧 agent 因"哈希未变"不再重建它**。agent 优雅退出会 `ip link delete wg0`，重启后若配置哈希/版本判定为"已应用"，旧版 agent 直接跳过应用 → **NAS 上连一个 wg 接口都没有**、`ss -uln` 里也没有 51820 监听（这正是 `ApplicationSchemaVersion` 修复要解决的现象）。诊断顺序：`ip -brief addr show wg0` → `ss -uln | grep 51820` → `docker inspect --format '{{.Config.Image}} {{.Image}}'`。
  - **现场手法**（可复用）：NAS 出网受限、拉不到 ghcr 时，用本机 `docker save | gzip` + 临时 `python3 -m http.server` + NAS 上 `curl | gunzip | docker load` 灌镜像（实测 11MB、HTTP 200、sha256 两侧一致）；只读盘点用 `docker exec` 看容器内 netns（`wg show`/`ip route`/state.json），宿主侧 `ip` 命令在容器为 host 网络时才等价。**改动机器前先 `docker inspect <ctr> > ~/backup/<ctr>-$(date).json` 留回滚点。**
  - **权限**：该机 sshd 对 root 要求 `publickey`+`password` 双因素（纯公钥登录不可能成功 ✗），`neil` 用户加公钥后可登录且已在 `docker` 组（`docker` 免 sudo）。**密码一律不入对话、不由助手代输**；需要提权时用 docker 组或由用户亲手执行。
  - **命令形态**：**单条 `docker run`**（隐式拉取）。曾短暂加过显式 `docker pull … && \` 前缀（为了让"往哪拉"一目了然），后按用户要求去掉——"镜像来源"改由界面单独标注（响应字段 `agent_image` → Docker 页签顶部的「镜像来源」行）。
  - **公开包的机制（已验证）**：GitHub 规则是**包继承发布它时所用仓库的可见性**——用 `GITHUB_TOKEN` 从**公开仓库**发布 → 包自动 public。用户命名空间下的容器包**无法用 REST API 改可见性**（`PATCH /user/packages/container/<name>` 对私有和已公开的包一律 404，实测含对照包），只能网页 UI 或走"公开仓库发布"。
  - **发布流程**：改 `agent/` 代码 → 同步到公开仓库（`/tmp/public-repo` 的组装方式：`agent/` 全量 + `deploy/agent/Dockerfile` + `control-plane/{go.mod,go.sum,pkg/capabilities,pkg/enrolltoken,testkit}`）→ push 即触发 `.github/workflows/publish-agent.yml` 构建推送（用仓库自带 token，**公开库内不存任何密钥**）。
  - **推私有包（备用）**：`docker tag neilico-agent:local ghcr.io/aceneil/neilico-agent:latest && docker push …`。凭据在 `~/.hermes/.env` 的 `GITHUB_TOKEN`（classic PAT，账号 `aceneil`，scopes `repo, workflow, write:packages, delete:packages`；**无 `delete_repo`，所以删不了仓库**）。**值不要回显**。
- **HTTP + 局域网 IP 访问时 `navigator.clipboard` 根本不存在（已修）**：浏览器只在**安全上下文**（HTTPS 或 localhost）提供剪贴板 API。本次部署是 `http://192.168.1.10:13000` → `isSecureContext=false`、`navigator.clipboard === undefined`，于是**全应用 5 处复制**（接入命令 / 注册凭据 / 用户 Token / API Token / 敏感值）在真机**全部失效**，而提示还是误导性的「请检查浏览器剪贴板权限」（不是权限问题）。已加 `dashboard/src/utils/clipboard.ts::copyText()`：优先异步剪贴板 API，失败回退 `textarea + document.execCommand('copy')`（HTTP 下可用，但**必须由真实用户手势触发**），两者都失败才提示手动复制。**真机验证**：在真实 origin 上真实点击 → 出现成功提示；再用 CDP 发 Ctrl+V 粘回输入框，内容与页面显示的命令**逐字一致**（471 B）。

- **轮换密码回写 bootstrap env 的「三重约束」（mode 600 + 宿主可读 + 容器可写）**：控制面要把新密码写回宿主 `data/neilico/neilico.env`（`NEILICO_BOOTSTRAP_ENV_FILE=/opt/neilico/bootstrap.env`，compose 以 rw 挂载）。写入用「同目录临时文件 + rename」并固定 mode 600，因此**需要对该文件所在目录有写权限**。容器里控制面默认以 `neilico` 用户运行（uid≠宿主 1000），既写不了目录也会把文件 owner 改掉、破坏宿主 `show-admin-password.sh`。修法：`entrypoint.sh` 启动前用 `stat -c %u` 读该文件属主，**以其 uid 运行控制面**（必要时 `adduser -u <uid>` 补一个运行用户），从而写入成功且 owner/mode 不变；未挂载或属主为 root 时回落 `neilico`。回写失败时**轮换本身仍成功**（DB 为准），响应 `env_file_updated=false` 并只记警告——绝不因权限问题把改密判成失败，也绝不把明文写进日志。新增 `internal/bootstrapenv`（原子写 + 单测）与 `service/account.go`。
- **给 `users` 加 `token_version` 列必须带 `default`**（AutoMigrate 给存量表加 NOT NULL 列的既有教训）：tag 写成 `not null;default:0`，存量 token 版本 0 与新值一致 → 升级不会把所有人踢下线；改密时 `token_version+1` 才让旧 refresh token 失效（JWT 无状态，靠 claims 里的版本号比对）。

## 验收方法论教训（本项目实测踩到）

- **验「API 能登录」≠ 验「用户能登录」**：`curl POST /api/v1/auth/login` 返回 200 曾让我误判登录可用；而 UI 表单从 M4 起就是哑的（缺 `:model`），一路躲过所有验收。
- **验收必须在「用户实际访问的 origin」上做，localhost 会骗你**：`127.0.0.1` / `localhost` 属**安全上下文**，而 `http://<局域网IP>` 不是——两者浏览器能力不同（前者有 `navigator.clipboard`，后者 `undefined`）。我曾用 127.0.0.1 验过「复制命令可用」（绿），真机 HTTP 下却是全线失效。同类受影响的还有 `crypto.subtle`、Service Worker、摄像头/麦克风/地理定位。**规矩：凡涉这些 API，先读 `window.isSecureContext`，并一律加非安全上下文的兜底。**
- **截图脚本注入 token 会掩盖坏掉表单**：V1F 那 18 张「已登录」截图是往 localStorage 写 token 拿到的，没走表单。凡交付含交互（表单/按钮/参数提交），验收必须**真点一遍**。
- **权威网络证据用 `performance.getEntriesByType('resource')`**（页面内 patch fetch/XHR 可能被绕过）：点击后看有无**新增** `/api/` 条目。
- 复验「登录后」而不碰真密码：`curl` 取 token → 注入 localStorage（键 `neilico.access_token/refresh_token/user/remember`）→ 断言 URL 不回落 `/login` + 已鉴权 API 全通 + 侧栏渲染。
- 默认浏览器视口可能只有 800×479：按 `getBoundingClientRect()` 算的坐标常落在视口外，`elementFromPoint` 返回 null；且 `overflow:auto` 在内容容器上时 `window.scrollTo` 无效、`documentElement.scrollHeight` 也不变——**别据此报「按钮被裁掉」的假 bug**（本机 820px 断点已有 `display:block; overflow:auto`）。

## V1-S 传输安全记录

内置 CA、API TLS/mTLS、代理 HTTPS/HSTS/HTTPS 上游、NPS crypt/compress、WireGuard PSK 已实现；默认值保持关闭或 http。端到端加密仅 WireGuard 保证，relay 未实现，NPS crypt 不是端到端 AEAD，`internal_ip` 上游仍可能明文。

## 端口转发（Stream：任意 TCP/UDP 的地址:端口打通）— 2026-10-05 交付

- **定位**：与域名反代并列的第二条通路。反代管 **HTTP/HTTPS**；端口转发管 **任意 TCP/UDP**（SSH、数据库、游戏服、DNS）。只发布显式配置的那一个端口，**不暴露整个虚拟网络**（这是用户明确要求的形态，参考 Nginx Proxy Manager 的 Streams）。
- **代码落点**：`models.StreamRule`（(protocol, listen_port) 唯一）→ `validation/stream.go` → **`service/proxy/stream.go`（转发引擎：TCP 半关闭语义 + UDP 会话表/空闲回收 + 来源白名单 + 连接/字节统计）** → `service/stream_rules.go`（CRUD + node→虚拟IP 解析）→ `api/streams.go`（CRUD + `ReconcileStreams`）→ 前端 `pages/streams/StreamsPage.vue`（导航由路由 meta 自动生成）。
- **"保存即生效"**：规则变更后在 API 层直接 `ReconcileStreams`（不像节点配置那样等轮询），单条规则启动失败（端口被占等）只影响它自己，状态与原因在列表里直接展示。
- **端口发布模型（关键约束）**：监听器在容器内，**端口必须在 compose 里发布出去**，否则"运行中却不可达"。compose 里 `NEILICO_STREAM_PORT_MIN/MAX` 与 `ports` 的段**必须一致**；本机 userland-proxy 开启，每个发布端口多一个 docker-proxy 进程（约 2MiB），故默认 20 个，加宽要成对改。默认段 `20000-20019`。
- **取舍记录**：TCP 转发用 `countingWriter` 包一层来做**实时**字节统计，代价是失去 `io.Copy` 的 splice 零拷贝快路径。判断依据：入口带宽受公网链路限制，用户态拷贝的数 GB/s 远高于链路带宽，可观测性更值钱；若将来更看重裸吞吐，改回原始 `io.Copy` 并把统计挪到连接结束即可（代码里有注释）。

## 反代性能修复（2026-10-05）

- **症状**：自研内置反代压测只有 **2.5k RPS**，而 nginx 同条件 9.3k；并发 200 时 p99 1.16s、失败 146 次。**关键判据：压测期间容器 CPU 仅 8.8%** → 不是算力/语言上限，是卡在等待。
- **根因**：`ServeHTTP` 里**每请求新建 `httputil.ReverseProxy`** → 上游连接池退化成 `http.DefaultTransport` 的 `MaxIdleConnsPerHost=2` → 并发下几乎每请求重新建连。
- **修复**：按规则缓存 ReverseProxy（Reload 时 epoch 失效）+ 共享调优连接池（`MaxIdleConnsPerHost=512`）+ Director 改为从 `req` 自身取 Host/Proto（复用实例不能闭包捕获请求级数据）。结果 **7.8k RPS、p99 90ms、失败 0**；去掉客户端跨容器那跳 NAT 后 **9.6k，已超 nginx 8.8k**；p99 仍略逊（67ms vs 52ms）。
- **对比基架已入库**：`scripts/bench/`（server/client/nginx conf/README 含实测基线表）。**注意对比必须注明网络路径**：nginx 走 host 网络、控制面容器是 bridge，客户端跨容器时每请求多一跳 NAT。
- **结论**：为性能引入 nginx **不必要**（内存也省不下什么：nginx 常驻约 10MiB、每千连接约 10MiB；NPM 那套大头是 Node UI 而非 nginx）。要 HTTP/3 / 静态大文件 / 十万级并发时，再按 NPM 路子加可选 provider（`kind=nginx`，provider 抽象已就位）。

## 新增验收/操作教训

- **改 agent 配置快照的字段，必须同时更新 config 包 golden 并跑该包测试**：Mesh「同内网直连」轮次给 peer 加了 `listen_port`，`internal/service/config/testdata/node-config.golden.json` 没同步 → `TestNodeConfigSnapshotGoldenAndDeterministic` **静默失败**了很久（该轮之后没跑全量）。修法：`UPDATE_GOLDEN=1 go test -run TestNodeConfigSnapshotGoldenAndDeterministic ./internal/service/config/`（测试自带机制）。**判定"是不是我改坏的"用 `git stash` 对照跑**，别猜。
- **`pkill -f /tmp/xxx` 会匹配到自己的命令行**（命令行里含该字符串）→ 自己的 shell 被 SIGTERM 打断，后续命令全不执行。用 `pkill -x <进程名>`（按精确进程名）或 `pkill -f '^/path$'`。
- **容器内跑 Go 二进制前先 `CGO_ENABLED=0` 静态编译**：动态链接的二进制在 alpine 里报 `exec: no such file or directory`（缺 glibc 加载器），看着像丢文件，其实是链接方式。
- **多行测试失败信息（`t.Fatalf`）会被统一缩进**：想从输出里提取 JSON 做逐字节比对会被缩进骗到——优先用测试自带的 golden 更新开关，而不是手工解析输出。

## 首次注册 + 账号管理 交付与三处坑（2026-10-06）

- **功能**：`GET /api/v1/setup/status`、`POST /api/v1/setup/register`（无账号时可建首个管理员，已有则 409 already_initialized）、登入后 `GET /api/v1/account`、`PUT /api/v1/account/email`、`POST /api/v1/account/password/rotate`（均校验当前密码；改密码 `token_version+1` 使旧 refresh token 失效；均写审计）。轮换/改邮箱成功后把新值**回写** bootstrap env（`NEILICO_BOOTSTRAP_ENV_FILE`，容器里由 compose 挂载）的 `NEILICO_BOOTSTRAP_ADMIN_PASSWORD` / `_EMAIL` 键 —— **查询方式因此保持不变**（`show-admin-password.sh` 仍读同一键）。
- **坑①：单文件 bind mount 上 `rename` 必然 EBUSY**。`docker -v <宿主文件>:<容器文件>` 之后目标是个挂载点，`os.Rename` 到它报 `device or resource busy` → "临时文件 + rename"的原子回写在**这种挂载形态下永远不可能成功**。修法：rename 失败时**退化为就地重写**（`O_WRONLY|O_TRUNC` + write + sync，保留 inode/属主/mode）。另外还要把目标**目录**交给控制面运行用户（entrypoint `chown`），否则连临时文件都建不出来（`permission denied`）。
- **坑②：密码策略把自己 env 里的默认密码挡在门外**。默认是 64 位十六进制（仅数字+小写=2 类），策略要求"≥3 类字符"→ 想轮换回默认密码返回 **400**。修法：策略放开为「**≥16 字符 且（≥3 类 或 长度≥32）**」。
- **坑③：单次连接拨号失败 ≠ 规则坏了**。曾把整条端口转发规则标成 `error`（UI 误报"错误"），但监听器其实一直正常 accept、只是目标此刻不可达。改为只记 `last_error`、状态保持 `running`。诊断口径：规则 `last_error="dial tcp …: i/o timeout"` + 宿主直连目标 `000` = **环境不可达**（如 NAS 侧 wg0 掉了），不是规则配置问题。
- **操作教训：`{ … } > log` 分组里混 heredoc 会被截断**（报 `here-document … delimited by end-of-file` / `unexpected end of file`）。要跑复杂脚本就**落成文件再执行**，别塞进分组 + heredoc。
- **事故与恢复**：一次真机验收把线上管理员密码轮换成随机临时值、回写与恢复同时失败（当时策略没放开）→ 原密码登不上且临时密码从未落盘。恢复手法：用仓库自己的 `auth.HashPassword` 算出 env 原密码的 bcrypt 哈希 → `docker exec -i neilico psql` 直接更新 `users.password_hash`（**SQL 经 stdin，明文不进命令行**）→ 登录 200 恢复。**教训：改凭据的真机验收必须带 try/finally 兜底，且先用指纹比对确认能恢复再动手。**
- **复验脚本入库**：`scripts/verify_account.py`（轮换→校验回写→改回原值；全程只用 sha256 前 10 位指纹比对；带兜底恢复；绝不回显明文）。

## 自建 hbbs/hbbr 并入项目 + 按需加载（2026-10-09 真机验收）

- **不再依赖外部容器**：`rustdesk/rustdesk-server`（tag `1.1.16`）源码 **vendored** 进 `third_party/rustdesk-server/`（含 `vendor.tar.gz` 全量 crates，可**完全离线**编译；AGPL `LICENSE` 原文保留 + 根 `NOTICE` 记来源/commit/改动）。`deploy/allinone/Dockerfile` 新增 `rustdesk-build` 阶段，从 vendored 源编出 `hbbs`/`hbbr` 装进**同一个 allinone 镜像**（仍是单容器）。证据：容器内 `hbbs -h` → `hbbs 1.1.16` ✓。
- **定位（用户明确）**：**"我们只是借助它的能力"** ✓ —— 源码**保持上游原样**、不改其内部逻辑（不做 fork 式改造，升级只换 tag 重编）；"控制"体现在**部署与治理层**：何时启动、端口、密钥位置、是否启用、界面展示全在 NEILICO 侧。
- **按需生命周期**（`internal/service/rustdesk_server.go`，默认 `on_demand`）：无远程桌面活动**不启动** → 容器内 `hbbs`/`hbbr` **进程数 0**、不占 CPU/内存 ✓；有活动自动拉起；空闲超时自动停；另有 `always_on`/`off` 模式。接口 `GET /api/v1/remote-desktop/server-status`、`POST .../server/{start|stop}`（admin）。真机实测：idle `running=false`/进程 0 → start 后进程 2 且宿主 `0.0.0.0:21115/21116` 真监听、TCP 可连 → stop 后回 0 ✓✓。
  - ⚠️ **判据修正（诚实记录）**：`ports:` 发布模式下，宿主端口由 **docker-proxy** 常驻监听（6 端口 × v4/v6 = 12 条 ✓ 约 2MB/个、不占 CPU ✓）→ 所以"idle 时宿主零监听"**做不到** ✗；真正的按需收益是**服务进程不启动**（0 进程 ✓）。要连端口都不出现，只能 `network_mode: host` ✗（牺牲隔离 ✓）——按现状取舍：保留发布端口 ✓。
- **密钥**：RD 密钥目录已**绑定挂载** `data/neilico/rustdesk/`（宿主/容器指纹一致 ✓ 700/600 ✓）→ 容器重建**不再丢密钥** ✓；当前生效的是容器首次启动时**新生成**的那对（用户决定：**直接用新密钥** ✓，不做恢复；旧 `data/rustdesk/` 仅留作备份 ✓）。公钥由 `GET /api/v1/remote-desktop/config` 动态下发 ✓，界面自动跟随 ✓。
- **踩坑（重要，下次别再犯）**：**发布端口与外部同名容器冲突** → 新容器 `failed to bind host port 0.0.0.0:21115` ✗ 且留下**半残容器**（无网络、`{{.Ports}}` 空、宿主无监听 ✗）。正确顺序：**先 `docker compose -f docker-compose.rustdesk.yaml down`（不加 `-v` 以保密钥）腾出端口 → 再起新容器**；若已半残，用 `up -d --force-recreate` 修复 ✓。回滚：把外部的 `up -d` 拉回来即可 ✓。
- 已推送：`8d248df`（设备列表三处 UI 修正 ✓）+ 自建 hbbs/hbbr 相关提交 ✓，本地=远程 ✓。

## 远程桌面（RustDesk 内核 + 自建服务器）P1 + NEILICO 集成（2026-10-07 真机验收）

### 已落地
- **P1 自建 rustdesk-server**（hbbs 信令 + hbbr 中继）：compose 在 `~/Documents/Docker/docker-compose.rustdesk.yaml`，数据绑定挂载 `data/rustdesk/`（目录 700、密钥 600）。**宿主**端口 21115/tcp · 21116/tcp · 21116/udp · 21117/tcp · 21118/tcp · 21119/tcp 全部实测在监听 ✓。公钥 `eTJt8siibSbWPyj9p2sNQwbF5i6gQXICbkGPterq7oY=`（**私钥只在服务器、权限 600，任何环节都不读取** ✓）。未动 neilico / neilico-agent 容器 ✓、未改防火墙 ✓、Homepage 卡片已加 ✓。
- **NEILICO 侧集成**（提交 `4431b7c`，20 文件）：控制面 `GET/PUT /api/v1/remote-desktop/config`（未登录 401 / 非 admin 403 / 非法 400 ✓，**只下发公钥** ✓，`available` 如实反映就绪）、`GET .../devices`（节点 + `rustdesk_id` + `connect_url`）、`GET .../status`（TCP 探活 21115/21116/21117）；dashboard 侧栏新增「**远程桌面**」页（服务器参数卡 + 一键复制 + 三平台安装指引 `flatpak`/`brew`/`winget` + 设备网格 + 发起连接，未上报 ID 的设备按钮置灰并说明 ✓）；新增 `docs/REMOTE_DESKTOP.md`。
- **架构决策（已定）**：客户端走 **RustDesk 内核**（Flutter UI + `flutter_rust_bridge`，与官方同构）+ **方案②进程隔离**（内核作为独立组件，**不链接、不改其源码**）→ NEILICO 自身许可保持自由 ✓（AGPL 边界写在文档里）。
- **P1.5 公网化加固**：`hbbr` 加 `-k _`（**中继校验 Key**，公网防滥用）、`RUSTDESK_RELAY_HOST` 参数化（去掉写死的内网 IP）、镜像锁主版本 tag、NOTES 增「公网部署」节（6 个端口 + **密钥迁移铁律**：丢失或重新生成 → 所有已装客户端都要重配 Key）。

### 真机验收（我亲跑，不采信子代理自报）
- Go：`gofmt -l` 空 / `go build`·`go vet` / `go test -count=1 ./...` 全 ok；前端：`vue-tsc --noEmit` 0 错 / `npm run build` 成功；本次 20 个文件、**未碰 `DomainsPage.vue`/`StreamsPage.vue`** ✓（「域名与代理」三标签页保持合并状态 ✓）。
- 线上 `:13000`（真容器，不是临时二进制）：未登录 `/config` → **401** ✓；admin → **200 `available=true` `public_key` 长度 44** ✓；`/status` 21115·21116·21117 全 `reachable` ✓；`/devices` total=2 ✓；容器内只有 `rustdesk.pub`（44B）✓、**私钥不可读** ✓；hbbs/hbbr 仍 Up ✓。
- 线上 bundle `index-kkIXc0sR.js`，含远程桌面页 chunk `RemoteDesktopPage-3uLCN5PO.js` ✓。

### ⚠️ 踩坑（下次别再犯）
1. **`docker-compose.neilico.yaml` 的 `environment:` 是 mapping（`KEY: value`），不是 list（`- KEY=value`）** —— 插成 list 会让 `docker compose` 直接拒绝解析（幸好它拦住了，生产零影响）。
2. **服务名是 `app`**（`neilico` 只是 `container_name`）—— 写脚本前先 `yaml.safe_load` 打印结构，**别猜**。
3. **改 compose 必须先备份 + 用 YAML 解析器断言（服务名/键/卷都命中）通过后才 `up -d`** —— 第二次失败就是靠这道闸自动回滚的。
4. **公钥要「单文件 ro」挂进容器**（`data/rustdesk/id_ed25519.pub:/opt/neilico/rustdesk.pub:ro` + `NEILICO_RD_PUBLIC_KEY_FILE=/opt/neilico/rustdesk.pub`）：不挂 → `available=false`、页面显示「服务器未就绪」；**永远别把整个密钥目录挂进去**（私钥会进容器 ✗）。

## 域名反代 = NPM 式「代理主机」单步模型（2026-10-07 真机验收）

- **从「两步两表」改为 NPM Proxy Host 形态**：一张表单（域名 + 转发地址 + 转发端口，+ http/https 默认 http）**一次提交**即完成「域名 → 地址:端口」绑定；一行 = 一个主机，可单行编辑/删除。
- **新后端接口（增量扩展，旧接口签名不动）**：`POST /api/v1/proxy-hosts`（**一个事务**内建 domain + 默认 rule，返回 `domain_id` + `rule_id`；非法目标 400 且 **domain 不落盘**；重名 409）、`GET /api/v1/proxy-hosts`（域名 + 默认规则 join 成行，`{items,total,page,page_size}`）、`PUT /api/v1/proxy-hosts/:id`（`:id` = 域名 id，可同时改域名与目标）、`DELETE /api/v1/proxy-hosts/:id`（204，事务内**先删该域名全部规则再删域名** = 级联清理）。实现：`internal/service/proxy_hosts.go` 把**同一个事务句柄**注入既有 `DomainService`/`ProxyRuleService` 复用校验与落盘（不重写业务逻辑）。
- **前端照 NPM 原样（砍掉多余）**：页头「代理主机」+「搜索主机…」+ 绿色「添加代理主机」；表格 5 列 = **源**（域名 + 小字创建时间 + 状态圆点）/ **目的地**（`scheme://host:port`）/ **SSL**（无证书「仅 HTTP」，有则证书域名）/ **访问**（「公共」或「受限」）/ **状态**（● 在线 / ○ 离线）+ 操作（编辑/删除）。**已删除**：域名表·规则表双表与内层 segmented、高级/自定义开关、类型文字、提交前预览块、说明性提示段。目标类型**静默推断**（虚拟 IP 走 Mesh / 内网物理 IP / 节点 UUID）仍全支持，「从设备选择」下拉保留。保留页内三标签与 `/streams`、`/certificates` 重定向。
- **真机验收（我亲跑，不采信子代理自报）**：`gofmt -l` 空 / `go test ./internal/api` ok / `vue-tsc --noEmit` exit=0 / `npm run build` ok；部署后 bundle **`index-BBgbzfmE.js`**。`POST` 一次提交 **201**（同时返回 domain_id + rule_id）→ 立刻 `curl -H 'Host: <域名>'` 反代入口 **HTTP 200**（1833B = NAS 上 agent 指标，证明域名 → 虚拟地址:端口经 Mesh 可达）；`GET` 列表含该行（目的地 `http://100.64.0.2:9100`）；`PUT` 改物理地址 → 200 → 改后 curl 200（控制面页面）；`DELETE` → 204 → 删后 curl **404**（级联清规则）；旧接口 `POST /api/v1/domains` 仍 201（向后兼容）。
- **坑（SQLite + GORM 事务）**：事务内若用服务根连接（`s.db`）而不是传入的 `tx` 去读，在 SQLite 共享缓存下会与写事务**互锁挂死** —— 读助手必须接收 `tx`。

## 域名与代理 NPM 化 + 侧栏三合一（2026-10-06 真机验收）

- **代理规则表单改 NPM 风格**（参考 Nginx Proxy Manager：Forward Hostname/IP + Forward Port）：两框「转发地址」+「转发端口」，客户端**自动推断 target_type** —— UUID 且匹配节点 → `node`；命中当前虚拟网络成员的虚拟 IP → `virtual_ip`；其余合法 IP → `internal_ip`；界面明文标示含义（「虚拟地址（走 Mesh 隧道）」/「物理地址（内网直连）」/「节点（自动跟随其虚拟 IP）」）。含「从设备选择」下拉（选节点自动填 UUID + 端口）、「高级/自定义」双向切换不丢内容、提交前实时预览 `target`+`target_type`、具体校验（端口 1–65535 / IPv4 / UUID 不存在 / 非 IP 非 UUID）。**API 契约未变**（`target_type` + `target=host:port`，后端 0 改动）；列表新增「转发目标（地址:端口 + 类型标签）」列。
- **侧栏三合一**：原三项「域名与代理 / 端口转发 / TLS 证书」→ 只留一项「域名与代理」（`/domains`）。页内 `a-tabs` 三标签：域名反代（DomainsPage 自身 + 内层 segmented）、端口转发（StreamsPage 抽成可嵌入组件）、TLS 证书（CertificatesPage 同上，并入原域名页的手动 PEM 导入）。面板常驻、切换只隐藏不销毁，URL 随标签同步。**旧深链兼容**：`/streams`、`/certificates` 保留但 `meta.hidden: true`，重定向到 `/domains?tab=streams` / `?tab=certificates`。
- **验收（我亲跑，非采信自报）**：`vue-tsc --noEmit` exit=0；`npm run build` 成功；改动仅前端 4 文件、control-plane 0 文件；部署后线上 bundle **`index-C2OcPe3H.js`**。真机浏览器实测 `/streams` 未登录落点 = `/login?redirect=/domains?tab=streams`（**证明重定向生效**）；产物 chunk 实测含 `域名反代`/`转发地址`/`转发端口`/`从设备选择`/`虚拟地址`/`物理地址`。真机两条绑定：`vip2.demo.local` → `100.64.0.2:9100` 建规则 201 → 请求 **HTTP 200**（1830B，NAS 上 agent 指标）；`lan2.demo.local` → `192.168.1.10:13000` 建规则 201 → **HTTP 200**（454B，控制面页面）；演示域名已清理。
- 权限仍按原 `meta.roles`；TLS 标签的「导入证书」沿用 `canManageProxy`（后端本就限 admin → ops 仍 403），签发/续期/撤销/删除走 `canManageCertificates`。

## Mesh 全线不通的完整因果链与修复（2026-10-06，真机验收）

**现象**：界面显示两台设备 `online` + `mesh=ready`，但宿主无 `wg0`、虚拟 IP 全不可达、端口转发 000。

**因果链（逐层实测确认，别再只看 UI ✗）**
1. **agent 能力探测判错**：`tunnel` 只看外部客户端二进制是否存在 → 明明能建内核 WireGuard 接口（容器内 `ip link add type wireguard` 实测成功）却被自己的门禁挡住 → **不去应用 mesh 配置**。
2. **UI 掩盖故障**：capabilities 里 `mesh=ready` 与 `tunnel=unavailable` 自相矛盾，而前端只展示 `mesh`；"在线"只是**心跳**（走局域网 HTTP，与隧道无关），`last_seen` 也只存 DB 不展示。
3. **容器重启后永不重建**：recreate 会清空 netns（wg0 消失），但 agent 因 `state.json` 的 `applied_version` 未变而跳过重建（pollOnce 的 304 短路 + Reconcile 的哈希比对）—— 所以"重启 agent"治不了它 ✗。

**修复（三处，均有单测）**
- **能力探测按真实能力判定**：`Probes.CanCreateInterface` 真建再删一次性接口（探测动作与 applier 动作完全一致），失败才 `unavailable` 且把**底层真实错误**写进 reason；门禁改用 `MeshApplicable()`（mesh+tunnel+subnet 全 ready 才应用）。
- **视图诚信**：节点视图以 `tunnel` 为准（`EffectiveMesh` 取更悲观者），暴露 `effective_mesh` / `capabilities_note` / `heartbeat_stale` / `last_seen`（超心跳阈值标"陈旧"，绝不绿）。
- **本地实物校对（drift 检测）**：`agent/internal/mesh/probe.go` 在启动与每轮配置轮询前校验三项——wg0 存在、对端 peer 配置一致、对端 AllowedIPs→wg0 路由存在；声称已应用但实物缺失 → **无条件重应用**并打 `local mesh state drift detected; re-applying`（带缺失项）。`ApplicationSchemaVersion` 3→4；`state.json` 新增 `applied_peers`（仅公钥 + AllowedIPs，**无任何密钥**）。

**终局验收（真机双向）**：`ping 100.64.0.2/0.3` 双向通 ✓；`100.64.0.2:9100` → 200 ✓；NAS → `100.64.0.3:9100` → 200 ✓；**端口转发 `http://192.168.1.10:20000/metrics` → HTTP 200（内容是 NAS 上 agent 的指标）✓**。

**部署注意**：NAS 出网受限 → 用 `docker save | gzip | ssh 'gunzip | docker load'` 送镜像；重建必须 `docker compose -p neilco up -d --force-recreate`（显式复用原项目名，否则换空卷丢凭据 ✗）。**换 agent 镜像时两端都要换**（只换一端 → 单向不通 ✓）。

## Mesh 起不来的根因：agent 能力探测把 tunnel 判错（2026-10-06）

- **症状链**：agent 日志 `mesh=ready subnet_routes=ready tunnel=unavailable` + `WARN 能力不可用 capability=tunnel reason=未检测到可用的隧道/代理客户端` → agent 自认隧道不可用 → 不应用 mesh 配置 → 宿主上**永远没有 wg0** → 虚拟 IP `100.64.0.x` 全不通 → 端口转发也打不通。
- **根因**：`agent/internal/capabilities/detect.go` 把 `tunnel` 建立在「外部隧道/代理客户端二进制（`neilico-tunnel`/`npc`）是否存在」上。可 mesh 隧道是 agent **自己用内核 WireGuard 建接口**（`ip link add … type wireguard`），与那个二进制毫无关系。现场明明 `CapAdd=[CAP_NET_ADMIN]`、`/dev/net/tun` 存在、`ip link add dev wgtest type wireguard && ip link del wgtest` 成功——探测结论却是「没装客户端」，**self-report 与真实能力相反**，还一路传到控制面把故障掩盖。
- **修法**：`tunnel` 改由**真实、可回滚的探测**决定（`Probes.CanCreateInterface`，Linux 默认 = 创建再删除一次性接口 `neilico-p<pid>`）；失败时把**底层真实错误**写进 reason。没有接线真实探测时才退回静态判断（内核 WG 组件 + CAP_NET_ADMIN + /dev/net/tun）或外部客户端兼容信号。**门禁**同步：`shouldDryRun()` 用 `Capabilities.MeshApplicable()`（三项全 ready 才应用），tunnel 不可用即 dry-run——探测准了，门禁自然放行。
- **取舍**：探测放在 `Detect()`（启动 + 每次心跳），因此每个心跳周期会建/删一个一次性接口。内核 WG 建接口是毫秒级、且与 applier 的实际动作完全一致（applier 也要求 `ip link add type wireguard`，不会出现「探测过但应用挂」的错配）。若日后心跳过于频繁想省这两次 netlink，应改为缓存探测结果 + 应用前强制刷新，而不是退回静态判断。

## 节点视图不再用 mesh=ready 掩盖 tunnel 故障（2026-10-06）

- **问题**：控制面 DB 里同一 `capabilities` 对象可自相矛盾（`mesh=ready` 但 `tunnel=unavailable`），而前端只看 `mesh`，于是显示成绿色就绪把故障盖住；且「在线」只有心跳状态、没有时效依据。
- **修法**：`pkg/capabilities` 新增 `EffectiveMesh()`（取 mesh 与 tunnel 中**更悲观者**，tunnel 是权威信号）、`Contradictory()`、`Note()`、`MeshApplicable()`；`models.Node` 增 `effective_mesh`/`capabilities_note`/`heartbeat_stale`（`gorm:"-"`，读取边界由 `attachMembership` 计算）→ 节点 API 一并返回（`last_seen` 本就在返回里）。前端 `NodesPage.vue` 状态列以有效状态为准（tunnel 不可用**绝不显示绿色**）、`最后心跳` 列展示 `last_seen` 并在超时标「陈旧」，详情抽屉给出矛盾说明。
- **口径**：`heartbeat_stale` = `last_seen` 超过服务端心跳超时时间（与节点清扫器同一判据 `IsHeartbeatExpired`）。

## 域名反代改 NPM 式「代理主机」单步模型（2026-10-07）

- **形态**：域名反代的默认视图从「域名表 + 规则表两步」改为 Nginx Proxy Manager 式**一张代理主机列表 + 一张添加表单**。列表**只要 5 列**：源（域名 + 小字「创建时间: YYYY-MM-DD」+ 状态圆点）、目的地（`scheme://host:port`）、SSL（有证书显证书域名，否则「仅 HTTP」）、访问（默认「公共」，有白名单/Basic/JWT 时「受限」）、状态（● 在线 / ○ 离线）；行内编辑、删除（二次确认，级联清规则）。页头标题「代理主机」+ 搜索框（按域名或目的地过滤）+ 绿色「添加代理主机」（用 `--accent-success` 主题变量，不软编内联色）。
- **表单**：域名 + 转发地址 + 转发端口（+ scheme http/https，默认 http）。一次提交即在同一事务里同时建 `Domain` 与默认 `ProxyRule`（path `/`），失败整体回滚不留半成品。**目标类型自动推断保留但静默**：不显示类型文字、无高级/自定义开关、无提交预览块；「从设备选择」下拉可选。三种目标（虚拟 IP 走 Mesh / 内网物理 IP / 节点 UUID）全支持，复用 `validation.Target`。
- **砍掉**：原「域名表 / 规则表」双表 + 内层 segmented 高级视图、高级/自定义开关及所有说明性提示块。**保留**：页内三标签（域名反代 / 端口转发 / TLS 证书）与旧路由 `/streams`、`/certificates` 的深链重定向。
- **后端（增量，不动旧端点）**：新增 `ProxyHostService`（`internal/service/proxy_hosts.go`，把同一事务句柄注入既有的 `DomainService`/`ProxyRuleService` 复用其校验与落盘）与 `POST/GET/PUT/DELETE /api/v1/proxy-hosts`（GET 为域名+默认规则 join 的列表；`:id` = 域名 id；DELETE 先落盘配置版本再事务内先删规则后删域名，规避 `ProxyRule→Domain` 的 RESTRICT 外键 500）。旧的 `/api/v1/domains`、`/api/v1/proxy-rules` 保持兼容。
- **前线抽公共实现**：`composables/useForwardTarget.ts`（地址/端口 + 静默推断 + 校验 + 必填判定）与 `components/ProxyTargetFields.vue`（从设备选择 + 两框），表单一处实现。
- **验收（亲跑）**：`gofmt -l .` 空；`go build ./...`/`go vet ./...`/`go test ./...` 全绿；新接口集成测试 `TestProxyHostsSingleStepFlow`（一次提交同建 domain+rule、非法目标整笔回滚不落盘、域名冲突 409 不新增规则、三种目标、单行编辑改名改目标、删除级联清规则、跨租户 404/列表不泄漏）与 `TestProxyHostsBackwardCompatibleAndAdoptsLegacyDomain`（旧流程仍可用、无规则旧域名被单行编辑时补建默认规则）均 PASS；前端 `npx vue-tsc --noEmit` exit=0、`npm run build` 成功，产物含新文案且已不含「高级 / 自定义」「代理规则（」等被砍文案。**未部署**。


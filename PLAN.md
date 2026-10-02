# NEILICO 项目计划 (Project Plan)

> 上游规格：[docs/NEILICO_SPEC.md](docs/NEILICO_SPEC.md)（用户提供的总纲文档，唯一需求来源）
> 本文件是**执行计划**：里程碑拆分、验收标准、技术裁决记录。规格与本文冲突时以本文的「裁决」为准，并把裁决回写进规格附录。

## 0. 项目基线

| 项 | 值 |
| :--- | :--- |
| 项目代号 | NEILICO (Unified Mesh & Proxy Platform) |
| 仓库 | `~/Documents/Projects/neilico` |
| 代码托管 | 本地 git（推送远端见 §7） |
| 语言/工具链 | Go 1.27 / Node 26 / Docker 29 + Compose 2.40（本机已就绪） |
| 执行方式 | Codex CLI 分轮实现，Manager 逐轮验收；每轮一个 herdr workspace 内 pane |

## 1. 目标与范围

构建统一后台管理系统，三个平面：**控制面 Control Plane** / **穿透代理面 Proxy Plane** / **Mesh 组网面 SD-WAN Plane**。
本轮交付目标 = **规格 §12.1 的 MVP + 尽量多的 V1 项**，明确不做 V2（计费、QoS、多地域中继调度、OpenWrt）。

MVP 验证判据（来自规格 §12.1）：
1. 域名为入口能访问内网 Web 服务（代理链路可用）。
2. 两台 Agent 能通过虚拟 IP 互 ping（Mesh 链路可用）。
3. 控制面可管理租户/用户/节点/域名/网络，Agent 能注册+心跳+拉配置。

## 2. 技术裁决（规格内部矛盾的处理）

规格 §4.4.2 写 `React + Ant Design Pro`，§6 技术表写 `vue3 + Ant Design Pro`（Ant Design Pro 是 React 专属，两者不可同时成立）。

| 编号 | 争议点 | 裁决 | 依据 |
| :--- | :--- | :--- | :--- |
| D1 | 前端框架 | **Vue 3 + TypeScript + Vite + Ant Design Vue + ECharts** | §6 技术表明确写 vue3；Ant Design Vue 是 Ant Design 的官方 Vue 端口，兼顾两处意图；且用户既有项目栈以 Vue3 为主 |
| D2 | 数据库访问 | 生产 PostgreSQL 16（GORM）；**测试用纯 Go sqlite 驱动**（`github.com/glebarez/sqlite`）保证 hermetic | 无人值守环境不应让测试依赖 docker；纯 Go 免 cgo |
| D3 | Mesh 实现 | MVP **自研控制面生成 WireGuard 配置**（协调/密钥/peer/allowed_ips/子网路由），外部实现（EasyTier/Headscale）以 `MeshProvider` 接口适配，MVP 提供 `wireguard` + `easytier-config-export` 两个 provider | 规格 §4.3.2 的「集成 EasyTier/Headscale」需先有可被配置的对象模型；接口化才不锁死。避免一次性引入外部重型依赖导致不可运行 |
| D4 | 代理面 | MVP 控制面生成 **NPS 兼容配置**（`ProxyProvider` 接口）+ 内置 `httputil.ReverseProxy` 直连模式（用于 §12.1「域名访问内网 Web」的端到端验证） | 规格 §4.2.2「MVP 基于 nps/frp 二次开发」保留，但端到端验证不能依赖外部二进制存在 |
| D5 | 消息队列 | NATS JetStream 列为可选依赖：MVP 用进程内事件总线 + 轮询降级；接口 `EventBus` 预留 | 减少 MVP 启动面，配置下发用版本号轮询已足够 |
| D6 | 配置版本化 | `config_versions` 表 + `GET /api/v1/agent/config?node_id&version`，版本不变返回 `304`/`not_modified` | 规格 §8.5 + §16 风险「配置不一致」对策 |

## 3. 仓库结构（目标）

```
neilico/
├── docs/                 # NEILICO_SPEC.md（上游）、PLAN.md、USER_GUIDE.md、OPS.md、API.md
├── control-plane/        # Go: cmd/ internal/ migrations/ configs/
├── agent/                # Go: cmd/ internal/ configs/ Dockerfile
├── cli/                  # Go: neilicoctl
├── dashboard/            # Vue3 + Vite + Ant Design Vue
├── deploy/               # docker-compose/ 、helm/（V1）
├── scripts/              # 冒烟/验收脚本
├── NOTES.md              # 目录导览（用户约定）
└── README.md
```

## 4. 里程碑（每轮一个 Codex 轮次，独立可编译可测）

| 里程碑 | 内容 | 验收标准（硬） |
| :--- | :--- | :--- |
| **M1 控制面骨架 + 身份/节点** | Go module、config.yaml 加载、GORM 全量核心表迁移、`/healthz`、JWT 登录/刷新、RBAC 中间件、租户/用户 CRUD、节点注册/心跳/列表/删除（心跳超时 60s 判离线）、统一错误与审计日志写入 | `gofmt -l` 空、`go vet ./...` 通过、`go test ./...` 全绿（含 httptest 端到端：登录→建租户→注册节点→心跳→列表）、`CGO_ENABLED=0 go build` 成功 |
| **M2 域名/代理 + Mesh + 配置下发** | domains/certificates/proxy_rules CRUD；`ProxyProvider`(nps-config 生成 + 内置反代)；virtual_networks/network_members/acl_rules/subnet_routes CRUD；ACL 匹配引擎（默认拒绝、优先级）；`MeshProvider`(wireguard 配置生成)；`GET /api/v1/agent/config` 版本化下发 + 304/回滚；`/metrics` Prometheus | 上述全套测试全绿；ACL 匹配表驱动用例 ≥20 条；配置生成快照用例（golden file） |
| **M3 Agent + CLI** | Agent：读 agent.yaml、注册、30s 心跳、长轮询/定时拉配置、应用 WireGuard（`wgctrl` 或 shell 回退）、子网路由与 ip_forward/MASQUERADE、指标 :9100、优雅退出；Dockerfile（多阶段、静态二进制）；`neilicoctl` 登录/node list/network/domain/status | `go test ./...` 全绿；agent 与 control-plane 的**联调集成测试**（agent 用内存配置跑通注册+心跳+拉配置）；`CGO_ENABLED=0 go build` 两个二进制 |
| **M4 Dashboard** | 登录页、仪表盘（在线节点/隧道/流量/告警）、设备管理、域名与代理、虚拟网络（成员/ACL/路由）、用户与权限、日志与审计、系统设置；Axios 封装 + JWT 拦截 + 401 跳登录；ECharts 图表；WS 实时状态（可选降级轮询） | `npm ci && npm run build` 成功（零 TS 错误）；路由/菜单与规格 §4.4.1 逐条对应；组件树可构建产物 |
| **M5 部署 + 文档 + 端到端冒烟** | `deploy/docker-compose/docker-compose.yml`（postgres/redis/nats/control-api/dashboard/relay）；`.env.example`；`scripts/smoke.sh` 一键端到端（起栈→登录→建网络→注册两个 agent→虚拟 IP 互 ping 的可验证替代：控制面配置一致性断言）；README/USER_GUIDE/OPS/API 文档 | `docker compose config` 通过；`scripts/smoke.sh` 退出码 0 且打印每步断言；文档齐备 |

**轮次纪律**：同仓开发必须串行（上一轮验收通过后才派下一轮）。每轮开始前工作区必须 committing clean。

## 5. V1 路线（MVP 交付后启动，2026-10-02）

按「用户可感知价值 × 可验证性」排序，每轮一个 codex 轮次、独立可测：

| 轮次 | 主题 | 核心内容 | 硬验收 |
| :--- | :--- | :--- | :--- |
| **V1-R1** | ACME 证书自动化 | `x/crypto/acme` 签发 + HTTP-01 挑战服务 + 自动续期调度（阈值/退避/single-flight）+ 内置反代 **SNI TLS 终结** + 证书状态字段与指标 | hermetic 单测全绿 **且** `scripts/smoke-acme.sh` 用 **Pebble**（真 ACME 协议）签发出证书、SNI 握手取到该证书 |
| **V1-R2** | 告警体系（§15.2） | 5 条规则（节点离线/证书将到期/P2P 成功率/中继流量突增/配置下发失败）+ 状态机与 `alert_events` + `/api/v1/alerts` 系列 + webhook 通知 + Dashboard 告警卡片与 `/alerts` 页 | 规则逐条边界单测 + 状态机去重/恢复测试 + 无数据源必须 `insufficient_data`（**禁止编造**） |
| **V1-R3** | Helm Chart（§11.2） | `deploy/helm/neilico/`（control-api/dashboard + 可选 postgres/redis/nats/relay）+ `values.schema.json` + `ci/verify.sh` | `helm lint` + 两套 `helm template` + 断言：必需 Kind 齐备、渲染产物**无明文密钥**、非法 values 必须失败 |
| **V1-R4** | API Token / Scope / 限流（§10.1、§4.1.2） | `api_tokens` 表（只存哈希 + prefix）+ `neilico_` 前缀令牌 + scope 授权（角色→scope 映射表）+ 租户级令牌 CRUD/轮换 + 按令牌维度令牌桶限流 + CLI & Dashboard 接通 | 令牌三态错误码、**不泄露明文**断言、scope 矩阵、**API Token 不得自我提权**、限流隔离与豁免、readonly 回归 |

**执行方式**：沿用项目已沉淀的自主驱动器（systemd 定时器 + 机械验收 + 自动派下一轮 + 桌面/飞书双通道通知），轮次链 `M5 → V1R1 → V1R2 → V1R3 → V1R4 → DONE`；需要 docker/helm 的轮次通过 `~/.hermes/cache/neilico-<ROUND>-accept.sh` 挂附加验收。

**仍然不做（V2）**：商业化计费、Exit Node、DNS 解析、插件系统、高可用控制面、多地域中继调度、流量工程/QoS、OpenWrt、OAuth2 第三方登录、ClickHouse 访问日志、Windows/macOS Agent。

## 6. 验收与测试计划（规格 §14 落地）
- 单元：配置生成、ACL 匹配、子网路由计算、Agent 配置解析、JWT。
- 集成：登录→建网络→注册节点→下发配置→断言 peer/allowed_ips；域名→代理→HTTP 回显。
- 性能（可选/记录）：1000 节点心跳压测脚本、10000 代理规则配置生成耗时。
- 安全：未授权 401、越权跨租户 403、JWT 伪造、ACL 绕过。

## 7. 交付与留痕
- 每轮产物：`git commit`（Manager 复验后提交）+ `/tmp/neilico-<Mx>-report.md` 报告 + `scripts/` 下可复跑断言。
- 远端推送：若用户提供凭据/远端仓库则推送（SSH 需远端库已存在）。默认本地提交。

## 8. 风险
见规格 §16。执行侧新增风险：① 单轮任务过长会被云端通道中断 → 已按里程碑切分，每轮 ≤ 目标 30 分钟工作量；② 单仓并行写冲突 → 严格串行；③ 外部依赖（NPS/EasyTier 镜像）不可得 → 一律走接口 + 内置实现的降级路径，验收不依赖外部二进制。

## 9. 执行偏差记录（实现期追加，回写规格未覆盖之处）

| # | 偏差 | 原因 | 影响 |
| :--- | :--- | :--- | :--- |
| E1 | 规格 §8.2 的注册请求含 `public_key`，**实际实现改为控制面用 `wgtypes` 生成密钥对**，注册响应一次性返回 `private_key`，agent 不再上传公钥 | 自研 Mesh 需要控制面持有密钥以生成 peer 配置；由 agent 生成会导致控制面无法在节点离线时轮换密钥 | Agent（M3）按新契约实现；`docs/API.md`（M5）须反映 |
| E2 | 新增 `nodes.agent_token_hash`（规格 §7.1 未列） | 满足「agent_token 只存哈希」的安全要求 | 仅 DDL 增列，API 不返回 |
| E3 | 新增 `POST /api/v1/nodes/{id}/network-report`（规格未列） | peer 的 `Endpoint` 需要节点上报公网端点才能生成 | M3 Agent 需实现上报 |
| E4 | `network_secret` 仅创建响应与 agent 配置中下发 | 最小暴露面 | Dashboard 列表不显示 secret |
| E5 | 代理面新增 `POST /api/v1/proxy/render` 只读渲染端点 | 让「配置生成」可被验收而不依赖外部 NPS 二进制 | 仅管理接口，无副作用 |
| E6 | 规格 §4.4.2 前端 React 与 §6 vue3 冲突 → 取 **Vue 3**（裁决 D1） | 见 §2 | Dashboard 用 Ant Design Vue |
| E7 | **配置下发语义修正**：`GET /api/v1/agent/config` 的 `version` 视为**客户端状态提示**而非资源 ID —— 与最新版本不同（落后/超前/未知）一律返回**最新期望配置** + 最新版本号，仅「等于最新」时返回 304；不再返回调用方自己的历史快照，也不再对未知版本返回 404 | M2b 原实现返回历史快照 + 未知版本 404，会导致**落后超过一个版本的节点永不收敛**（应用旧快照 → 存旧版本号 → 永远请求同一版本）；版本超前（如从备份恢复）会被 404 卡死 | 已修 `internal/service/config/config.go::Delivery`；新增回归测试 `TestDeliveryServesLatestForLaggingClient`；历史快照仍可经 `GET /api/v1/configs` 与 `POST /configs/.../rollback` 取用 |

**尚未实现（转 V1）**：ACME 自动签发（仅有 `Issuer` 接口位 + `ErrNotImplemented`）、中继节点调度、Windows/macOS Agent、Helm Chart、WebSocket 实时推送（Dashboard 用轮询降级）、商业化计费。

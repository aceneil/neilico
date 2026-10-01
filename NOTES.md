# NOTES.md — umpp 目录导览

> 用户约定：进入本目录前先读本文件；结构变化后**随时更新**，保证新会话只凭本文件即可开工。

## 这是什么
UMPP（Unified Mesh & Proxy Platform）：统一「内网穿透 + Mesh 组网」后台管理系统。
三平面：控制面（管理/下发/审计）、穿透代理面（域名反代/隧道）、Mesh 组网面（WireGuard 虚拟网）。

## 文件地图
| 路径 | 作用 |
| :--- | :--- |
| `docs/UMPP_SPEC.md` | **上游需求规格**（用户提供，勿擅改；只可追加「裁决/偏差」附录） |
| `PLAN.md` | **执行计划**：里程碑 M1–M5、验收标准、技术裁决 D1–D6 |
| `docs/USER_GUIDE.md` / `docs/OPS.md` / `docs/API.md` | 用户 / 运维 / 接口文档（M5 产出） |
| `control-plane/` | Go 控制面 API（Gin + GORM + PostgreSQL16） |
| `agent/` | Go Agent（注册/心跳/拉配置/应用 WireGuard/子网路由） |
| `cli/` | `umppctl` 命令行 |
| `dashboard/` | Vue3 + Vite + Ant Design Vue 管理后台 |
| `deploy/docker-compose/` | 单机一键部署栈 |
| `scripts/` | 冒烟与验收脚本（可复跑，退出码即判据） |

## 关键决策（详见 PLAN.md §2）
- 前端 = **Vue3 + Ant Design Vue**（规格 §4.4.2 React 与 §6 vue3 矛盾，取 Vue3）。
- 测试用**纯 Go sqlite 驱动**（`glebarez/sqlite`），生产用 PostgreSQL。
- Mesh 走 `MeshProvider` 接口：MVP = 自研 WireGuard 配置生成；EasyTier/Headscale 仅配置导出。
- 代理走 `ProxyProvider` 接口：MVP = NPS 配置生成 + 内置 `httputil.ReverseProxy` 直连。

## 当前状态
| 里程碑 | 状态 | 提交 | 备注 |
| :--- | :--- | :--- | :--- |
| M0 骨架/计划 | ✅ | `643786d` | 仓库、规格入库（1320 行）、PLAN（里程碑 + D1–D6 裁决） |
| M1 控制面骨架+身份/节点 | ✅ | `c9c8372` | 14 张核心表、JWT/RBAC/租户隔离、节点注册/心跳/离线 sweeper、审计、`/metrics` |
| M2a 域名/证书/代理面 | ✅ | `c3c4af6` | 域名/证书（私钥 AES-GCM 加密、响应 `json:"-"`）/代理规则 CRUD、NPS 配置 Provider、**内置 httputil 反代**（WS + IP 白名单 + BasicAuth + JWT）、流量上报 |
| M2b Mesh + 配置版本化下发 | ✅ | 见 `git log` | 虚拟网络/成员（并发安全 VIP 分配）/ACL（31 条用例，默认拒绝）/子网路由、WireGuard 与 EasyTier Provider、加密密钥与轮换、`config_versions` + 304 + 回滚只增版本号 |
| M3 Agent + CLI | ✅ | 见 `git log` | `agent/`（注册/心跳/端点上报/版本化轮询/幂等应用管线/wgctrl→shell 降级/dry-run 不碰宿主网络/子网路由与回滚/流量增量/指标/优雅退出/Dockerfile）+ `cli/`（umppctl 全部子命令）+ `control-plane/testkit` 跨 module 集成测试 |
| **修正**：配置下发语义 | ✅ | 见 `git log` | `agent/config` 的 `version` 改为**客户端状态提示**：非最新版本（落后/超前/未知）一律返回最新配置，仅相等时 304。原实现返回历史快照会让落后节点**永不收敛**（详见 PLAN §9 E7） |
| M4 Dashboard | 🔄 | — | codex 运行中（Vue3 + Ant Design Vue，含 4K/主题/一屏三项硬性 UI 要求） |
| M5 部署+文档+冒烟 | ⏳ | — | 任务书已就绪 |

### 关键接口事实（Agent/Dashboard 对接必读）
- 注册 `POST /api/v1/nodes/register` **不需要上传 public_key**（控制面用 wgtypes 生成密钥对）；响应里的 `private_key` **只出现一次**。
- 配置轮询 `GET /api/v1/agent/config?node_id=&version=`：**`version` 是客户端状态提示，不是资源 ID** —— 与最新版本相等返回 **HTTP 304**；落后 / 超前 / 未知版本一律返回 **200 + 最新期望配置**（保证任何客户端都收敛）。历史快照请走 `GET /api/v1/configs` 与 `POST /api/v1/configs/{target_type}/{target_id}/rollback`。
- 节点端点上报 `POST /api/v1/nodes/{id}/network-report`（agent_token 认证），成功后同网络 peer 版本递增。
- 私有数据保护约定：证书 `key_pem`、节点密钥 `private_key` 一律 `json:"-"` 或一次性返回；agent_token 只存 SHA-256。

### 轮次任务书位置（Manager 用）
`~/.hermes/cache/umpp-{M1,M2a,M2b,M3,M4,M5}-brief.md`；派发器 `~/.hermes/cache/umpp-dispatch.sh <brief> w23 /home/neil/Documents/Projects/umpp`；守护 `~/.hermes/cache/umpp-watch.sh <pid> <tag>`。

### 操作纪律（踩过的坑）
- **codex 轮次运行期间不要改仓库工作区**（含文档）：轮次结束时报 "_Verified baseline ... clean worktree_"，会静默回滚未提交改动。所有仓库内编辑只在「轮次退出 → 复验 → 写文档 → 一次性 commit」窗口做。

## 常用命令
```bash
# 控制面（开发）
cd control-plane && go test ./... && go run ./cmd/api -c configs/config.yaml
# Dashboard
cd dashboard && npm ci && npm run dev
# 全栈
cd deploy/docker-compose && docker compose up -d --build
# 端到端冒烟
bash scripts/smoke.sh
```

## 坑与注意
- Go 依赖代理：若 `go mod tidy` 卡住，设 `GOPROXY=https://goproxy.cn,direct`。
- npm 用 `npm ci`（有 lock 时），无 lock 时 `npm install`。
- Agent 应用 WireGuard 需要 root + `wg` 或 wgctrl；无权限环境下必须能「dry-run 打印配置」而不报错。

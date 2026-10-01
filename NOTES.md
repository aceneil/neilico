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
| 里程碑 | 状态 | 备注 |
| :--- | :--- | :--- |
| M0 骨架/计划 | ✅ | 2026-10-01 建立仓库、规格入库、PLAN 完成 |
| M1 控制面骨架+身份/节点 | ⏳ | |
| M2 域名代理+Mesh+配置下发 | ⏳ | |
| M3 Agent + CLI | ⏳ | |
| M4 Dashboard | ⏳ | |
| M5 部署+文档+冒烟 | ⏳ | |

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

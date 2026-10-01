# UMPP — Unified Mesh & Proxy Platform

统一的内网穿透 + Mesh 组网后台管理系统。让「普通用户用域名访问内网服务」与「技术用户设备间 P2P 直连」共用一套控制面。

- 📄 需求规格：[docs/UMPP_SPEC.md](docs/UMPP_SPEC.md)
- 🗺️ 执行计划：[PLAN.md](PLAN.md)
- 🧭 目录导览：[NOTES.md](NOTES.md)

## 三个平面

| 平面 | 职责 | 关键组件 |
| :--- | :--- | :--- |
| 控制面 | 多租户、认证授权、节点管理、配置下发、监控审计 | control-plane (Go/Gin/PostgreSQL) |
| 穿透代理面 | 公网域名反代、内网穿透、SSL | ProxyProvider（NPS 配置 / 内置反代） |
| Mesh 组网面 | 虚拟网、P2P、中继、子网路由、ACL | MeshProvider（WireGuard 配置生成 / EasyTier 导出） |

## 组件

| 目录 | 说明 |
| :--- | :--- |
| `control-plane/` | 控制面 REST API + 配置生成 + 监控指标 |
| `agent/` | 节点 Agent：注册、心跳、拉配置、应用 WireGuard、子网路由 |
| `cli/` | `umppctl` 命令行工具 |
| `dashboard/` | Vue 3 + Vite + Ant Design Vue 管理后台 |
| `deploy/` | Docker Compose / Helm 部署 |

## 快速开始

```bash
# 1) 全栈启动
cd deploy/docker-compose && cp .env.example .env && docker compose up -d --build

# 2) 登录控制面
curl -s -XPOST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"<见 .env>"}'

# 3) 端到端冒烟
bash scripts/smoke.sh
```

Dashboard: http://localhost:3000 · Control API: http://localhost:8080

## 开发状态

里程碑进度见 [NOTES.md](NOTES.md#当前状态)。

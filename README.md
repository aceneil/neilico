# UMPP — Unified Mesh & Proxy Platform

统一的内网穿透 + Mesh 组网后台管理系统。「普通用户用域名访问内网服务」与「技术用户设备间 P2P 直连」共用一套控制面。

- 📄 需求规格：[docs/UMPP_SPEC.md](docs/UMPP_SPEC.md)
- 🗺️ 执行计划：[PLAN.md](PLAN.md)
- 🧭 目录导览：[NOTES.md](NOTES.md)
- 📚 API 文档：[docs/API.md](docs/API.md)
- 🧑‍💻 用户指南：[docs/USER_GUIDE.md](docs/USER_GUIDE.md)
- 🛠️ 运维手册：[docs/OPS.md](docs/OPS.md)

## 三个平面

| 平面 | 职责 | 关键组件 |
| :--- | :--- | :--- |
| 控制面 | 多租户、认证授权、节点管理、配置下发、监控审计 | control-plane (Go/stdlib HTTP + GORM + PostgreSQL) |
| 穿透代理面 | 公网域名反代、内网穿透、SSL | ProxyProvider（NPS 配置 / 内置反代） |
| Mesh 组网面 | 虚拟网、P2P、中继、子网路由、ACL | MeshProvider（WireGuard 配置生成 / EasyTier 导出） |

## 组件状态

| 组件/交付物 | 状态 | 说明 |
| :--- | :--- | :--- |
| `control-plane/` | ✅ M1–M2b/M4b/V1-R1 | REST API、JWT/RBAC、租户隔离、配置版本、ACME 自动签发/续期、SNI TLS、metrics |
| `agent/` | ✅ M3 | 注册/心跳/配置轮询、WireGuard shell applier、dry-run、指标、Dockerfile |
| `cli/` | ✅ M3 | `umppctl` 登录、节点、网络、域名、状态 |
| `dashboard/` | ✅ M4 | Vue 3 + Ant Design Vue + ECharts |
| NPS 配置集成 | ✅ MVP | 生成配置；NPS 数据面由外部服务提供 |
| Mesh 配置生成 | ✅ MVP | WireGuard 配置生成、ACL/子网路由、版本化下发 |
| relay 数据面 | ⚠️ 占位 | 代码只有 relay 元数据 CRUD；Compose 使用 wg-easy 占位，3478/udp 预留，留给 V1 |
| Docker Compose | ✅ M5 | PostgreSQL/Redis/NATS/control-api/dashboard/relay，非 root 镜像、healthcheck、日志限制 |
| ACME/TLS overlay | ✅ V1-R1 | Pebble RFC 8555 真实 HTTP-01、CA 信任、SNI TLS，`scripts/smoke-acme.sh` |
| 端到端冒烟 | ✅ M5 | `scripts/smoke.sh`，真实 Agent dry-run + builtin 反代 + metrics/audit 断言 |
| Helm Chart | ⏳ 留给 V1 | 本预算不提供半成品 Chart，建议在 V1 做 PostgreSQL 外部依赖版本 |

## 一键启动

本机开发机的 `8080/3000/5432/6379/4222` 已被既有容器占用，因此默认宿主端口采用偏移值。修改 `deploy/docker-compose/.env` 后可改回标准端口。

```bash
cd deploy/docker-compose
cp .env.example .env
# 用密码管理器/openssl rand -hex 32 替换所有 replace-* 占位符
docker compose up -d --build
curl -fsS http://127.0.0.1:18080/healthz
```

Dashboard: `http://127.0.0.1:13000` · Control API: `http://127.0.0.1:18080` · Builtin proxy: `http://127.0.0.1:18081` · ACME/TLS overlay: TLS `18443`, HTTP-01 `18082`, Pebble `14000/8055`

```bash
# 端到端冒烟（退出码即判据）
bash scripts/smoke.sh
# ACME/Pebble 真实协议冒烟（只用 Pebble，禁止 LE 生产）
bash scripts/smoke-acme.sh
# 破坏性清理（需明确 --yes，会删除 pgdata）
bash scripts/smoke-down.sh --yes
```

## 截图

- Dashboard 登录页：`docs/assets/dashboard-login.png`（待补）
- 节点/网络总览：`docs/assets/dashboard-overview.png`（待补）
- 域名代理规则：`docs/assets/dashboard-proxy.png`（待补）

## 开发状态

里程碑进度、端口、启动命令和已知坑见 [NOTES.md](NOTES.md#当前状态)。

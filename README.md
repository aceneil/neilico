# NEILICO — Unified Mesh & Proxy Platform

统一的内网穿透 + Mesh 组网后台管理系统。「普通用户用域名访问内网服务」与「技术用户设备间 P2P 直连」共用一套控制面。

- 📄 需求规格：[docs/NEILICO_SPEC.md](docs/NEILICO_SPEC.md)
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
| `control-plane/` | ✅ M1–M2b/M4b/V1-R1/V1-R2 | REST API、JWT/RBAC、租户隔离、配置版本、ACME 自动签发/续期、SNI TLS、告警规则引擎、metrics |
| `agent/` | ✅ M3 | 注册/心跳/配置轮询、WireGuard shell applier、dry-run、指标、Dockerfile |
| `cli/` | ✅ M3 | `neilicoctl` 登录、节点、网络、域名、状态 |
| `dashboard/` | ✅ M4/V1-R2 | Vue 3 + Ant Design Vue + ECharts；告警中心与证书剩余天数 |
| NPS 配置集成 | ✅ MVP | 生成配置；NPS 数据面由外部服务提供 |
| Mesh 配置生成 | ✅ MVP | WireGuard 配置生成、ACL/子网路由、版本化下发 |
| relay 数据面 | ⚠️ 占位 | 代码只有 relay 元数据 CRUD；Compose 使用 wg-easy 占位，3478/udp 预留，留给 V1 |
| all-in-one Docker | ✅ V2-A1 | 单容器 PostgreSQL + control-api + Dashboard；Redis/NATS/nginx/wg-easy 不再部署 |
| ACME/TLS overlay | ✅ V1-R1 | Pebble RFC 8555 真实 HTTP-01、CA 信任、SNI TLS，`scripts/smoke-acme.sh` |
| 告警体系 | ✅ V1-R2 | 五条 §15.2 规则、状态机/事件、log/webhook、告警 API 与 Dashboard |
| 端到端冒烟 | ✅ M5 | `scripts/smoke.sh`，真实 Agent dry-run + builtin 反代 + metrics/audit 断言 |
| Helm Chart | ✅ V1-R3 | `deploy/helm/neilico`，默认外部 PostgreSQL，含开发依赖开关、Secret/Ingress/HPA/PDB/NetworkPolicy/ServiceMonitor |

## 一键启动

本机开发机的 `8080/3000/5432/6379/4222` 已被既有容器占用。V2-A1 单容器使用 `13000`（Dashboard + API）和 `18081`（内置反代）。

```bash
cd deploy/allinone
./sync-source.sh
cp .env.example /home/neil/Documents/Docker/data/neilico/neilico.env
# 用密码管理器/openssl rand -hex 32 替换所有 replace-* 占位符
docker compose up -d --build
curl -fsS http://127.0.0.1:13000/healthz
```

Dashboard + Control API: `http://127.0.0.1:13000` · Builtin proxy: `http://127.0.0.1:18081`

```bash
# 端到端冒烟（退出码即判据）
bash scripts/smoke.sh
# ACME/Pebble 真实协议冒烟（只用 Pebble，禁止 LE 生产）
bash scripts/smoke-acme.sh
# 破坏性清理（需明确 --yes，会删除 pgdata）
bash scripts/smoke-down.sh --yes
```

## 首次登入注册与账号管理

### 首次登入 = 注册

- 登录页提供「首次使用？注册」入口，进入 `/register`。
- 注册页会先请求 `GET /api/v1/setup/status`：
  - `registration_open=true`（系统还没有任何账号）→ 可创建**第一个平台管理员**，创建成功即自动登录。
  - 已初始化（`initialized=true`）→ 页面提示「系统已完成初始化」，只能登录。
- 后端对「已有账号再注册」一律返回 `409 already_initialized`（`POST /api/v1/setup/register`）。
- **默认强密码不被弱化**：若 env 里配置了 `NEILICO_BOOTSTRAP_ADMIN_EMAIL` / `NEILICO_BOOTSTRAP_ADMIN_PASSWORD`，首启时仍会用它们种入第一个管理员；只有 env **未配置**管理员凭据时，才需要走上面的注册流程（此时 `bootstrap` 不再让服务启动失败）。
- **密码强度要求**（注册与轮换一致）：长度 ≥ 16 字符，且包含 大写字母 / 小写字母 / 数字 / 符号 中的至少三类。

### 账号管理

登录后侧栏「账号管理」（右上角用户菜单里也有入口）可自助维护当前账号：

- **修改登录邮箱**：需输入**当前密码**；目标邮箱若已被占用返回 `409 conflict`。
- **轮换登录密码**：需输入**当前密码** + **新密码** + **确认新密码**；成功后**此前的 refresh token 立即失效**，前端会用响应里的新会话保持登录。

接口（均登录后可用；API Token 不能代替本人操作）：`GET /api/v1/account`、`PUT /api/v1/account/email`、`POST /api/v1/account/password/rotate`。

### 如何查询当前管理员密码（方式保持不变）

密码始终从同一个键读取，轮换后**会自动回写**，查询方法不变：

```bash
# 唯一的查看入口（值不落文档、不入仓库）
/home/neil/Documents/Docker/data/neilico/show-admin-password.sh
```

- 文件：`/home/neil/Documents/Docker/data/neilico/neilico.env`（mode 600）
- 键：`NEILICO_BOOTSTRAP_ADMIN_PASSWORD`（密码）、`NEILICO_BOOTSTRAP_ADMIN_EMAIL`（邮箱）
- 在控制台执行「轮换密码 / 修改邮箱」后，控制面会把新值**原子回写**到该 env 文件的同一键（同目录临时文件 + rename、保持 mode 600、只替换目标键、不动其它行），因此 `show-admin-password.sh` 读到的始终是最新值。
- 容器侧：compose 把该文件以 rw 方式挂载到 `/opt/neilico/bootstrap.env`，并设置 `NEILICO_BOOTSTRAP_ENV_FILE` 指向它（该路径可配置，默认值就是 `/opt/neilico/bootstrap.env`）。

## 截图

- Dashboard 登录页：`docs/assets/dashboard-login.png`（待补）
- 节点/网络总览：`docs/assets/dashboard-overview.png`（待补）
- 域名代理规则：`docs/assets/dashboard-proxy.png`（待补）

## Kubernetes

集群部署使用 `deploy/helm/neilico`；安装、外部 PostgreSQL/Redis/NATS、Secret、Ingress/ACME 与生产检查见 [Chart README](deploy/helm/neilico/README.md) 和 [运维手册](docs/OPS.md#kubernetes-部署)。离线断言可运行：

```bash
export PATH="$HOME/.local/bin:$PATH"
cd deploy/helm && bash neilico/ci/verify.sh
```

## 开发状态

里程碑进度、端口、启动命令和已知坑见 [NOTES.md](NOTES.md#当前状态)。

## V1-S 传输安全

支持内置 PKI、控制面 TLS/mTLS、代理 HTTPS/HSTS/HTTPS 上游、NPS 隧道 crypt/compress 和 WireGuard PSK。默认配置保持历史行为；启用前阅读 `docs/OPS.md` 的 CA 保管、回退和未加密链路说明。真实验证：`bash scripts/smoke-tls.sh`。

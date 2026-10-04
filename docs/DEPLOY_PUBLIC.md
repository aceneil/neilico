# 公网单机部署清单（含真域名 + TLS + Mesh）

> 目标：在一台**有公网 IP** 的机器上跑 NEILICO 控制面（单容器），用真实域名 + ACME 证书对外，
> 并让内网/异地设备通过 WireGuard Mesh 互联。
> 本文件只写"上公网"特有的部分；功能用法见 `docs/USER_GUIDE.md`，接入验收见 `docs/AGENT_ACCEPTANCE.md`。

## 0. 需要的东西

| 件 | 位置 / 说明 |
| :--- | :--- |
| 控制面镜像 | `ghcr.io/aceneil/neilico-allinone:latest`（单容器，内含控制面+内嵌 agent+前端） |
| 数据卷 | Postgres 数据目录（本机为 `Docker/data/neilico/pg`，可整目录搬走或全新开始） |
| env | `Docker/data/neilico/neilico.env`（**新机器建议重新生成，不要沿用旧 JWT/密码**） |
| 设备侧镜像 | `ghcr.io/aceneil/neilico-agent:latest`（**public，可直接拉**） |

⚠️ `neilico-allinone` 包默认 **private**：要么在新机器上 `docker login ghcr.io`，
要么在 GitHub 网页把包改成 Public（`https://github.com/users/aceneil/packages/container/neilico-allinone/settings`）。

## 1. 必须改的环境变量

| 键 | 公网部署该填什么 |
| :--- | :--- |
| `NEILICO_SERVER_HOST` | 你的公网域名（如 `neilico.example.com`）；影响接入命令里给设备的下发地址 |
| `NEILICO_SERVER_PORT` | 对外端口（有 TLS 时为 `443`，否则与监听一致） |
| `NEILICO_AUTH_JWT_SECRET` | **必须重新生成**（`openssl rand -hex 32`），不要复用旧值 |
| `NEILICO_BOOTSTRAP_ADMIN_EMAIL` / `_PASSWORD` | 首启管理员；密码用强口令（首启后会写进 DB，之后改 env 无效） |
| `POSTGRES_PASSWORD` | 重新生成 |
| `NEILICO_PROXY_ENABLED` | `true`（域名反代面） |
| `NEILICO_PROXY_LISTEN` | `:8080`（容器内监听；宿主映射成 80/443） |
| `NEILICO_PROXY_TLS_ENABLED` | `true`（要 HTTPS 就开） |
| `NEILICO_ACME_ENABLED` / `NEILICO_ACME_CHALLENGE` | `true` / `http-01`；ACME 要求 **80 端口公网可达**，且 `agree_tos` 必须显式同意 |

## 2. 端口与防火墙

| 端口 | 用途 |
| :--- | :--- |
| `80/tcp` | ACME http-01 挑战 + HTTP（可 301 到 HTTPS） |
| `443/tcp` | 域名反代 + 控制面（TLS 终止） |
| `51820/udp` | 设备侧 WireGuard Mesh（**进出都要放行**，否则设备握不上手） |
| 控制面管理端口 | 建议只在内网/白名单开放；暴露公网时必须走 TLS |

## 3. 起服务

```bash
# 新机器上
docker login ghcr.io            # 若 allinone 包仍是 private
mkdir -p data/neilico && cp <你改好的>neilico.env data/neilico/neilico.env
docker compose -f docker-compose.neilico.yaml up -d
curl -fsS http://127.0.0.1:<SERVER_PORT>/healthz     # 期望 {"db":"up","status":"ok",...}
```

## 4. 打通设备与域名（顺序建议）

1. 登录控制面 → **虚拟网络**：新建网络（如 `100.64.0.0/24`）→ 记下网络 ID
2. **设备管理 → 接入设备**：按平台的命令接入设备（agent 镜像可直接拉 ✓）
   - 验收看两点：`wg show` 有握手、宿主/容器内 `ip route show dev wg0` 出现对端虚拟 IP 路由
   - 同内网设备会自动用内网地址建隧道；跨网的靠公网 endpoint（见"已知边界"）
3. **域名与代理**：新建域名 → 关联证书（或等 ACME 签发）→ 建规则
   - `target_type=node` + `target=<节点ID>:<端口>`：反代会把它解析成该节点的**虚拟 IP**（走 Mesh）
   - 也可用 `virtual_ip` / `internal_ip`
   - 规则支持访问控制：IP 白名单 / Basic Auth / 要求 JWT
4. DNS：把域名 A 记录指到这台公网机（ACME 与对外访问都依赖它）

## 5. 部署后自检（缺一不可）

```bash
curl -fsS https://<域名>/healthz                       # 控制面
curl -fsS -H 'Host: <业务域名>' http://127.0.0.1/       # 反代规则（本机）
ping -c2 <对端虚拟IP>                                   # Mesh（在设备上）
# 双向：A 设备访问 B 设备虚拟 IP 上的服务（如 http://<B的VIP>:9100/metrics）
```

## 6. 已知边界（先知道，避免误判）

- **无 relay / 无打洞**：两端都在不可达 NAT 之后时隧道建不起来；至少一端需公网可达或与对端同内网。
- **Windows / macOS 组网未落地**：agent 在这些平台上如实上报 `mesh=degraded`（注册/心跳/拉配置可用）。
- **NPS 方式穿透未集成**：当前穿透只走内置反代（域名 → 节点虚拟 IP）。
- **ACME 需 80 可达**且需显式同意条款；`dns-01` 与 EAB 仍是保留位。
- **反代入口端口**若非 80/443，浏览器访问要带端口（Host 带端口已能正确匹配，但仍不便于用户）。

## 7. 升级镜像（避免踩过的坑）

```bash
docker pull ghcr.io/aceneil/neilico-allinone:latest
docker compose -p <原项目名> up -d --force-recreate     # 注意 -p 必须与原项目名一致，否则会换空卷→丢数据
docker inspect <容器> --format '{{.Image}}'              # 确认镜像 ID 真的变了，不要只看 pull 输出
```

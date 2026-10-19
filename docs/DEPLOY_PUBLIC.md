# 公网服务器部署（Caddy + 单容器控制面）

> 适用于一台有公网 IP 的 Linux 机器，用真实域名 + 自动证书对外。
> 产物：`deploy/server/docker-compose.public.yml`、`deploy/server/Caddyfile.example`、
> `deploy/server/neilico.env.public.example`。
> 本文所有命令都在 `/opt/neilico` 下执行。

## 0. 先决条件

| 项 | 要求 |
| :--- | :--- |
| 域名 | `neilico.example.com` 的 A 记录指向本机公网 IP（Caddy 办证要能解析到） |
| 端口 | `80/tcp`、`443/tcp`、`443/udp`（Caddy）；`20000-20019/tcp+udp`（端口转发）；`21115-21119/tcp`、`21116/udp`（自托管 RustDesk） |
| 镜像 | `ghcr.io/aceneil/neilico-server:latest`（公开包，匿名可拉） |
| 系统 | 已装 Docker + compose v2；`curl` |

> 安全组/防火墙**只放行上面这些端口**；控制面不要用明文端口暴露公网，统一走 443。

## 1. 目录与文件

```bash
sudo mkdir -p /opt/neilico && cd /opt/neilico
sudo mkdir -p data/pg data/rustdesk data/downloads data/caddy/data data/caddy/config

# 三个文件（从仓库 deploy/server/ 复制后改名）
#   docker-compose.public.yml -> docker-compose.yml
#   neilico.env.public.example -> neilico.env
#   Caddyfile.example -> Caddyfile
sudo chmod 600 neilico.env
```

## 2. 生成配置

```bash
# 生成两个密钥
openssl rand -hex 32   # -> NEILICO_AUTH_JWT_SECRET
openssl rand -hex 32   # -> POSTGRES_PASSWORD
```

编辑 `neilico.env`，**必须**替换的项：

```ini
POSTGRES_PASSWORD=<上面生成的>
NEILICO_AUTH_JWT_SECRET=<上面生成的>
NEILICO_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
NEILICO_BOOTSTRAP_ADMIN_PASSWORD=<强口令，首启后写入库>
NEILICO_ENROLL_PUBLIC_URL=https://neilico.example.com   # 决定接入命令里的地址
NEILICO_RD_ID_SERVER=neilico.example.com
NEILICO_RD_RELAY_SERVER=neilico.example.com
```

编辑 `Caddyfile`：把 `neilico.example.com` 换成你的域名，`email` 换成你的邮箱；
业务域名（被反代到内网节点的）在文件末尾逐个添加。

## 3. 启动

```bash
cd /opt/neilico
docker compose pull
docker compose up -d
docker compose ps                 # server 应为 healthy，caddy 为 running
```

首次启动会：建库 → 建租户 → 建管理员 → 起 8080（控制面）+ 8081（内置反代）。

## 4. 自检

```bash
curl -fsS https://neilico.example.com/healthz
# {"db":"up","status":"ok","uptime":...,"version":"..."}

docker compose logs --tail=20 server     # 看 "NEILICO API listening"
docker compose logs --tail=20 caddy      # 看证书是否拿到
```

浏览器打开 `https://neilico.example.com` → 用 bootstrap 管理员登录。

再验证接入命令的地址对不对（关键，填错设备接不回来）：

> 控制台 → 设备管理 → 接入设备 → 生成令牌 → 任意页签，
> 命令里的地址必须是 `https://neilico.example.com`。

## 5. 加设备 / 加业务域名

1. **设备**：接入设备 → 复制对应平台命令；Docker 版一条 `docker run`，裸机版一条 curl。
2. **业务域名**：`Caddyfile` 加一段 `xxx.example.com { reverse_proxy server:8081 }`
   → `docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile`
   → 控制台「域名与代理」建域名 + 规则（目标填 `节点ID:端口`，会自动解析成该节点虚拟 IP）。
3. **端口转发**：控制台新建规则，外部访问 `公网IP:20000+n`。

## 6. 升级

```bash
cd /opt/neilico
docker compose pull
docker compose up -d            # 项目名固定为 neilico，卷不会换
docker inspect neilico --format '{{.Image}}'   # 确认镜像 ID 变了
```

> 千万别改 compose 顶部的 `name:`，也别用 `down -v`：那会换空卷、丢数据库。

## 7. 备份

```bash
docker exec neilico pg_dump -U neilico neilico | gzip > neilico-$(date +%F).sql.gz
sudo tar czf neilico-data-$(date +%F).tgz data/pg data/rustdesk data/downloads
```

`data/rustdesk` 里是 RustDesk 服务端密钥，丢了所有客户端要重新配。

## 8. 已知边界

- ACME 走 Caddy（http-01），**80 端口必须公网可达**；不想开 80 就用 DNS 方式或自备证书挂到 Caddy。
- 无 relay / 无打洞：两端都在不可达 NAT 后时 WireGuard 建不起来。
- `dns-01`、EAB、证书吊销仍是保留位（服务端内置 ACME）。
- Windows/macOS 的 agent 上报 `mesh=degraded`；接入/心跳/拉配置可用。

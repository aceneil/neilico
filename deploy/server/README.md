# NEILICO 服务端部署

单容器：PostgreSQL + 控制面 API + 内置反代 + Dashboard 全在一个镜像里。
面板进程**默认监听 `11333`**（写在 `entrypoint.sh` 里，不需要环境变量）。
部署只有一个文件：[`neilico-stack.yml`](neilico-stack.yml)。

---

## 1. compose（完整可用）

```yaml
name: neilico

services:
  server:
    image: ghcr.io/aceneil/neilico-server:latest
    container_name: neilico
    restart: unless-stopped
    ports:
      - "11333:11333"                      # 面板 + /api/v1
      # - "127.0.0.1:8081:8081"            # 业务域名反代（用 Nginx 接管时按需打开）
    environment:
      # ① 必填：数据库密码，缺了容器直接退出
      POSTGRES_PASSWORD: "neilico"
      # ② 可留空：留空 = 用「你打开面板时的地址」生成接入命令
      #    NEILICO_ENROLL_PUBLIC_URL: "https://neilico.example.com"
      # ③ 留空 = 首次打开页面时注册管理员
      NEILICO_BOOTSTRAP_ADMIN_EMAIL: ""
      NEILICO_BOOTSTRAP_ADMIN_PASSWORD: ""
    volumes:
      - ./neilico/data:/var/lib/postgresql/data     # PostgreSQL 数据（别删）
      - ./neilico/state:/var/lib/neilico            # bootstrap.env / RustDesk 密钥
      # 让「反向代理」页能识别 Docker 里的 nginx/caddy（只读即可探测）
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

```bash
docker compose -f neilico-stack.yml up -d
curl -fsS http://<IP>:11333/healthz     # {"db":"up","status":"ok"}
```

打开 `http://<IP>:11333` → 首次注册管理员。

| 端口 | 用途 | 是否必须 |
| :--- | :--- | :--- |
| `11333/tcp` | 面板 + `/api/v1` | ✅ |
| `8081/tcp` | 业务域名反代（容器内**始终**在监听） | 走 Nginx 时要能访问 |
| `20000` tcp+udp | 端口转发 | 用才开 |
| `21116` tcp+udp、`21117/tcp` | 自托管 RustDesk | 用才开 |

| 设置 | 说明 |
| :--- | :--- |
| `POSTGRES_PASSWORD` | 必填 |
| `NEILICO_ENROLL_PUBLIC_URL` | 可留空；留空按请求 Host 推导 |
| `NEILICO_BOOTSTRAP_ADMIN_EMAIL` / `_PASSWORD` | 留空 = 首次注册；都留空才有效 |

---

## 2. 与已有 Nginx 共存（推荐拓扑）

**80/443 只有一个所有者**：让 Nginx 持有，NEILICO 退到内网端口。

```
浏览器 → :443 Nginx（TLS 终止，Host 透传）
           ├─ neilico.example.com → http://neilico:11333   面板 / API
           └─ app.example.com     → http://neilico:8081    业务反代
                                        ↓ 按 Host 找规则
                                   100.64.0.x:端口（走 WireGuard）
```

### 2.1 Nginx 与 NEILICO 在同一 Docker 网络（用容器名）

> 只有 user-defined network 才有 DNS；默认 `bridge` 网络解析不了容器名。

```nginx
# /etc/nginx/conf.d/neilico.conf

# 控制面
server {
    listen 443 ssl;
    http2 on;
    server_name neilico.example.com;

    ssl_certificate     /etc/letsencrypt/live/neilico.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/neilico.example.com/privkey.pem;

    client_max_body_size 512m;

    location / {
        proxy_pass http://neilico:11333;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}

# 业务域名（每个域名一段；Host 必须原样透传）
server {
    listen 443 ssl;
    http2 on;
    server_name app.example.com;

    ssl_certificate     /etc/letsencrypt/live/app.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/app.example.com/privkey.pem;

    client_max_body_size 512m;

    location / {
        proxy_pass http://neilico:8081;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

如果 Nginx 在**另一个** compose 项目里，让 NEILICO 额外接入那个网络：

```yaml
services:
  server:
    networks: [default, proxy]

networks:
  proxy:
    external: true
    name: <nginx 项目名>_default
```

### 2.2 Nginx 装在宿主机上（用回环端口）

compose 里把端口只绑回环：

```yaml
    ports:
      - "127.0.0.1:11333:11333"
      - "127.0.0.1:8081:8081"
```

Nginx 里把 `proxy_pass` 改成：

```nginx
        proxy_pass http://127.0.0.1:11333;   # 面板那一段
        proxy_pass http://127.0.0.1:8081;    # 业务域名那一段
```

其余（Host / X-Forwarded-* / buffering / timeout）与 2.1 完全一致。

### 2.3 验证

```bash
# 面板
curl -s -o /dev/null -w '%{http_code}\n' https://neilico.example.com/healthz

# 业务域名（从 Nginx 所在环境执行）
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: app.example.com' http://neilico:8081/

# 从 Nginx 容器内确认网络可达
docker exec -it <nginx容器> sh -c \
  'curl -s -o /dev/null -w "%{http_code}\n" -H "Host: app.example.com" http://neilico:8081/'
```

| 返回 | 含义 |
| :--- | :--- |
| `200` / `30x` | 通了 |
| `404` | 通了，但**域名没登记或规则没启用** |
| `502` / `504` | 节点离线，或目标端口不通 |
| `bad address` | 不在同一网络，改用 2.2 的回环方式 |

---

## 3. 在控制台里配置业务域名

1. **域名与代理 → 新建域名**：`app.example.com`（DNS A 记录指向服务器公网 IP）
2. **新建代理规则**：目标类型选 **节点**，目标填 `节点ID:端口`（如 `a1b2c3d4…:8080`）
   - 用 `节点ID:端口` 而不是写死 `100.64.0.x`：节点换网络/换虚拟 IP 后规则仍然有效
   - 规则可选：IP 白名单 / Basic Auth / 要求 JWT
3. 保存即生效，不需要重启容器

链路：

```
客户端 → Nginx:443 → NEILICO:8081（按 Host 找规则）
       → 100.64.0.x:端口（WireGuard 隧道 → 目标节点）
```

---

## 4. 「反向代理」页（自动接管已有 Nginx/Caddy）

页面会扫描本机 Docker 里的 nginx / caddy / traefik，生成站点配置并跑
`nginx -t` / `caddy validate`，**默认只校验不写入**。

前提是能访问 Docker API，二选一：

### 4.1 挂只读 socket（简单）

```yaml
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

### 4.2 socket-proxy 旁车（不给 socket 文件）

```yaml
services:
  docker-proxy:
    image: tecnativa/docker-socket-proxy:latest
    restart: unless-stopped
    environment:
      CONTAINERS: 1        # 只放开容器列表；要写入/热重载再加 EXEC: 1 / POST: 1
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro

  server:
    environment:
      DOCKER_HOST: tcp://docker-proxy:2375
```

真正写入 + 热重载需要可写通道，并显式打开：

```yaml
    environment:
      NEILICO_WEBPROXY_WRITE: "1"
```

**原生安装的 Nginx/Caddy**（非容器）不需要 Docker API：镜像会看 `/usr/bin/nginx`、
`/etc/nginx`，把配置目录挂进来即可。

---

## 5. 中继兜底（打洞失败时）

两端都在不可达 NAT 后时，STUN/打洞可能失败。中继跑在**控制面进程内**，默认关闭：

```yaml
    ports:
      - "11333:11333"
      - "51821:51821/udp"                 # 中继
    environment:
      NEILICO_RELAY_ENABLED: "true"
      NEILICO_RELAY_LISTEN: ":51821"
      NEILICO_RELAY_PUBLIC_HOST: "<公网域名或IP>"   # 下发对端的地址，必须公网可达
```

- 只有**直连不通**的 peer 才会拿到 `relay_endpoint`，走得通的对端完全不受影响
- 鉴权复用 node token，每会话限流 2000 pps / 20 MB/s，2 分钟无流量过期
- 指标：`neilico_relay_packets_total` / `neilico_relay_bytes_total` / `neilico_relay_sessions`
- 代价：流量绕服务器一圈，带宽受服务器出口限制（不是 P2P）

## 6. 从源码构建

```bash
git clone https://github.com/aceneil/neilico.git && cd neilico
docker build -f deploy/server/Dockerfile -t neilico-server:local .
# 然后把 neilico-stack.yml 的 image 改成 neilico-server:local
```

---

## 7. 升级与备份

```bash
docker compose -f neilico-stack.yml pull
docker compose -f neilico-stack.yml up -d
docker inspect neilico --format '{{.Image}}'        # 确认镜像 ID 真变了

# 逻辑备份
docker exec neilico pg_dump -U neilico neilico | gzip > neilico-$(date +%F).sql.gz

# 整目录冷备（先 docker compose stop）
tar czf neilico-$(date +%F).tgz neilico/
```

> **不要 `down -v`**：`./neilico/data` 是数据库，`./neilico/state` 存 bootstrap.env 与 RustDesk 密钥。

---

## 8. 已知限制

- 单容器是单点部署，无水平扩展 / 滚动升级。
- PostgreSQL 与应用同生命周期：用 `docker stop` 正常停止，entrypoint 会先停应用再 `fast` 停库。
- `relay`（wg-easy 占位）不是 NEILICO 自有中继数据面，V2 再补。
- WireGuard 的 `wg0` 在**宿主机网络命名空间**（agent 用 `--network host`）；
  普通 bridge 网络的容器访问不到 `100.64.0.0/10`，必须经 NEILICO 的 `8081` 中转。

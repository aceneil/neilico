# NEILICO 服务端部署

单容器：PostgreSQL + 控制面 API + 内置反代 + Dashboard 全在一个镜像里，
**面板进程直接监听 `11333`**（无前置代理、无第二个容器）。
部署只有一个文件：[`neilico-stack.yml`](neilico-stack.yml)。

## 快速开始

```bash
curl -fsSLo neilico-stack.yml \
  https://raw.githubusercontent.com/aceneil/neilico/main/deploy/server/neilico-stack.yml

# 只需改 POSTGRES_PASSWORD（接入地址默认取你访问面板时的地址）
docker compose -f neilico-stack.yml up -d
curl -fsS http://<IP>:11333/healthz
```

打开 `http://<IP>:11333` → 首次注册管理员（模板里 bootstrap 账号留空）。

| 端口 | 用途 | 是否必须 |
| :--- | :--- | :--- |
| `11333/tcp` | 面板 + `/api/v1`（镜像默认监听端口） | ✅ |
| `8081/tcp` | 业务域名反代 | 用才开 |
| `20000` tcp+udp | 端口转发 | 用才开 |
| `21116` tcp+udp、`21117/tcp` | 自托管 RustDesk | 用才开 |

## 要填的设置

| 键 | 说明 |
| :--- | :--- |
| `POSTGRES_PASSWORD` | 必填，缺了容器直接退出 |
| `NEILICO_ENROLL_PUBLIC_URL` | **可留空**：留空时用你打开面板时的地址（Host）生成接入命令；面板在反向代理后或设备要走另一个域名时再显式指定 |
| `NEILICO_BOOTSTRAP_ADMIN_EMAIL` / `_PASSWORD` | 留空 = 首次注册；填了就用它建首个管理员 |

监听端口、数据库驱动、Dashboard 目录、日志格式、下载目录都写在镜像里，**不需要在 compose 里重复**。

## 业务域名反代

反代在容器内监听 `8081`，需要就把端口映射出来：

```yaml
    ports:
      - "11333:11333"
      - "8081:8081"
```

然后在控制台「域名与代理」登记域名 + 规则（目标填 `节点ID:端口`）。

## 不想挂 docker.sock？

「反向代理」页要看到 Docker 里的 nginx/caddy，就必须能访问 Docker API。除了挂 socket，
还可以在**同网络**里放一个只读的 socket 代理，NEILICO 通过 TCP 连它：

```yaml
services:
  docker-proxy:
    image: tecnativa/docker-socket-proxy:latest
    restart: unless-stopped
    environment:
      CONTAINERS: 1        # 只放开容器列表（探测够用）
      # 需要"写入并热重载"时再加：EXEC: 1  POST: 1
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    # 不要 publish 端口，只在 compose 内网可达

  server:
    environment:
      DOCKER_HOST: tcp://docker-proxy:2375
```

这样 NEILICO 容器里既没有 socket 文件，也拿不到完整 Docker 权限，只有「列容器」一个能力。

另外两条更"干净"的路子：

1. **干脆不接管**：宿主 Nginx 前面手动加一段 `proxy_pass http://<neilico>:11333` 即可，
   NEILICO 完全不碰 Docker（见下一节）。
2. **原生安装的 Nginx/Caddy**（不是容器）：不需要 socket，镜像会直接看
   `/usr/bin/nginx` 与 `/etc/nginx`（需要把配置目录挂进来）。

## 复用宿主已有的 Nginx

把面板只绑回环，交给宿主 Nginx 终止 TLS：

```yaml
    ports:
      - "127.0.0.1:11333:11333"    # 面板 + /api/v1
      - "127.0.0.1:8081:8081"      # 业务域名反代（可选）
```

Nginx 侧（`Host` 必须原样透传，反代规则按 Host 匹配）：

```nginx
server {
    listen 443 ssl;
    server_name neilico.example.com;
    location / {
        proxy_pass http://127.0.0.1:11333;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

控制台的「反向代理」页能自动检测这类 Nginx/Caddy（挂载 `docker.sock` 后 Docker 里的也能识别），
生成配置并跑 `nginx -t` / `caddy validate`，默认只校验不写入。

## 从源码构建

```bash
git clone https://github.com/aceneil/neilico.git && cd neilico
docker build -f deploy/server/Dockerfile -t neilico-server:local .
# 然后把 neilico-stack.yml 的 image 改成 neilico-server:local
```

## 升级与备份

```bash
docker compose -f neilico-stack.yml pull
docker compose -f neilico-stack.yml up -d
docker inspect neilico --format '{{.Image}}'      # 确认镜像 ID 真变了

docker exec neilico pg_dump -U neilico neilico | gzip > neilico-$(date +%F).sql.gz
```

> **不要 `down -v`**：`neilico_pg` 是数据库，`neilico_state` 存 RustDesk 密钥（丢了所有客户端要重配）。

## 已知限制

- 单容器是单点部署，无水平扩展 / 滚动升级。
- PostgreSQL 与应用同生命周期：用 `docker stop` 正常停止，entrypoint 会先停应用再 `fast` 停库。
- `relay`（wg-easy 占位）不是 NEILICO 自有中继数据面，V2 再补。

# NEILICO all-in-one container

该形态把 PostgreSQL 16、`neilico-control-api` 与 Dashboard 静态资源合并为 **一个容器**。API、Dashboard、内置反代和 ACME 监听器都由同一个 Go 进程提供；PostgreSQL 是容器内唯一额外服务。`redis`、`nats`、nginx `dashboard`、wg-easy `relay` 均不再部署。

构建上下文是**仓库根**（见下方命令），镜像名 `neilico-allinone:local`。修改仓库源码后不需要任何快照/打包步骤 —— 重新 `docker compose ... up -d --build` 即从当前源码构建。

## 构建与运行

**构建上下文必须是仓库根**（Dockerfile 在多阶段里直接 `COPY control-plane/` 与 `dashboard/`）：

```bash
cd $HOME/Documents/Projects/neilico          # ← 仓库根，不是本目录
docker build -f deploy/allinone/Dockerfile -t neilico-allinone:local .
```

**常驻部署**用 Docker 目录下的 compose（顶层 `name: neilico`，单服务 `app`，`container_name: neilico`），
它自己 `build`（`context` = 仓库根绝对路径），不需要手工 build：

```bash
cd $HOME/Documents/Docker
mkdir -p data/neilico/pg
cp $HOME/Documents/Projects/neilico/deploy/allinone/.env.example data/neilico/neilico.env
chmod 600 data/neilico/neilico.env            # 编辑该文件，替换全部密码/密钥占位符
docker compose -f docker-compose.neilico.yaml up -d --build
```

> 本目录内的 `docker-compose.yml` 只是参考用的等价 Compose（`context: ../..`），常驻不用它。
> 参考 Compose 的项目名是 `neilico`，服务名是 `app`，并显式设置 `container_name: neilico`，因此 `docker ps` 中可直接识别为 `neilico`（不使用 Compose 自动名 `neilico-app-1`）。宿主端口为 `13000 -> 8080`（Dashboard + API **同端口/同源**）和 `18081 -> 8081`（内置反代）。可选 TLS 使用容器端口 `8443`，HTTP-01 使用 `5002`。

```bash
curl -fsS http://127.0.0.1:13000/healthz
open http://127.0.0.1:13000/
```

## 数据、备份与升级

PostgreSQL 持久化目录是 `$HOME/Documents/Docker/data/neilico/pg`，容器内挂载到 `/var/lib/postgresql/data`。该路径保存数据库、角色和 WAL；不要把 `neilico.env` 放进数据库目录。

**下载分发目录**是 `$HOME/Documents/Docker/data/neilico/downloads`，容器内挂载到 `/usr/local/share/neilico/downloads`（`NEILICO_DOWNLOADS_DIR`）。控制面 `/downloads/{filename}` 与 `/install.sh`、`/install.ps1` 都从这里读取产物。把自有客户端发布包（`neilico-client-windows-x64.zip` 等按白名单命名）放进该目录即**无需重建镜像**即可分发/更新；镜像内置的 agent 二进制在容器启动时由 `entrypoint` 从 `/opt/neilico/downloads-seed/` 补齐缺失项，故挂载不会影响一键接入脚本。详见 `docs/REMOTE_DESKTOP.md` 的「客户端分发」。

逻辑备份（命令不会输出密码）：

```bash
docker exec neilico pg_dump --username=neilico --format=custom --dbname=neilico > neilico-$(date +%Y%m%d).dump
```

冷备份前先执行 `docker compose stop app`，确认 PostgreSQL 已优雅停止后归档宿主 `pg` 目录，再启动。恢复时先准备空目录、放入备份并按 PostgreSQL 16 的 `pg_restore` 流程恢复，最后启动单容器。

升级时保持数据目录不变：

```bash
cd $HOME/Documents/Projects/neilico/deploy/allinone
docker compose build --pull app
docker compose up -d
docker inspect --format '{{.State.Health.Status}}' neilico
```

从旧六容器迁移：先对旧 `postgres` 执行 `pg_dump`，保存旧 Compose 的 `.env` 中所需配置（不要复制无关的 Redis/NATS/relay 配置），准备好 `neilico.env` 和空 `pg` 目录，恢复备份后启动本 Compose。确认登录、节点和配置数据正常后，旧栈可按原目录执行：

```bash
cd $HOME/Documents/Projects/neilico/deploy/docker-compose
docker compose down --remove-orphans
```

该命令会下线旧的 postgres/control-api/dashboard/redis/nats/relay 六容器；确认新单容器健康后再执行，且不要删除旧数据卷/目录直到完成恢复演练。

## 已知限制

- 单容器是单点部署，不具备水平扩展、滚动升级或独立数据库故障域。
- PostgreSQL 和应用共享生命周期；必须使用 `docker stop` 等正常停止方式，entrypoint 会先停应用，再以 `fast` 模式停 PostgreSQL。
- `relay` 是原 wg-easy 占位，不是 NEILICO 中继数据面。V2 实现真实 relay/ICE 后再增加独立服务或进程。
- 生产密钥只放在 `$HOME/Documents/Docker/data/neilico/neilico.env`，仓库中的 `.env.example` 只有占位符。

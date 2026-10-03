# syntax=docker/dockerfile:1
#
# NEILICO 单体容器（PostgreSQL + 控制面 API + 内置反代 + Dashboard 静态资源 全在一个容器里）
#
# ⚠️ 构建上下文必须是**仓库根**，因为要在不同 stage 里直接 COPY 两个源码目录：
#     cd <repo> && docker build -f deploy/allinone/Dockerfile -t neilico-allinone:local .
#   compose 里对应 context: ../..  dockerfile: deploy/allinone/Dockerfile
#   （不要用「打包源码成 tar 再喂给构建」的做法：源码一改包就过期，构建的会是旧代码）

# ── stage 1: 前端产物 ────────────────────────────────────────────────
FROM node:22-alpine AS dashboard-build
WORKDIR /src/dashboard
# 先只拷 manifest，利用层缓存：依赖没变时 npm ci 不重跑
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY dashboard/ ./
RUN npm run build

# ── stage 2: 控制面静态二进制 ────────────────────────────────────────
FROM golang:1.27-alpine AS control-build
WORKDIR /src/control-plane
COPY control-plane/go.mod control-plane/go.sum ./
RUN go mod download
COPY control-plane/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/neilico-control-api ./cmd/api \
    && /out/neilico-control-api --help >/dev/null 2>&1 || true

# ── stage 2b: Agent 二进制（供 /downloads/ 分发给一键安装脚本）──────────
#   ⚠️ agent 模块有 `replace neilico/control-plane => ../control-plane`，
#   所以两个源码目录都要在（与 deploy/agent/Dockerfile 一致）。
#   文件名必须与 install.sh / install.ps1 和 public_downloads.go 白名单完全一致：
#   neilico-agent-linux-{amd64,arm64,armv7}
#   neilico-agent-darwin-{amd64,arm64}
#   neilico-agent-windows-amd64.exe
FROM golang:1.27-alpine AS agent-build
WORKDIR /src/agent
COPY agent/go.mod agent/go.sum ./
COPY control-plane/go.mod control-plane/go.sum /src/control-plane/
RUN go mod download
COPY agent/ ./
COPY control-plane/ /src/control-plane/
ARG VERSION=dev
RUN set -eux; \
    mkdir -p /out/downloads; \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-linux-amd64 ./cmd/agent; \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-linux-arm64 ./cmd/agent; \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-linux-armv7 ./cmd/agent; \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-darwin-amd64 ./cmd/agent; \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-darwin-arm64 ./cmd/agent; \
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/downloads/neilico-agent-windows-amd64.exe ./cmd/agent; \
    cd /out/downloads && sha256sum neilico-agent-* > SHA256SUMS && cat SHA256SUMS

# ── stage 3: 运行时（postgres 官方镜像为底座，自带 initdb/pg_isready/psql）──
FROM postgres:16-alpine
# bash(entrypoint) + curl(healthcheck) + su-exec(降权) + tini(信号/PID1)
RUN apk add --no-cache bash curl su-exec tini \
    && addgroup -S neilico \
    && adduser -S -G neilico -h /var/lib/neilico neilico \
    && mkdir -p /opt/neilico/configs /opt/neilico/dashboard /var/lib/neilico /var/lib/postgresql/data /var/log/neilico /usr/local/share/neilico/downloads \
    && chown -R neilico:neilico /var/lib/neilico /opt/neilico/dashboard /usr/local/share/neilico \
    && chown -R postgres:postgres /var/lib/postgresql/data /var/log/neilico

COPY --from=dashboard-build /src/dashboard/dist/ /opt/neilico/dashboard/
COPY --from=control-build /out/neilico-control-api /usr/local/bin/neilico-control-api
COPY --from=agent-build /out/downloads/ /usr/local/share/neilico/downloads/
COPY --from=control-build /src/control-plane/configs/config.example.yaml /opt/neilico/configs/config.example.yaml
COPY deploy/allinone/entrypoint.sh /usr/local/bin/neilico-entrypoint.sh
RUN chmod 0755 /usr/local/bin/neilico-entrypoint.sh /usr/local/bin/neilico-control-api /usr/local/share/neilico/downloads/neilico-agent-*

# 应用侧默认值（数据库连接由 entrypoint 在运行时导出，密码只走环境变量）
ENV PGDATA=/var/lib/postgresql/data \
    POSTGRES_DB=neilico \
    POSTGRES_USER=neilico \
    NEILICO_SERVER_DASHBOARD_DIR=/opt/neilico/dashboard \
    NEILICO_SERVER_DASHBOARD_SPA=true \
    NEILICO_DOWNLOADS_DIR=/usr/local/share/neilico/downloads \
    NEILICO_SERVER_HOST=0.0.0.0

VOLUME ["/var/lib/postgresql/data"]
EXPOSE 8080 8081 5002 8443
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=10 \
    CMD curl -fsS "http://127.0.0.1:${NEILICO_SERVER_PORT:-8080}/healthz" >/dev/null || exit 1

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/neilico-entrypoint.sh"]

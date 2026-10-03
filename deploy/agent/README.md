# NEILICO Agent Docker image

`deploy/agent/Dockerfile` 构建最小 Agent 镜像。入口是 `neilico-agent run`，会从 `NEILICO_TOKEN` 读取一次性接入令牌；首次启动自动 enroll，之后只使用 `/var/lib/neilico-agent/state.json` 中的节点凭据。

## 用官方镜像（推荐）

已发布到 GitHub Container Registry（公开，无需登录）：

```bash
docker pull ghcr.io/aceneil/neilico-agent:latest
```

一键接入：

```bash
docker pull ghcr.io/aceneil/neilico-agent:latest && \
docker run -d --name neilico-agent --restart unless-stopped \
  --network host --cap-add NET_ADMIN --device /dev/net/tun \
  -v neilico-agent-state:/var/lib/neilico-agent \
  -e NEILICO_TOKEN=<TOKEN> ghcr.io/aceneil/neilico-agent:latest
```

## 自行构建（离线 / 自建 registry / 改代码时）

```bash
cd <repo-root>
docker build -f deploy/agent/Dockerfile -t neilico-agent:local .

docker run -d --name neilico-agent --restart unless-stopped \
  --network host --cap-add NET_ADMIN --device /dev/net/tun \
  -v neilico-agent-state:/var/lib/neilico-agent \
  -e NEILICO_TOKEN=<TOKEN> neilico-agent:local
```

> ⚠️ `neilico-agent:local` 只是**本机 tag**，别的机器 `docker pull` 会得到
> `pull access denied`。要让别人也能用，请推到你自己的 registry，或把控制面
> 配置 `enroll.agent_image` 指向已发布的地址。

`--network host`、`CAP_NET_ADMIN` 和 `/dev/net/tun` 用于真实 WireGuard/路由应用。没有这些权限时可加 `--dry-run` 验证配置，但不会建立隧道。令牌只用于首次 enroll，不要写入镜像层或日志；state 卷包含一次性 `agent_token` 与私钥，权限应保持 0600。

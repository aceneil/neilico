# NEILICO Agent 接入（Enroll）

目标是 Cloudflare Tunnel 式接入：在控制面创建一次性接入令牌后，直接复制界面返回的命令。令牌是自包含签名串，包含控制面地址、租户、可选网络和过期时间，不需要再向目标机器解释 server/tenant/network 配置。

> 接入令牌、`agent_token` 和 WireGuard 私钥都是一次性敏感凭据。不要写入日志、审计、镜像层或共享聊天记录；Agent state 文件必须保持 `0600`。

## 1. Linux 一行安装

创建令牌后执行（命令由 `POST /api/v1/enroll-tokens` 的 `commands.linux` 直接返回）：

```bash
curl -fsSL <SERVER>/install.sh | sudo bash -s -- --token <TOKEN>
```

可选参数：

```bash
curl -fsSL <SERVER>/install.sh | sudo bash -s -- \
  --token <TOKEN> --name nas-01 --server https://neilico.example.com
```

脚本会：

1. 从令牌读取 `srv`（也可用 `--server` 覆盖），检测 `amd64` / `arm64` / `armv7`。
2. 下载 `neilico-agent-linux-<arch>`，比对响应头 `X-Neilico-Sha256`。
3. 安装 `/usr/local/bin/neilico-agent`，把令牌以 `0600 root:root` 写入 `/etc/neilico/agent.token`。
4. 写入 systemd unit，`ExecStart=/usr/local/bin/neilico-agent run --token-file /etc/neilico/agent.token`，并执行 `daemon-reload`、`enable --now`、`restart`。
5. 重复执行会升级二进制并重启服务，不会重复创建节点；已有 state 时令牌不再使用。

没有 root、没有 `/dev/net/tun` 或只想检查动作时：

```bash
curl -fsSL <SERVER>/install.sh | bash -s -- --token <TOKEN> --dry-run
```

`--dry-run` 只打印将执行的下载、校验、安装、令牌写入、systemd 步骤，不落盘、不启动服务。脚本使用 POSIX `sh` 写法，`bash -n install.sh` 必须通过。

## 2. Docker 一行接入

创建令牌后执行（命令由 `commands.docker` 直接返回）：

```bash
docker run -d --name neilico-agent --restart unless-stopped \
  --network host --cap-add NET_ADMIN --device /dev/net/tun \
  -v neilico-agent-state:/var/lib/neilico-agent \
  -e NEILICO_TOKEN=<TOKEN> neilico-agent:local
```

镜像定义在 `deploy/agent/Dockerfile`，入口是 `neilico-agent run`。它读取 `NEILICO_TOKEN`，首次启动自动 enroll，随后只使用 state 卷中的凭据。没有 TUN/`NET_ADMIN` 时可改为 `docker run ... neilico-agent:local run --dry-run` 检查配置，但不会建立 WireGuard 隧道。

## 3. 手动二进制接入

先下载带 SHA-256 的二进制（`os` 仅支持 `linux`/`darwin`，`arch` 支持 `amd64`/`arm64`，Linux 另支持 `armv7`）：

```bash
curl -fsSL -D /tmp/neilico.headers \
  -o /tmp/neilico-agent <SERVER>/downloads/neilico-agent-linux-amd64
grep -i '^X-Neilico-Sha256:' /tmp/neilico.headers
sha256sum /tmp/neilico-agent
sudo install -m 0755 /tmp/neilico-agent /usr/local/bin/neilico-agent
```

然后显式 enroll（`--server` 可省略，Agent 从令牌读取）：

```bash
sudo install -d -m 0750 /etc/neilico /var/lib/neilico-agent
sudo sh -c 'umask 077; printf "%s\n" "<TOKEN>" > /etc/neilico/agent.token'
sudo /usr/local/bin/neilico-agent enroll \
  --token-file /etc/neilico/agent.token --name nas-01
sudo /usr/local/bin/neilico-agent run --token-file /etc/neilico/agent.token
```

也可以用环境变量：

```bash
sudo NEILICO_TOKEN=<TOKEN> NEILICO_STATE_DIR=/var/lib/neilico-agent \
  /usr/local/bin/neilico-agent run
```

`enroll` 会把 `node_id`、`agent_token`、WireGuard `private_key`、`virtual_ip` 写入 `/var/lib/neilico-agent/state.json`。之后删除令牌文件不影响运行；重启时 Agent 只读取 state。

## 令牌生命周期与撤销

- 创建：`POST /api/v1/enroll-tokens` 需要 `nodes:write`（JWT admin/ops，或带 `nodes:write` 的 API Token）。响应里的 `token` 和两条命令只出现一次。
- 使用：`POST /api/v1/nodes/enroll` 不需要 JWT，使用令牌自身签名认证。令牌绑定 `tenant_id`、可选 `network_id`、`jti`、`exp`，并按来源 IP 走令牌桶限流。
- 重放：同一 `jti` 和同一请求参数返回同一节点，不新建节点、不重新签发 `agent_token`；首次响应中的凭据不能从服务端恢复。
- 用尽：`used_count >= max_uses` 返回 `410 HTTP Gone`。
- 撤销：`DELETE /api/v1/enroll-tokens/{id}` 幂等；撤销后再次调用返回 `401`。
- 过期或签名篡改：返回 `401`，消息不区分撤销/过期/签名/租户原因，防止枚举。
- 审计：成功调用写入 `action=node.enroll`，记录 token id/jti、node id、network id 和来源 IP；不会记录令牌、`agent_token` 或私钥明文。

## 常见错误

| 状态 | 含义 | 处理 |
| :--- | :--- | :--- |
| `401 invalid_enroll_token` | 令牌格式/签名错误、已撤销、已过期、租户/网络不一致 | 检查令牌是否完整且未撤销；重新创建令牌 |
| `410 enroll_token_exhausted` | `max_uses` 已用尽 | 创建新令牌 |
| `404 not_found` | 下载文件不存在或架构文件未发布 | 检查 `downloads.dir` 和 `neilico-agent-<os>-<arch>` 文件名 |
| `429 rate_limited` | 同一来源 IP 请求过快 | 按 `Retry-After` 退避 |

若机器无 root、无 TUN 或没有 `CAP_NET_ADMIN`，先执行安装脚本 `--dry-run` 或 Agent `run --dry-run`；这不会写入 state、二进制、systemd 或网络配置。真实 WireGuard 应用需要 root/`CAP_NET_ADMIN`、`ip`、`wg` 与 `/dev/net/tun`。

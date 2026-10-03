# NEILICO Agent 接入

接入令牌是自包含 HMAC 令牌，只在创建响应中出现一次。`<TOKEN>`、`agent_token` 和 WireGuard 私钥不得进入日志、审计、镜像层或共享聊天记录；Agent state 文件必须保持 `0600`。

创建令牌后，响应中的 `commands.{linux,macos,windows,docker}` 是四段可直接粘贴的命令。Linux/macOS 命令可以相同，但 API 必须分别返回。

## 1. Linux 一行接入

**权限**：安装、systemd、WireGuard 接口和子网路由需要 root；WireGuard 还需要 `ip`、`wg`、`/dev/net/tun` 或 `CAP_NET_ADMIN`。

```bash
curl -fsSL <SERVER>/install.sh | sudo bash -s -- --token <TOKEN>
```

可选参数：

```bash
curl -fsSL <SERVER>/install.sh | sudo bash -s -- \
  --token <TOKEN> --name nas-01 --server https://neilico.example.com
```

脚本会检测 `amd64` / `arm64` / `armv7`，下载 `neilico-agent-linux-<arch>` 并校验 `X-Neilico-Sha256`，安装 `/usr/local/bin/neilico-agent`，把令牌以 `0600 root:root` 写入 `/etc/neilico/agent.token`，写入 systemd unit 后执行 `daemon-reload`、`enable --now`、`restart`。重复执行会升级二进制并重启服务，不会重复创建节点。

只检查动作：

```bash
curl -fsSL <SERVER>/install.sh | bash -s -- --token <TOKEN> --dry-run
```

`--dry-run` 打印下载、校验、安装、令牌写入和 systemd 命令，不落盘、不启动服务。Linux 分支保持 E1 的 systemd 行为和命令语义。

**能做 / 不能做**：

| 能力 | 能做 | 不能做 / 如实上报 |
| :--- | :--- | :--- |
| 注册、心跳、配置、指标 | 始终运行 | 不会因 Mesh 能力不足而退出 |
| Mesh | 工具、TUN 和权限齐全时创建 WireGuard 接口 | 缺 `wg`、无 TUN 或无权限时上报 `unavailable`/`degraded` 和原因 |
| 子网路由 | root/`CAP_NET_ADMIN` 且有 `ip` 时改路由 | 否则上报 `subnet_routes:"unavailable"` |
| 隧道/代理 | 检测到可用客户端时上报 `tunnel:"ready"` | 未检测到客户端时上报 `unavailable`，不伪造已连通 |

## 2. macOS 一行接入

**权限**：安装 `/usr/local/bin`、`/etc/neilico` 和 `/Library/LaunchDaemons` 需要 sudo/root。支持 `amd64` 与 `arm64`。

```bash
curl -fsSL <SERVER>/install.sh | sudo bash -s -- --token <TOKEN>
```

因为 Darwin 与 Linux 共用 `curl|bash`，`commands.macos` 可以与 `commands.linux` 使用同一行，但会单独返回，并明确要求 `sudo`。

只检查动作：

```bash
curl -fsSL <SERVER>/install.sh | bash -s -- --token <TOKEN> --dry-run
```

macOS 分支下载 `neilico-agent-darwin-{amd64,arm64}`、校验 SHA-256、安装 `/usr/local/bin/neilico-agent`，把令牌以 `0600 root:wheel` 写入 `/etc/neilico/agent.token`，并写入 `/Library/LaunchDaemons/com.neilico.agent.plist`：

- `RunAtLoad=true`、`KeepAlive=true`
- `StandardOutPath` 与 `StandardErrorPath` 均为 `/var/log/neilico-agent.log`
- 新系统优先 `launchctl bootstrap system`，失败或旧系统容错回退 `launchctl load -w`
- 重复执行会替换二进制并重新加载 launchd 服务

非 Linux/Darwin 平台会明确报错，不会静默退出。

**能做 / 不能做**：

| 能力 | 能做 | 不能做 / 如实上报 |
| :--- | :--- | :--- |
| 注册、心跳、配置、指标 | launchd 启动后正常运行 | Mesh 缺失不影响这些面 |
| Mesh | 检测到 WireGuard 系统组件且适配可用时才可建接口 | 没有 WireGuard 系统组件时 `mesh:"unavailable"`；组件存在但适配/权限不足时 `degraded`，必须给原因 |
| 子网路由 | 当前 Linux `ip` 路由适配不覆盖 Darwin | `subnet_routes:"unavailable"`，不执行不支持的系统命令 |
| 隧道/代理 | 有可用客户端时继续隧道/代理面 | 客户端不可用时 `tunnel:"unavailable"` |

## 3. Windows 一行接入

**权限**：必须在“以管理员身份运行”的 PowerShell 中执行；当前分发目标是 Windows Server/Desktop **AMD64**。

```powershell
powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm <SERVER>/install.ps1))) -Token <TOKEN>"
```

参数为 `-Token`（必填）、`-Name`（默认 `$env:COMPUTERNAME`）、`-Server`（可从令牌读取）和 `-DryRun`。只检查动作：

```powershell
powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm <SERVER>/install.ps1))) -Token <TOKEN> -DryRun"
```

脚本会检测架构，下载 `neilico-agent-windows-amd64.exe` 并与 `X-Neilico-Sha256` 比对，安装到 `%ProgramFiles%\NEILICO\`，把令牌写入仅 Administrators/SYSTEM 可访问的 ACL 文件，再用 `New-Service` 注册 `NEILICOAgent` 并启动。已存在服务时先停止、替换二进制、更新服务命令并重启。非管理员会给出明确提示并以非零状态退出。

**能做 / 不能做**：

| 能力 | 能做 | 不能做 / 如实上报 |
| :--- | :--- | :--- |
| 注册、心跳、配置、指标 | Windows Service 正常运行 | Mesh 缺失不影响这些面 |
| Mesh | 只有 WireGuardNT/系统 WireGuard 组件和接口适配都可用时才建接口 | 未检测到系统组件时 `mesh:"unavailable"`；组件存在但需要管理员或适配不足时 `degraded`，必须给原因 |
| 子网路由 | 当前 Agent 未实现 Windows 路由命令 | `subnet_routes:"unavailable"` |
| 隧道/代理 | 有可用客户端时继续隧道/代理面 | 客户端不可用时 `tunnel:"unavailable"` |

Windows/macOS 没有可用 WireGuard 组件时，Agent **只继续注册/心跳/配置/指标以及可用的隧道/代理面**，同时把 Mesh 报成 `unavailable` 或 `degraded`；绝不会假装已经组网。

## 4. Docker 一行接入

**权限**：容器需要 `NET_ADMIN` 与 `/dev/net/tun` 才能真正创建 WireGuard 接口；没有这些能力时只做控制面和其他可用面。

**镜像来源**：`ghcr.io/aceneil/neilico-agent:latest`（公开，目标机直接 pull，无需登录）。
地址可在配置里覆盖（`enroll.agent_image` / `NEILICO_ENROLL_AGENT_IMAGE`）；离线或自建
registry 环境请改成自己的地址，或在本机用 `deploy/agent/Dockerfile` 自行构建。

```bash
docker pull ghcr.io/aceneil/neilico-agent:latest && \
docker run -d --name neilico-agent --restart unless-stopped \
  --network host --cap-add NET_ADMIN --device /dev/net/tun \
  -v neilico-agent-state:/var/lib/neilico-agent \
  -e NEILICO_TOKEN=<TOKEN> ghcr.io/aceneil/neilico-agent:latest
```

镜像入口是 `neilico-agent run`，首次启动读取 `NEILICO_TOKEN` 自动 enroll，之后只使用 state 卷。没有 TUN/`NET_ADMIN` 时可运行 `neilico-agent run --dry-run` 检查配置，但不会建立 WireGuard 隧道。

## 能力模型

注册和每次心跳都可上报：

```json
{
  "capabilities": {
    "mesh": "ready",
    "subnet_routes": "unavailable",
    "tunnel": "ready",
    "reason": "改路由需 root/管理员权限"
  }
}
```

字段约束：

- `mesh`: `ready | unavailable | degraded`
- `subnet_routes`: `ready | unavailable`
- `tunnel`: `ready | unavailable`
- `reason`: 任一能力不是 `ready` 时必须填写，例如 `缺 wg 工具 / 无 TUN / 需管理员权限 / 平台不支持`

Agent 使用可注入的真实探测函数检查工具、TUN/系统组件和管理员权限。能力不足时会明确记录 `mesh 能力不可用：原因` 等日志，但注册、心跳、配置和指标协程继续运行。控制面把能力保存在 `nodes.capabilities`，`GET /api/v1/nodes` 与节点详情都会返回。

## 5. 手动二进制接入

公开下载文件名固定为：

- `neilico-agent-linux-{amd64,arm64,armv7}`
- `neilico-agent-darwin-{amd64,arm64}`
- `neilico-agent-windows-amd64.exe`

每个响应都带 `X-Neilico-Sha256`。Linux 示例：

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

也可以使用 `NEILICO_TOKEN=<TOKEN>`。`enroll` 把 `node_id`、`agent_token`、WireGuard `private_key`、`virtual_ip`、`network_id` 写入 state；之后删除令牌文件不影响运行。

## 令牌生命周期与撤销

- 创建：`POST /api/v1/enroll-tokens` 需要 `nodes:write`。响应里的 `token` 和 `commands.{linux,macos,windows,docker}` 只出现一次。
- 使用：`POST /api/v1/nodes/enroll` 不需要 JWT，由令牌签名认证，并按来源 IP 限流。
- 重放：同一 `jti` 和同一请求参数返回同一节点，不新建节点、不重发凭据。
- 用尽：`used_count >= max_uses` 返回 `410 HTTP Gone`。
- 撤销：`DELETE /api/v1/enroll-tokens/{id}` 幂等；撤销后返回 `401`。
- 审计不会记录令牌、`agent_token` 或私钥明文。

## 常见错误

| 状态 | 含义 | 处理 |
| :--- | :--- | :--- |
| `401 invalid_enroll_token` | 令牌无效、撤销、过期或租户/网络不一致 | 重新创建令牌 |
| `410 enroll_token_exhausted` | `max_uses` 已用尽 | 创建新令牌 |
| `404 not_found` | 下载文件不存在或架构文件未发布 | 检查 `downloads.dir` 与固定文件名 |
| `429 rate_limited` | 同一来源 IP 请求过快 | 按 `Retry-After` 退避 |

安装脚本的 `--dry-run` 不写二进制、令牌、服务或网络配置。真实 WireGuard 应用仍需对应平台的管理员权限、WireGuard 组件/TUN 和路由工具。

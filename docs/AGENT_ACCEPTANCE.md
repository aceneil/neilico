# NEILICO Agent 真机验收清单（Docker / Linux / macOS / Windows）

> 用途：在**真实机器**上验收四种接入方式。控制面侧已在 Linux 上端到端验过（Docker 26/26、
> Linux 脚本 `bash -n` + `--dry-run`）；**macOS 与 Windows 的脚本语法与分支逻辑已验，但真机
> 服务生命周期（launchd / Windows Service）尚未在真机跑过** —— 本文就是补这一步的操作手册。

## 0. 通用准备

1. 控制面地址（本文示例）：`http://192.168.1.10:13000`
2. 登录控制台 → **设备管理** → 右上角「**接入设备**」→ 填名称提示（可选）→ 选网络（可选）→
   有效期 → **生成接入令牌**
3. 弹窗第 2 步有四个页签，**复制相应平台的命令**（命令里已内嵌令牌，一次性、有有效期）
4. 记下令牌的有效期（弹窗里有倒计时），过期的令牌需要重新生成

> ⚠️ 令牌是**一次性凭据**，且**只在生成时显示一次**（离开弹窗就取不到明文）。
> 被用尽 / 撤销 / 过期后，enroll 会分别返回 `410 / 401 / 401`。

## 1. Docker（已实测通过，作为基线）

**执行**（就是界面上复制到的那条）：

**前提**：目标机能访问 `ghcr.io`（或已提前 `docker pull` 过）。镜像由控制面生成，
地址可在 `enroll.agent_image` 覆盖；离线环境请自行 `docker build -f deploy/agent/Dockerfile`。

> **若用 docker compose 部署 agent**：注意 compose 与 `docker run` 的一处差异——
> `docker run -v 名字:/路径` 会自动建命名卷；compose 里**必须在顶层声明 `volumes:`**，
> 否则报 `refers to undefined volume ...: invalid compose project`。用绑定挂载（如
> `./data/neilico-agent:/var/lib/neilico-agent`）可以完全绕开这个坑。本机现成的
> compose 文件见 `$HOME/Documents/Docker/docker-compose.neilico-agent.yaml`。

```bash
docker run -d --name neilico-agent --restart unless-stopped \
  --network host --cap-add NET_ADMIN --device /dev/net/tun \
  -v neilico-agent-state:/var/lib/neilico-agent \
  -e NEILICO_TOKEN=<TOKEN> ghcr.io/aceneil/neilico-agent:latest
```

**判定通过（逐条核对）**：

| # | 检查 | 命令 | 期望 |
| :-- | :--- | :--- | :--- |
| 1 | 容器在跑 | `docker ps --filter name=neilico-agent` | `Up` |
| 2 | 完成自注册 | `docker logs neilico-agent \| grep -i 'identity ready'` | 有 `agent identity ready node_id=...` |
| 3 | 凭据落盘 | `docker exec neilico-agent ls /var/lib/neilico-agent/` | 有 `state.json`（重启不再需要令牌） |
| 4 | **控制面看到在线** | 控制台设备列表 | 节点名 = 容器宿主名，状态 **online** |
| 5 | **分到虚拟 IP** | 设备列表「虚拟 IP」列 | 若生成令牌时选了网络，应有 `100.64.x.y` |
| 6 | 心跳推进 | 隔 35s 刷新两次 | `最后心跳` 时间在变（间隔 30s） |
| 7 | **Mesh 接口** | `docker exec neilico-agent wg show wg0` | 有 `interface: wg0` 与公钥（公钥应与 `state.json` 一致） |
| 8 | 能力上报 | 设备列表「能力」徽标 + 原因 | Linux 上 `Mesh ready`（有 wg+TUN+CAP_NET_ADMIN 时） |

**回收**：`docker rm -f neilico-agent && docker volume rm neilico-agent-state`

## 2. Linux（systemd）

**执行**：
```bash
curl -fsSL http://192.168.1.10:13000/install.sh | sudo bash -s -- --token <TOKEN>
```
先看它要做什么（不落盘、不改系统）：
```bash
curl -fsSL http://192.168.1.10:13000/install.sh | sudo bash -s -- --token <TOKEN> --dry-run
```

**判定通过**：

| # | 检查 | 命令 | 期望 |
| :-- | :--- | :--- | :--- |
| 1 | 服务已起且开机自启 | `systemctl status neilico-agent` | `active (running)`、`enabled` |
| 2 | 二进制就位 | `ls -l /usr/local/bin/neilico-agent` | 存在且可执行 |
| 3 | 令牌文件权限 | `ls -l /etc/neilico/agent.token` | `600 root` |
| 4 | 日志正常 | `journalctl -u neilico-agent -n 30` | 有 `identity ready`，无 panic |
| 5 | 控制面在线 + 心跳 + 虚拟 IP | 控制台设备列表 | 同 Docker 第 4/5/6 条 |
| 6 | Mesh | `sudo wg show wg0` | 有接口与公钥 |
| 7 | 幂等 | 再跑一次同一条命令 | 不报错，服务重启后仍 online |

**回收**：`sudo systemctl disable --now neilico-agent && sudo rm -f /usr/local/bin/neilico-agent /etc/systemd/system/neilico-agent.service /etc/neilico/agent.token && sudo rm -rf /var/lib/neilico-agent && sudo systemctl daemon-reload`

## 3. macOS（launchd）—— **待真机验收**

**执行**（与 Linux 同一条命令；脚本按 `uname -s` 自动走 Darwin 分支）：
```bash
curl -fsSL http://192.168.1.10:13000/install.sh | sudo bash -s -- --token <TOKEN>
```
先 dry-run 看步骤（会打印 launchd plist 路径与 `launchctl bootstrap/kickstart`）：
```bash
curl -fsSL http://192.168.1.10:13000/install.sh | sudo bash -s -- --token <TOKEN> --dry-run
```

**判定通过**：

| # | 检查 | 命令 | 期望 |
| :-- | :--- | :--- | :--- |
| 1 | 二进制就位 | `ls -l /usr/local/bin/neilico-agent` | 存在且可执行（darwin-amd64 / darwin-arm64 按机器架构） |
| 2 | plist 已装 | `ls -l /Library/LaunchDaemons/com.neilico.agent.plist` | 存在 |
| 3 | 服务已加载 | `sudo launchctl print system/com.neilico.agent` | 有 `state = running` |
| 4 | 开机自启 | `sudo launchctl print-disabled system \| grep neilico` | 未被禁用（`RunAtLoad` 生效） |
| 5 | 日志 | `tail -50 /var/log/neilico-agent.log` | 有 `identity ready` |
| 6 | 控制面在线 + 心跳 + 虚拟 IP | 控制台设备列表 | 同 Docker 第 4/5/6 条 |
| 7 | **能力如实上报** | 设备列表能力徽标 | **`Mesh degraded`**，原因含「macOS … 适配不支持」——**这是预期结果，不是故障** |
| 8 | 隧道代理 | 能力徽标 | 装 `npc`/`neilico-tunnel` 才有 `隧道 ready`，否则 `unavailable`（如实） |

**回收**：`sudo launchctl bootout system /Library/LaunchDaemons/com.neilico.agent.plist && sudo rm -f /Library/LaunchDaemons/com.neilico.agent.plist /usr/local/bin/neilico-agent /etc/neilico/agent.token && sudo rm -rf /var/lib/neilico-agent`

## 4. Windows（Windows Service）—— **待真机验收**

**执行**（管理员 PowerShell）：
```powershell
powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm http://192.168.1.10:13000/install.ps1))) -Token <TOKEN>"
```
先 dry-run：在上面命令末尾加 ` -DryRun`。

**判定通过**：

| # | 检查 | 命令（管理员 PowerShell） | 期望 |
| :-- | :--- | :--- | :--- |
| 1 | 服务已注册并运行 | `Get-Service neilico-agent` | `Running`，启动类型 `Automatic` |
| 2 | 可执行文件 | `Test-Path "$env:ProgramFiles\NEILICO\neilico-agent.exe"` | `True` |
| 3 | 服务路径正确 | `(Get-CimInstance Win32_Service -Filter "Name='neilico-agent'").PathName` | 指向上面的 exe |
| 4 | 令牌文件 | `Test-Path "$env:ProgramData\NEILICO\agent.token"` | `True`（ACL 限管理员） |
| 5 | 控制面在线 + 心跳 + 虚拟 IP | 控制台设备列表 | 同 Docker 第 4/5/6 条 |
| 6 | **能力如实上报** | 设备列表能力徽标 | **`Mesh degraded`** + 原因（Windows 接口适配未支持）——**预期结果** |
| 7 | 幂等 | 重跑安装命令 | 停服务→换二进制→重启，仍 online |
| 8 | 失败要可诊断 | 用**非管理员** PowerShell 跑 | 明确提示需要管理员并**非零退出**（不静默） |

**回收**：`Stop-Service neilico-agent; sc.exe delete neilico-agent; Remove-Item -Recurse -Force "$env:ProgramFiles\NEILICO","$env:ProgramData\NEILICO"`

## 5. 常见错误对照

| 现象 | 含义 | 处理 |
| :--- | :--- | :--- |
| enroll 返回 **401** | 令牌签名不对 / 已撤销 / 已过期 | 重新生成令牌 |
| enroll 返回 **410** | 令牌用尽（`max_uses` 已到） | 该令牌不能再用于新设备；重新生成 |
| `GET /downloads/...` **404** | 该平台二进制不在镜像里 | 检查控制面启动日志的 `agent downloads ready` 与目录 |
| 服务起不来、日志 `permission denied` | 缺 root/管理员 | 用 `sudo`（Linux/macOS）或管理员 PowerShell（Windows） |
| 能力显示 `Mesh unavailable`（原因「无 TUN」/「缺 wg 工具」） | 宿主缺内核或工具 | Linux 装 `wireguard-tools`、确认 `/dev/net/tun`、容器加 `--cap-add NET_ADMIN --device /dev/net/tun` |
| 控制面看不到节点 | 网络不通 / 令牌过期 | 从目标机 `curl http://192.168.1.10:13000/healthz` 验证连通性 |

## 6. 控制面侧核对（与界面互相印证）

```bash
# 节点是否在线、能力是什么
curl -s -H "Authorization: Bearer <ADMIN_JWT>" \
  'http://192.168.1.10:13000/api/v1/nodes?page_size=100' | jq '.items[] | {name,status,virtual_ip,capabilities}'

# 网络成员（虚拟 IP 分配）
curl -s -H "Authorization: Bearer <ADMIN_JWT>" \
  'http://192.168.1.10:13000/api/v1/networks/<NETWORK_ID>/members' | jq
```

## 7. 明确的能力边界（不要在验收时误判为故障）

- **Windows / macOS 的 Mesh 目前是 `degraded`**：Agent 的 WireGuard 接口创建适配尚未支持这两个平台，
  会**如实上报原因**。这两端当前可用于注册 / 心跳 / 配置下发 / 隧道代理；
- **中继（relay）未实现**：P2P 打洞失败时没有中继兜底；
- `p2p_success_rate` / `relay_bytes` 恒为 0（无采集器）；
- **Linux 是唯一 Mesh 全能力平台**（需要 `wg` 工具或 wgctrl + `/dev/net/tun` + NET_ADMIN/root）。

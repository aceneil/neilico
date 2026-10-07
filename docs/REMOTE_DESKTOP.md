# 远程桌面（自建 RustDesk）

NEILICO 的「远程桌面」能力：控制面下发**自建 RustDesk 服务器**（`rustdesk-server`：`hbbs` 信令 +
`hbbr` 中继）的接入参数，Dashboard 展示服务器参数、客户端安装指引与设备网格，并支持一键复制连接
参数、一键调起本地 RustDesk 发起连接。

参考上游：[rustdesk/rustdesk](https://github.com/rustdesk/rustdesk)（客户端）、
[rustdesk/rustdesk-server](https://github.com/rustdesk/rustdesk-server)（hbbs/hbbr 服务端）。

> 本页只覆盖 **控制面 + Dashboard** 的 NEILICO 侧集成。RustDesk **服务端**（hbbs/hbbr）由独立的部署
> 环节（P1）用 Docker 常驻，见下文「与 P1 部署的对应关系」。Flutter 客户端不在本轮范围。

## 组成

| 部分 | 位置 | 说明 |
| :--- | :--- | :--- |
| 控制面接口 | `control-plane/internal/api/remote_desktop.go` | `/api/v1/remote-desktop/*` |
| 服务/配置 | `control-plane/internal/service/remote_desktop.go`、`internal/config/config.go` | 参数解析、公钥读取、设备视图、端口探活 |
| Dashboard 页 | `dashboard/src/pages/remote-desktop/RemoteDesktopPage.vue` | 侧栏「远程桌面」 |
| 前端 API | `dashboard/src/api/remote-desktop.ts` | |

## 快速启用

1. **部署 rustdesk-server（P1）**：在 Docker 里跑 `hbbs` + `hbbr`，暴露 `21115/21116/21117/21118/21119`
   （TCP；`21116` 另需 UDP）。首次启动会生成密钥对，其中 `id_ed25519.pub` 是**公钥**。
2. **让控制面能读到公钥**：把 `id_ed25519.pub` 挂载进控制面容器，并把路径用
   `NEILICO_RD_PUBLIC_KEY_FILE` 指过去（**只读公钥，绝不挂载/读取私钥**）。
3. **设置服务器地址**：`NEILICO_RD_ID_SERVER`（hbbs）与 `NEILICO_RD_RELAY_SERVER`（hbbr）。
4. 打开侧栏「远程桌面」，把页面上的 **ID 服务器 / 中继服务器 / Key 公钥** 填进各设备的 RustDesk 客户端。

### 环境变量（均可用 YAML `remote_desktop:` 段覆盖）

| 变量 | 默认值 | 含义 |
| :--- | :--- | :--- |
| `NEILICO_RD_ENABLED` | `true` | 是否启用远程桌面能力 |
| `NEILICO_RD_ID_SERVER` | （留空，由部署机下发） | hbbs 信令服务器（`host` 或 `host:port`）；示例 `192.168.1.10` |
| `NEILICO_RD_RELAY_SERVER` | （留空，由部署机下发） | hbbr 中继服务器（`host` 或 `host:port`）；示例 `your-server.example.com` |
| `NEILICO_RD_PUBLIC_KEY_FILE` | （留空，由部署机下发） | **公钥**文件路径（`id_ed25519.pub`）；示例 `$HOME/Documents/Docker/data/rustdesk/id_ed25519.pub` |
| `NEILICO_RD_PORTS` | `21115,21116,21117,21118,21119` | 需要暴露/探活的端口列表 |

YAML 形态见 `control-plane/configs/config.example.yaml` 的 `remote_desktop:` 段；
单容器部署的 env 示例见 `deploy/allinone/.env.example`。

### 端口含义

| 端口 | 归属 | 用途 |
| :--- | :--- | :--- |
| `21115` | hbbs | NAT 类型测试 |
| `21116` | hbbs | **ID 注册/信令**（TCP + UDP） |
| `21117` | hbbr | **中继转发**（P2P 打不通时走这里） |
| `21118` / `21119` | hbbs | Web 客户端（`/ws`，可选） |

## 接口

全部需要登录；`config` 的 `PUT` 仅平台管理员（`platform_admin`）；设备授权 `PATCH` 允许
`platform_admin` / `tenant_admin`。

| 方法 | 路径 | 权限 | 说明 |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/remote-desktop/config` | 登录可读 | 服务器参数，**只含公钥** |
| `PUT` | `/api/v1/remote-desktop/config` | 仅 `platform_admin` | 改 `id_server` / `relay_server` / `enabled`，写审计 |
| `GET` | `/api/v1/remote-desktop/devices` | 登录可读 | 设备列表 + 每台的 `rustdesk_hint` / 连接参数文本 |
| `GET` | `/api/v1/remote-desktop/status` | 登录可读 | 对 `21115/21116/21117` 做纯 TCP 探活（1s 超时，总预算 3s） |
| `GET` | `/api/v1/remote-desktop/device-policies` | 登录可读（限自身租户） | 每台设备的授权状态；未建过策略的节点返回默认值 |
| `PATCH` | `/api/v1/remote-desktop/device-policies/{node_id}` | `platform_admin` / `tenant_admin` | 局部更新授权开关，写审计 `remote_desktop.policy.update` |

`GET /config` 响应示例：

```json
{
  "enabled": true,
  "id_server": "192.168.1.10",
  "relay_server": "your-server.example.com",
  "public_key": "<id_ed25519.pub 的内容>",
  "available": true,
  "hint": "服务器已就绪：在 RustDesk 客户端「ID/中继服务器」填入 192.168.1.10，并把上方公钥填入「Key」。",
  "ports": [21115, 21116, 21117, 21118, 21119]
}
```

```bash
TOKEN=$(curl -fsS -X POST http://<host>:13000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@neilico.local","password":"<admin-password>"}' | jq -r .token)

curl -fsS http://<host>:13000/api/v1/remote-desktop/config  -H "Authorization: Bearer $TOKEN"
curl -fsS http://<host>:13000/api/v1/remote-desktop/devices -H "Authorization: Bearer $TOKEN"
curl -fsS http://<host>:13000/api/v1/remote-desktop/status  -H "Authorization: Bearer $TOKEN"
```

### 「服务器未就绪」

公钥文件缺失/不可读时，接口**不报 500**：返回 `available:false`、`public_key:""`，并在 `hint` 里给出
原因。Dashboard 顶部卡片显示黄色「服务器未就绪」提示。完成 P1 部署或改对
`NEILICO_RD_PUBLIC_KEY_FILE` 后即变绿。

## 设备授权策略（device-policies）

面向公网的产品**默认拒绝**：`remote_control_allowed` 默认 `false`（opt-in），未建过策略的节点也按
默认值返回。策略**真实持久化**在 `remote_desktop_device_policies` 表（AutoMigrate 建表）。

`GET /device-policies` 响应形状（`items` / `total` 与 `/devices` 一致）：

```json
{
  "items": [
    {
      "node_id": "uuid",
      "remote_control_allowed": false,
      "tunnel_mode": "auto",
      "isolated_tunnel": { "enabled": false, "stream_rule_id": null },
      "mesh": { "joined": true, "network_id": "uuid", "virtual_ip": "10.42.0.7" },
      "readonly": { "subnet_routes": "ready" }
    }
  ],
  "total": 1
}
```

字段语义：

| 字段 | 取值 | 说明 |
| :--- | :--- | :--- |
| `remote_control_allowed` | `bool`（默认 `false`） | 被控方授权开关。为 `false` 时 NEILICO 客户端不得对其发起连接 |
| `tunnel_mode` | `auto` / `direct` / `relay` | 直连（走 Mesh 虚拟 IP）/ 中继（hbbr）/ 自动；默认 `auto` |
| `isolated_tunnel.enabled` | `bool` | 单独隧道；复用现有 **StreamRule**（不另造转发引擎），`stream_rule_id` 指向那条规则 |
| `mesh.joined` / `mesh.network_id` / `mesh.virtual_ip` | `bool` / `uuid\|null` / `string\|null` | Mesh 成员身份，**读自现有 `NetworkMember`**（不新造数据源） |
| `readonly.subnet_routes` | `ready` / `degraded` / `unavailable` | 只读；由现有 `SubnetRoute` 派生（有启用→ready，全禁用→degraded，无→unavailable） |

`PATCH /device-policies/{node_id}` 是**局部更新**（只改传入字段）：

```json
{ "remote_control_allowed": false }
{ "tunnel_mode": "direct" }
{ "isolated_tunnel_enabled": true }
{ "mesh_joined": true }
```

- `isolated_tunnel_enabled=true`：在已发布的端口转发区间内自动分配端口，用现有 StreamRule 能力建一条
  `<node_id>:21118`（RustDesk 直连端口）的 tcp 转发规则；`false` 则删除该规则。设备必须已分配虚拟 IP，否则 `400`。
- `mesh_joined=true`：复用现有虚拟网络成员能力加入；租户只有一个网络时自动选中，多个网络需带
  `mesh_network_id`，没有网络则 `400`。`false` 则退出（移除成员关系）。
- 权限：`platform_admin` / `tenant_admin` 可改；其它角色 `403`；节点不存在 `404`；非法枚举 `400`。
- 写审计：`action = remote_desktop.policy.update`，`detail` 记录改了哪些字段。

```bash
curl -fsS http://<host>:13000/api/v1/remote-desktop/device-policies -H "Authorization: Bearer ***"
curl -fsS -X PATCH http://<host>:13000/api/v1/remote-desktop/device-policies/<node_id> \
  -H "Authorization: Bearer ***" -H 'Content-Type: application/json' \
  -d '{"remote_control_allowed": true, "tunnel_mode": "direct"}'
```

### 限制（务必如实理解）

- 本轮交付的是 **NEILICO 侧的策略开关**：它决定「对我们的客户端与用户可见性」是否允许被远程，并在
  Dashboard / 桌面客户端上如实呈现；**它本身不是 RustDesk 内核层的硬拦截**。也就是说，在 RustDesk
  **内核接入（`rust-core`）阶段**落地之前，一个绕过 NEILICO 客户端、直接用 RustDesk 客户端 + 同一把公钥
  的连接仍可能建立——控制面目前无法在内核层强制阻断。
- 因此：`remote_control_allowed=false` 应理解为「NEILICO 客户端会拒绝发起、界面显示为不可远程」，
  **不是**「网络层已强制阻断」。内核级强制拦截作为后续 `rust-core` 阶段交付物，不在本轮。
- 单独隧道复用 StreamRule，转发目标为该节点的虚拟 IP；Mesh 身份复用 NetworkMember——两者都不是新造的
  独立数据源，行为与「端口转发」「虚拟网络」页保持一致。

## 设备与 RustDesk ID

`GET /devices` 复用现有节点数据（`name` / `status` / `virtual_ip` / `last_seen` / `heartbeat_stale` /
`os`+`arch`），并为每台设备派生：

- `rustdesk_id` / `rustdesk_hint`：该设备上报的 RustDesk ID（没有则为空串）；
- `connect_url`：`rustdesk://<id>`，Dashboard 的「发起连接」按钮用它调起本地客户端；
- `connection_params`：可直接粘贴的连接参数文本（设备名 / 虚拟 IP / RustDesk ID / 两个服务器 / Key）。

**ID 上报方式（当前）**：给节点打一个标签 `rustdesk:<id>`（大小写不敏感）。这是客户端原生上报字段
落地前的最小约定；未上报时按钮置灰并提示「需该设备安装 RustDesk 并告知 ID」。

## 与 P1 部署的对应关系

- P1 在本机 Docker 部署 `rustdesk-server`，产出密钥对：
  - `.../data/rustdesk/id_ed25519`（**私钥，仅服务端持有**）
  - `.../data/rustdesk/id_ed25519.pub`（**公钥，客户端与 NEILICO 使用**）
- NEILICO 控制面**只读** `id_ed25519.pub`（默认 `NEILICO_RD_PUBLIC_KEY_FILE`），把它通过
  `GET /config` 下发给客户端填写到「Key」。
- 服务器地址**不硬编码进仓库**：代码默认留空，由部署机用 `NEILICO_RD_ID_SERVER` /
  `NEILICO_RD_RELAY_SERVER` 下发（示例 `192.168.1.10` / `your-server.example.com`），端口 `21116`（hbbs）/
  `21117`（hbbr）。未配置时接口返回 `available:false` 并在 `hint` 里说明「未配置服务器地址」。
- **不要**把私钥文件挂载给控制面；`NEILICO_RD_PUBLIC_KEY_FILE` 只应指向 `.pub`。

## 安全说明（务必如实理解）

- **信令与中继在我们自建的服务器上**：设备的 ID 注册/发现（hbbs）与打不通 P2P 时的中继（hbbr）都走
  NEILICO 侧的机器，不经过 RustDesk 官方公共中继。
- **端到端画面加密由 RustDesk 协议负责**：屏幕画面的加解密由客户端之间的 RustDesk 协议完成（公钥校验 +
  会话加密）。服务器（含中继）只转发**已加密**的流量，**不持有会话密钥**，也看不到明文画面。NEILICO 的
  控制面只下发公钥，从不参与画面加解密。
- **控制面绝不接触私钥**：NEILICO 只读取并下发 `.pub`；服务端私钥 `id_ed25519` 仅存在于 P1 的部署目录，
  代码里没有任何读取私钥的路径，接口与日志也不回显任何密钥。
- **风险与注意**：
  - `Key`（公钥）用于客户端校验 ID/中继服务器，务必在客户端的「Key」字段填对，防止连到仿冒中继。
  - `21115–21119` 是新的对外暴露面：公网部署时只放行必要端口，并可用防火墙把 hbbs/hbbr 限制在可信来源。
  - RustDesk 的加密保护画面，但**不替代**主机侧的访问控制（账号、锁屏、白名单等）。

## 遗留 / 未做（如实列出）

- **`/config` 覆盖仍是内存态**：`PUT /config` 的改动只在当前控制面进程内生效（重启回落到
  `NEILICO_RD_*` / YAML）；需要长期固化请用环境变量。设备授权策略（`device-policies`）**不是**这种情况，
  它是真持久化的（新表 `remote_desktop_device_policies`）。
- **RustDesk 内核层的硬拦截未做**：`remote_control_allowed=false` 目前是 NEILICO 侧策略，
  RustDesk 内核级强制阻断要等内核接入（`rust-core`）阶段，见上文「限制」。
- **RustDesk ID 原生上报**未做：目前靠节点标签 `rustdesk:<id>`，待客户端接入后改为原生字段。

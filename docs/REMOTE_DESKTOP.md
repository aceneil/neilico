# 远程桌面（自建 RustDesk）

NEILICO 的「远程桌面」能力：控制面下发**自建 RustDesk 服务器**（`rustdesk-server`：`hbbs` 信令 +
`hbbr` 中继）的接入参数，Dashboard 只做**设备与策略管理**——展示服务器参数、客户端安装指引与设备网格，
并支持一键复制连接参数。**Web 只做管理，远程连接一律由 NEILICO 客户端发起；Web 不提供任何连接入口。**

参考上游：[rustdesk/rustdesk](https://github.com/rustdesk/rustdesk)（客户端）、
[rustdesk/rustdesk-server](https://github.com/rustdesk/rustdesk-server)（hbbs/hbbr 服务端）。

> 本页覆盖 **控制面 + Dashboard** 的 NEILICO 侧集成。RustDesk **服务端**（hbbs/hbbr）已
> **vendored 进本仓**并由 allinone 镜像**自行编译**、由控制面**按需拉起**（不再依赖外部容器/镜像），
> 见下文「自建与按需模式」。Flutter 客户端不在本轮范围。

## 组成

| 部分 | 位置 | 说明 |
| :--- | :--- | :--- |
| 控制面接口 | `control-plane/internal/api/remote_desktop.go` | `/api/v1/remote-desktop/*` |
| 服务/配置 | `control-plane/internal/service/remote_desktop.go`、`internal/config/config.go` | 参数解析、公钥读取、设备视图、端口探活 |
| Dashboard 页 | `dashboard/src/pages/devices/DevicesPage.vue`（页头 + 设备列表）+ `dashboard/src/pages/nodes/NodesPage.vue`（列表内「远程」列 + 详情抽屉） | 侧栏「设备管理」**单一视图**（无页内标签）：列表「远程」列只展示「可被远程」开关 + 一行「连接请在 NEILICO 客户端中发起」提示（**Web 不提供连接入口**）；四个策略开关（可被远程 / 隧道模式 / 单独隧道 / Mesh）在详情抽屉里即时 PATCH；全局参数在页头「远程桌面设置」弹窗（`remote-desktop/RemoteDesktopSettingsModal.vue`）。旧深链 `/remote-desktop` 与 `/devices?tab=remote` 均落到 `/devices` |
| 前端 API | `dashboard/src/api/remote-desktop.ts` | |

## 自建与按需模式（本轮）

自 RustDesk 服务端**进入本仓自行构建**后，hbbs/hbbr 不再是常驻的外部容器，而是**控制面的子进程**，
默认**按需启动**。

### 工作原理

- **源码内置**：`third_party/rustdesk-server/`（tag `1.1.16` + 展平的 `hbb_common`）随仓；
  `vendor.tar.gz` 内含全部 cargo 依赖，`deploy/allinone/Dockerfile` 的 `rustdesk-build` 阶段
  **完全离线**（`cargo --offline`）编译出 `hbbs`/`hbbr`，装进最终镜像 `/usr/local/bin/`。镜像仍是**单容器**。
- **按需生命周期**（`RemoteDesktopServer` 进程监管模块，`control-plane/internal/service/rustdesk_server.go`）：
  - `on_demand`（默认）：**无远程桌面活动时不启动** —— 不监听 `21115-21119`、进程数 0。
  - **触发**：设备策略被启用（`PATCH device-policies` 里 `remote_control_allowed` / `isolated_tunnel_enabled`
    / `mesh_joined` 任一置真）时**自动拉起**；admin 也可在 Dashboard 手动「启动」。
  - **空闲回收**：无活动超过 `NEILICO_RD_IDLE_TIMEOUT`（默认 10m）后**自动停止**。手动启动为「手动保持」，
    不受空闲回收影响，直到手动「停止」。
  - `always_on`：控制面启动即拉起并常驻（仍可被 admin 手动停）。
  - `off`：永不启动；触发为 no-op，手动 start 返回 `409 server_disabled`。
- **启动参数**：hbbs 以工作目录=密钥目录启动（**只有服务端读私钥**），可带 `-r <relay_host>`；hbbr 以
  `-k _` 启动（从 `id_ed25519` 取 Key 并**强制校验客户端**，公网防滥用）。子进程日志写在密钥目录的
  `hbbs.log`/`hbbr.log`（mode 600），**私钥绝不进日志或接口响应**。

### 端口

| 端口 | 归属 | 用途 | 协议 |
| :--- | :--- | :--- | :--- |
| `21115` | hbbs | NAT 类型测试 | TCP |
| `21116` | hbbs | **ID 注册/信令** | TCP **+ UDP** |
| `21117` | hbbr | **中继转发**（P2P 打不通时走这里） | TCP |
| `21118` | hbbs | 直连 / Web 客户端 | TCP |
| `21119` | hbbs | Web 客户端（`/ws`） | TCP |

共 6 个发布端点（`21116` 兼 TCP/UDP）。**按需模式下未启用时这些端口不监听是正常的。**

### 密钥位置与迁移铁律

- 密钥目录由 `NEILICO_RD_KEY_DIR` 指定（默认 `/var/lib/neilico/rustdesk`，allinone 部署里绑定挂载
  宿主 `data/neilico/rustdesk/`），权限 **700**，密钥文件 **600**。
- hbbs 首次启动自动生成 `id_ed25519`（**私钥**）与 `id_ed25519.pub`（**公钥**）；hbbr 与 hbbs 共用同一目录，
  读到的是**同一把** Key。
- **铁律**：
  1. **私钥 `id_ed25519` 永不出该目录**——不挂载给别的服务、不入库、不贴聊天、不进日志。
  2. 控制面**只读 `id_ed25519.pub`**（`GET /config` 只下发公钥；仓库里没有任何读取私钥的代码路径）。
  3. **迁移/换机**：整目录（`id_ed25519` + `id_ed25519.pub` + `db_v2.sqlite3`）一起搬；只搬公钥会导致
     客户端校验失败、服务端签名自相矛盾。旧外部部署的密钥目录可直接迁到新目录，**保持权限 700/600**。

### 切成 always_on

把 `NEILICO_RD_SERVER_MODE` 设为 `always_on`（或 YAML `remote_desktop.server_mode: always_on`）并重启控制面，
服务端即常驻；想临时常驻又不想改配置，可在「远程桌面设置」弹窗点「启动」（手动保持）。切回按需用 `on_demand`。

## 快速启用

1. **服务端已内置**：hbbs/hbbr 由 allinone 镜像从 `third_party/rustdesk-server/` 自行编译，
   无需单独部署；镜像发布 `21115/21116/21117/21118/21119`（`21116` 兼 UDP）。首次启动（或首次被触发）
   会生成密钥对，其中 `id_ed25519.pub` 是**公钥**（落在 `NEILICO_RD_KEY_DIR`）。
2. **让控制面能读到公钥**：绑定挂载密钥目录并把 `NEILICO_RD_KEY_DIR` 指过去（`NEILICO_RD_PUBLIC_KEY_FILE`
   留空即可自动派生 `<key_dir>/id_ed25519.pub`）。**只读公钥，绝不挂载/读取私钥**。
3. **设置服务器地址**：`NEILICO_RD_ID_SERVER`（hbbs）与 `NEILICO_RD_RELAY_SERVER`（hbbr）。
4. 打开侧栏「设备管理」→ 点页头「远程桌面设置」，把弹窗里的 **ID 服务器 / 中继服务器 / Key 公钥** 填进各设备的 RustDesk 客户端；在设备列表「远程」列（或设备详情抽屉）逐台开启「可被远程」。**远程连接请到 NEILICO 客户端发起**——Web 端只做管理与策略开关，不提供连接入口。

### 环境变量（均可用 YAML `remote_desktop:` 段覆盖）

| 变量 | 默认值 | 含义 |
| :--- | :--- | :--- |
| `NEILICO_RD_ENABLED` | `true` | 是否启用远程桌面能力 |
| `NEILICO_RD_ID_SERVER` | （留空，由部署机下发） | hbbs 信令服务器（`host` 或 `host:port`）；示例 `192.168.1.10` |
| `NEILICO_RD_RELAY_SERVER` | （留空，由部署机下发） | hbbr 中继服务器（`host` 或 `host:port`）；示例 `your-server.example.com` |
| `NEILICO_RD_PUBLIC_KEY_FILE` | （留空，由部署机下发） | **公钥**文件路径（`id_ed25519.pub`）；留空则从 `NEILICO_RD_KEY_DIR` 派生 `<key_dir>/id_ed25519.pub` |
| `NEILICO_RD_PORTS` | `21115,21116,21117,21118,21119` | 需要暴露/探活的端口列表 |
| `NEILICO_RD_SERVER_MODE` | `on_demand` | hbbs/hbbr 生命周期：`on_demand` / `always_on` / `off` |
| `NEILICO_RD_IDLE_TIMEOUT` | `10m` | on_demand 模式下的空闲回收阈值（Go 时长语法） |
| `NEILICO_RD_KEY_DIR` | `/var/lib/neilico/rustdesk` | 密钥目录（`id_ed25519` + `.pub`，权限 700/600） |
| `NEILICO_RD_HBBS_PATH` / `NEILICO_RD_HBBR_PATH` | `/usr/local/bin/hbbs` / `hbbr` | 自编译二进制路径 |
| `NEILICO_RD_RELAY_PORT` / `NEILICO_RD_UDP_PORT` | `21117` / `21116` | hbbr 中继端口 / hbbs 的 UDP 端口 |
| `NEILICO_RD_RELAY_HOST` | （留空） | 非空则给 hbbs 传 `-r <relay_host>` |

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
| `GET` | `/api/v1/remote-desktop/devices` | 登录可读 | 设备列表 + 每台的 `rustdesk_hint` / 连接参数文本（供客户端或手动填写，Web 不据此发起连接） |
| `GET` | `/api/v1/remote-desktop/status` | 登录可读 | 对 `21115/21116/21117` 做纯 TCP 探活（1s 超时，总预算 3s） |
| `GET` | `/api/v1/remote-desktop/device-policies` | 登录可读（限自身租户） | 每台设备的授权状态；未建过策略的节点返回默认值 |
| `PATCH` | `/api/v1/remote-desktop/device-policies/{node_id}` | `platform_admin` / `tenant_admin` | 局部更新授权开关，写审计 `remote_desktop.policy.update` |
| `GET` | `/api/v1/remote-desktop/server-status` | 登录可读 | 自托管服务端状态：运行中/已停止 + 监听端口 + 最近活动时间 + 空闲倒计时（`mode`/`running`/`manual`/`ports`/`idle_remaining_seconds`） |
| `POST` | `/api/v1/remote-desktop/server/start` | `platform_admin` / `tenant_admin` | 手动拉起（进入「手动保持」，不受空闲回收）；`mode=off` 时 `409 server_disabled` |
| `POST` | `/api/v1/remote-desktop/server/stop` | `platform_admin` / `tenant_admin` | 手动停止自托管服务端 |

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

curl -fsS http://<host>:13000/api/v1/remote-desktop/config  -H "Authorization: Bearer ***"
curl -fsS http://<host>:13000/api/v1/remote-desktop/devices -H "Authorization: Bearer ***"
curl -fsS http://<host>:13000/api/v1/remote-desktop/status  -H "Authorization: Bearer ***"

# 自托管服务端状态与手动开关（start/stop 需 admin）。
curl -fsS http://<host>:13000/api/v1/remote-desktop/server-status -H "Authorization: Bearer ***"
curl -fsS -X POST http://<host>:13000/api/v1/remote-desktop/server/start -H "Authorization: Bearer ***"
curl -fsS -X POST http://<host>:13000/api/v1/remote-desktop/server/stop  -H "Authorization: Bearer ***"
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
- `connect_url`：客户端连接深链；**Web 侧已不再使用**（连接一律由客户端发起）；
- `connection_params`：可直接粘贴的连接参数文本（设备名 / 虚拟 IP / RustDesk ID / 两个服务器 / Key）。

**ID 上报方式（当前）**：给节点打一个标签 `rustdesk:<id>`（大小写不敏感）。这是客户端原生上报字段
落地前的最小约定；未上报时 Web 不展示连接入口（连接本就在客户端发起），客户端会提示「需该设备安装 RustDesk 并告知 ID」。

## 与 P1 部署的对应关系（已由「自建与按需模式」取代）

> **注意**：下文的「P1 外部部署」是历史形态。自本轮起 hbbs/hbbr 已 **vendored 进本仓、由 allinone 镜像
> 自行编译、由控制面按需拉起**，不再依赖外部容器/镜像。旧的外部 `docker-compose.rustdesk.yaml`
> **仅作回滚保留**，文档不再推荐使用。以下描述保留以便理解迁移。

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

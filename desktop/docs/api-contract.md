# 客户端 ↔ 控制面 接口约定（远程桌面 / 设备授权）

本文件是 `desktop/` 与 `control-plane` 之间的契约。客户端已按此实现，
**后端就绪后无需改动客户端即可点亮开关面板**；未就绪时客户端按约定置灰并说明原因。

## 已就绪（控制面 P2 已提供，客户端已对接）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | 入参 `{email, password}`；返回 `{token, refresh_token, user{id,email,role,tenant_id}}` |
| `POST` | `/api/v1/auth/refresh` | 入参 `{refresh_token}`；返回同上 |
| `GET` | `/api/v1/remote-desktop/config` | `{enabled, id_server, relay_server, public_key, available, hint, ports[]}`（**只含公钥**） |
| `GET` | `/api/v1/remote-desktop/devices` | `{items[], total}`，元素见下 |
| `GET` | `/api/v1/remote-desktop/status` | 端口探活 `{id_server_host, relay_server_host, ports[], reachable, checked_at}` |

`devices.items[]` 字段（客户端 `RemoteDesktopDevice` 依赖）：

```json
{
  "id": "uuid", "name": "办公室主机", "status": "online|offline",
  "virtual_ip": "10.42.0.7", "last_seen": "2026-10-07T02:00:00Z",
  "heartbeat_stale": false, "platform": "linux/x86_64",
  "os": "linux", "arch": "x86_64",
  "rustdesk_id": "123456789", "rustdesk_hint": "123456789",
  "connect_url": "rustdesk://123456789",
  "connection_params": "设备: ...\nKey: <公钥>"
}
```

## 需要的接口（后端尚未提供 —— 这是本轮的「接口清单」）

> 客户端已实现调用与置灰逻辑：现在打 `/device-policies` 会拿到 404，
> 客户端显示「后端接口未就绪」并禁用全部开关；后端一旦实现，功能自动点亮。

### 1. 读取每台设备的授权状态

```
GET /api/v1/remote-desktop/device-policies
Authorization: Bearer <token>          # 任何登录用户可读（只读自己的租户）
```

```json
{
  "items": [
    {
      "node_id": "uuid",
      "remote_control_allowed": true,
      "tunnel_mode": "auto",
      "isolated_tunnel": { "enabled": false, "stream_rule_id": null },
      "mesh": { "joined": true, "network_id": "uuid|null", "virtual_ip": "10.42.0.7" },
      "readonly": { "subnet_routes": "ready" }
    }
  ],
  "total": 1
}
```

字段语义：

| 字段 | 取值 | 说明 |
| --- | --- | --- |
| `remote_control_allowed` | `bool` | 被控方授权开关。**为 false 时任何客户端都不得对其发起连接**；未提供时客户端按 `true` 处理（向后兼容） |
| `tunnel_mode` | `"auto"` / `"direct"` / `"relay"` | 直连（走 Mesh 虚拟 IP）/ 中继（hbbr）/ 自动。客户端把 `p2p`、`mesh` 也解析为 `direct`，`relayed` 解析为 `relay` |
| `isolated_tunnel.enabled` | `bool` | 单独隧道（复用现有 StreamRule，不另造一套） |
| `isolated_tunnel.stream_rule_id` | `string\|null` | 关联的转发规则 ID |
| `mesh.joined` / `mesh.network_id` | `bool` / `string\|null` | Mesh 成员身份；`network_id` 为空且未加入时客户端置灰 Mesh 开关并说明「未加入任何虚拟网络」 |
| `readonly.subnet_routes` | `"ready"`/`"degraded"`/`"unavailable"` | 子网路由状态（只读展示） |

### 2. 修改单台设备的授权状态

```
PATCH /api/v1/remote-desktop/device-policies/{node_id}
Authorization: Bearer <token>          # 需要 tenant_admin / platform_admin
Content-Type: application/json
```

请求体为**局部更新**（只带被改动的字段）：

```json
{ "remote_control_allowed": false }
{ "tunnel_mode": "direct" }
{ "isolated_tunnel_enabled": true }
{ "mesh_joined": true }
```

响应：`{ "item": { ...同 GET 的元素... } }`（客户端也接受直接返回该元素，或 `204`）。

错误约定（客户端已按此分支处理）：

| 状态码 | 客户端行为 |
| --- | --- |
| `400` | 展示后端 `message` |
| `401` | 自动登出并回到登录页 |
| `403` | 提示「当前账号无权修改该设备授权」 |
| `404` / `405` / `501` | 提示「后端接口未就绪，暂不可修改」 |
| `5xx` | 展示后端 `message` |

### 3. 建议（非阻塞）

- **RustDesk ID 上报**：目前后端从节点 tag（`rustdesk:<id>`）里取。
  更稳的做法是给 agent 增加一个原生字段上报，接口侧仍以 `rustdesk_id` 出参，
  客户端无需改动。
- **授权变更审计**：`PATCH` 建议复用现有审计中间件，action 形如
  `remote_desktop.policy.update`，detail 里记录改了哪些字段。
- **列表分页**：`device-policies` 如需分页，请保持 `items` / `total` 形状与
  `/remote-desktop/devices` 一致。

# NEILICONET 可行性分析

> **范围**：本轮只做现状盘点、对照分析、设计草案、影响评估与工作量估算。**不实现代码、不部署、不重启容器。** 本文中的占位符（如 `<VIP>`、`<公网端点>`、`<域名>`）不代表任何真实地址或密钥。
>
> **证据规则**：凡标注“现状”的结论均给出仓库内 `文件:行号`；没有代码证据的判断标注“推断”；性能数字若来自外部公开基准，标注“外部基准/量级”，不冒充本仓实测。

## 0. 执行摘要

**结论：可行，但有前提。** 可以在现有 WireGuard 全互联基础上补出“直连优先、星形中继兜底、控制面只协调”的 Neiliconet，但必须同时解决四件事：

1. 端点发现不能继续只用 HTTP 查询公网 IP；需要 STUN/多候选/端口映射与打洞协同。
2. 中继必须是独立的 **WireGuard 数据包中继**，只在直连失败时承载数据；不能把中继塞进控制面 API 进程。
3. 控制面或其同网络网关必须仍能到达 mesh 虚拟 IP；否则“域名反代 → `<VIP>`”会 502/超时。这是域名转发最关键的前提。
4. 需要把节点私钥从控制面迁回节点本地，并持久化足够的本地配置快照，才能兑现“控制面宕机不影响已建立直连”。

**当前最重要的纠偏**：现状控制面不是“只分发公钥”，而是生成 WireGuard 密钥对、加密保存节点私钥并在配置中下发私钥（`control-plane/internal/service/nodes.go:114-163`、`control-plane/internal/service/config/config.go:581-610`）；因此“控制面只分发公钥/路由/端点候选”是 Neiliconet 的**目标约束**，不是现状。

---

## 1. 事实盘点：当前 mesh、端点、数据路径

### 1.1 当前是不是全互联？节点拿到什么？

**结论：默认是 WireGuard 全互联，但 ACL 会过滤；不是星形。**

- 控制面按网络成员逐一构造 peer；每个成员的 `AllowedIPs` 至少包含该成员的虚拟 IP `/32`，再追加该成员启用的子网路由（`control-plane/internal/service/config/config.go:353-400`）。
- `filterPeers` 只排除本节点并按 ACL 删除被拒绝的 peer；没有 ACL 时保留其余全部成员（`control-plane/internal/service/config/config.go:430-459`）。因此逻辑规模是 `N×(N-1)`，不是“所有节点只连中心”。
- 虚拟 IP 由 `network_members.virtual_ip` 持久化并按网络唯一分配（`control-plane/internal/models/models.go:252-258`；分配逻辑 `control-plane/internal/service/networks.go:274-319`）。
- WireGuard 配置渲染为每个 peer 写 `PublicKey`、可选 `Endpoint`、`AllowedIPs`、网络 PSK 和 `PersistentKeepalive=25`（`control-plane/internal/service/mesh/wireguard/wireguard.go:58-72`）。
- 控制面把对端的 `LocalAddresses` 与 `ListenPort` 一并放进配置 JSON（`control-plane/internal/service/config/config.go:380-399`），但这些字段不是 WireGuard 配置本身的固定行；agent 会在同内网时改写 endpoint。

### 1.2 Endpoint 从哪里来？内网地址优先吗？

**结论：endpoint 由节点上报的 `public_endpoint` 提供；同内网时 agent 优先改成内网地址。**

- 节点上报 `public_endpoint`、`local_addresses`、`listen_port`，控制面保存到 `nodes`（`control-plane/internal/service/nodes.go:234-240`、`control-plane/internal/service/nodes.go:291-324`）。
- 配置构建时 endpoint 直接取 `node.PublicEndpoint`（`control-plane/internal/service/config/config.go:380-399`）。
- 未配置公网 endpoint 时，agent 用 HTTPS 查询外部 IP 服务，然后拼上本地 WireGuard 监听端口（`agent/cmd/agent/main.go:577-598`）。这不是 STUN：它只能看到 HTTP 请求的公网源地址，不能证明 UDP 监听端口的 NAT 映射。
- 对端与本机在同一内网时，`PreferLANEndpoints` 会优先使用对端上报的内网地址，并按对端自己的监听端口改写配置（`agent/internal/mesh/lanendpoint.go:91-127`）。
- 未填 endpoint 的 peer 仍然可以依赖 WireGuard roaming：对端主动发包后本端学习源地址（`control-plane/internal/service/mesh/wireguard/wireguard.go:61-66`）。

### 1.3 直连成立条件；两边都在 NAT 后会怎样？

**结论：当前没有 NAT 打洞/发现/中继数据面；两边都在严格 NAT 后时，跨网直连通常不成立。**

- 当前只有“固定/上报 endpoint + LAN 优先 + PersistentKeepalive”（`control-plane/internal/service/mesh/wireguard/wireguard.go:58-72`；`agent/internal/mesh/lanendpoint.go:91-127`）。
- 端点探测是 HTTP 公网 IP 查询（`agent/cmd/agent/main.go:577-598`），没有 STUN/ICE、端口预测、PCP/NAT-PMP/UPnP、并发打洞或候选路径协商。
- `p2p_success_rate`、`relay_bytes` 指标虽注册，但帮助文本明确写着没有采集器（`control-plane/internal/metrics/metrics.go:77-83`）；两者作为 Prometheus Gauge 初始为 0，且当前代码没有 setter/采集写入路径（字段与注册见 `control-plane/internal/metrics/metrics.go:27-28`、`control-plane/internal/metrics/metrics.go:152-170`），因此**当前恒为 0**。
- Compose 中的 `relay` 明确只是 wg-easy 占位，不是 NEILICO 中继数据面（`deploy/docker-compose/docker-compose.yml:112-125`）；`relay_servers` 目前只是元数据 CRUD（`control-plane/internal/service/relay.go:20-48`、`control-plane/internal/service/relay.go:117-195`）。

**场景判断（涉及 NAT 行为的部分标为“推断”）：**

| 场景 | 当前结果 | 依据/性质 |
|---|---|---|
| 同一 LAN，UDP 端口可达 | 可用；agent 改用内网 endpoint | 现状：`agent/internal/mesh/lanendpoint.go:91-127` |
| 一端有可入站的公网 UDP 映射，另一端能主动拨号 | 有机会直连；WireGuard roaming/keepalive 可维持已建立映射 | **推断**：WireGuard 行为 + 当前 endpoint/keepalive |
| 两端都在严格/对称 NAT，均无入站映射 | 当前没有打洞协同，通常无法建连 | 现状缺失：`agent/cmd/agent/main.go:577-598`；**推断**：NAT 结果 |
| 两端公网 IP 能查到但 UDP 端口映射随机 | `公网 IP:本地监听端口` 可能是错的，握手失败 | **推断**：当前探测只取 IP + 本地端口（`agent/cmd/agent/main.go:577-598`） |
| 直连失败 | 没有 WireGuard relay 兜底；域名/端口代理若可用，只是另一条应用层链路 | 现状：`deploy/docker-compose/docker-compose.yml:112-125`、`control-plane/internal/service/relay.go:20-48` |

`PersistentKeepalive` 只能维持已有 NAT 映射，不能单独完成两端同时开洞；这是**推断**，但与当前没有发现/打洞代码的事实一致。

### 1.4 控制面是不是 mesh 参与者？能否直接 `curl <VIP>`？

**结论：从当前代码和部署看，控制面 API 进程本身不是 mesh 参与者；`curl <VIP>` 不是仓库保证的能力。**

- 控制面负责渲染 WireGuard 配置并下发（`control-plane/internal/service/config/config.go:588-610`），但没有 agent 的 `wgctrl`/`ip link` 应用链路；mesh 应用链路在 `agent/internal/mesh/wgctrl.go:42-82`。
- `cmd/api` 启动的是 API 与内置反代 HTTP 服务（`control-plane/cmd/api/main.go:270-283`），没有创建控制面自身的 WireGuard 接口。
- Compose 的 control-api 服务没有 `NET_ADMIN`/TUN 配置；带 `NET_ADMIN` 的是独立 `relay` 占位容器（`deploy/docker-compose/docker-compose.yml:58-98`、`deploy/docker-compose/docker-compose.yml:112-125`）。
- 反代会把 `node` 目标解析成该节点虚拟 IP，再由 `httputil.ReverseProxy` 直接拨号（`control-plane/internal/service/proxy/routes.go:126-178`；`control-plane/internal/service/proxy/builtin.go:431-453`）。因此只有当 control-api 的网络命名空间/宿主已有到 `<VIP>` 的路由时，域名转发才会成功。

**这不是“控制面永远不能 curl `<VIP>`”**，而是当前仓库没有把控制面接入 mesh；若部署时另有宿主 WireGuard/路由，可能偶然可用，不能作为产品保证。

### 1.5 当前数据路径

#### A. Mesh 节点之间

```text
节点 A 的 WireGuard 内核接口
        ⇅（UDP，WireGuard 加密）
节点 B 的 WireGuard 内核接口
```

控制面只生成/下发 peer 配置；当前没有 mesh relay 数据面（`control-plane/internal/service/mesh/wireguard/wireguard.go:35-79`；`deploy/docker-compose/docker-compose.yml:112-125`）。

#### B. 绑定域名反代

```text
客户端
  →（TLS 可选）控制面内置反代
  → 解析 target_type=node/virtual_ip 为 <VIP>:<端口>
  → 直接 net/http 拨号目标
  → 目标节点/内网服务
```

- 目标解析与 VIP 见 `control-plane/internal/service/proxy/routes.go:126-178`。
- 反代缓存连接池、保留 Host，并写 `X-Forwarded-Host/Proto`（`control-plane/internal/service/proxy/builtin.go:399-471`）。
- 上游默认 `http`，只有显式 `https` 才走 TLS（`control-plane/internal/service/proxy/routes.go:189-194`；`control-plane/internal/service/proxy/builtin.go:431-453`）。

#### C. StreamRule 端口转发

```text
外部客户端
  → 控制面 StreamForwarder 监听 <发布端口>
  → net.Dial(target host:port)
  → TCP 双向 io.Copy / UDP 会话转发
  → 目标服务
```

- TCP 监听、拨号、双向复制见 `control-plane/internal/service/proxy/stream.go:277-345`；UDP 会话转发见 `control-plane/internal/service/proxy/stream.go:347-429`。
- `node` 目标由服务层解析为该节点虚拟 IP（`control-plane/internal/service/stream_rules.go:156-195`）。
- StreamForwarder 对原始 TCP/UDP 字节做转发，没有 TLS 包装；因此“进入控制面前”“控制面到内网目标”是否明文，取决于客户端协议和目标服务协议，不能因为目标在 VIP 上就自动变成应用层加密。

#### D. 自建远程桌面 hbbs/hbbr

```text
RustDesk 客户端
  → hbbs：ID/信令
  → P2P 成功：客户端之间直连
  → P2P 失败：客户端 ↔ hbbr ↔ 客户端
```

- NEILICO 只保存/下发 `id_server`、`relay_server`、公钥等接入参数（`control-plane/internal/service/remote_desktop.go:32-44`、`control-plane/internal/service/remote_desktop.go:164-195`）。
- 控制面按需启动 hbbs/hbbr；hbbs 可带 `-r <relay_host>`，hbbr 使用 `-k _` 校验密钥（`control-plane/internal/service/rustdesk_server.go:337-355`、`control-plane/internal/service/rustdesk_server.go:381-392`）。
- “单独隧道”复用 StreamRule，把发布端口转发到节点 RustDesk 直连端口 `21118`（`control-plane/internal/service/remote_desktop_policies.go:30-32`、`control-plane/internal/service/remote_desktop_policies.go:257-275`）。
- `tunnel_mode=auto/direct/relay` 当前是持久化策略枚举与视图字段（`control-plane/internal/service/remote_desktop_policies.go:16-21`、`control-plane/internal/service/remote_desktop_policies.go:467-499`）；仓库中未见 RustDesk 会话路由器根据该枚举切换数据路径，因此**模式开关是否真正改变传输路径属于“推断/待验收”**。

**拓扑结论**：mesh 节点间是全互联；“星形”只出现在应用层入口（控制面域名反代/StreamRule）与 RustDesk hbbr，**当前不存在 WireGuard 直连失败后的星形 relay 兜底**（`control-plane/internal/service/config/config.go:353-459`；`control-plane/internal/service/proxy/stream.go:277-345`；`deploy/docker-compose/docker-compose.yml:112-125`）。

---

## 2. Tailscale 对照

以下是对 Tailscale 公开设计的对照模型，不是本仓现状；其行为应以 Tailscale 官方 DERP、NAT traversal、key expiry 文档为最终依据。

| 能力 | Tailscale 的典型做法 | Neiliconet 应学到什么 | 当前差距 |
|---|---|---|---|
| 协调面 | 分发公钥、节点身份、虚拟 IP、路由/ACL、DERP/端点候选；不转发业务数据 | 控制面只做编排与元数据；数据面独立 | 当前控制面生成并暂存节点私钥（`control-plane/internal/service/nodes.go:114-163`），且内置反代/StreamRule 会在控制面进程转发业务数据（`control-plane/internal/service/proxy/builtin.go:431-453`、`control-plane/internal/service/proxy/stream.go:277-345`） |
| NAT 穿透 | STUN/端点发现、内网/公网/映射端口候选、端口映射协议、并发打洞、失败后切换路径 | 候选集 + 双方协同开洞 + 可观测成功率 | 当前只有 HTTP IP 查询和 LAN 优先（`agent/cmd/agent/main.go:577-598`；`agent/internal/mesh/lanendpoint.go:91-127`） |
| DERP | 只在直连不可用时转发 **已加密的 WireGuard/会话包**；可多地域、可迁移 | 独立 relay，转发 WG 密文，不终止 WireGuard | 当前 relay 是元数据/占位（`control-plane/internal/service/relay.go:20-48`；`deploy/docker-compose/docker-compose.yml:112-125`） |
| 控制面宕机 | 已建立直连在密钥/路由仍有效时继续；新发现、策略变更、证书/密钥更新受影响 | 本地缓存完整可恢复配置，避免运行时依赖控制面 | 当前 agent 拉不到配置会退避重试但保留旧版本（`agent/cmd/agent/main.go:409-443`）；state 只存部分 peer 期望信息（`agent/internal/state/state.go:13-47`），节点/容器重启后未必能离线重建 |
| 密钥 | 私钥留在节点，控制面只持有公钥/策略 | 节点本地生成/保存私钥，控制面只签发/分发公钥 | 当前注册由控制面生成私钥并回传（`control-plane/internal/service/nodes.go:114-163`） |
| 路径迁移 | 直连质量更好就切直连，失败才 DERP；已有连接可平滑迁移 | 直连优先，relay 只做兜底；切换不影响 VIP/ACL 语义 | 当前无路径选择器、无 relay 字节统计 |

**“控制面宕机不影响已建立直连”的成立机制（目标设计）**：每个节点本地保存私钥、peer 公钥、AllowedIPs、端点候选、子网路由和已应用版本；WireGuard 会话由内核/本地接口维持，控制面不参与每个包。控制面宕机只暂停新配置、端点更新、策略变更和新节点加入。**前提**是密钥仍在有效期内、节点本地状态未丢失，且端点/路由未变化。

---

## 3. 差距清单

| # | 现在缺什么 | 缺了会怎样 | 补上难度（估算） |
|---|---|---|---|
| 1 | STUN/ICE/端口映射/候选端点发现 | NAT 后双方握手失败；公网 IP 不等于 UDP 可入站 | 中高：新增发现协议、采集器、候选数据模型与跨平台网络探测 |
| 2 | 双方协同打洞、端口预测/并发尝试 | 严格 NAT 下没有直连，只能依赖未来的 relay | 高：UDP 穿透时序、NAT 类型、时钟/重试、回滚 |
| 3 | 真实 relay 数据面 | relay_servers 只能显示元数据；P2P 失败没有兜底 | 高：独立进程、加密转发、选路、计费/限速/观测 |
| 4 | 控制面到 VIP 的 mesh 路由 | 域名反代的 `virtual_ip`/`node` 目标会 502/超时 | 中：控制面 sidecar/宿主网关或 relay gateway |
| 5 | 节点本地私钥与公钥注册 | 控制面成为密钥托管点；泄露/轮换/离线恢复风险 | 高：注册协议迁移、旧 agent 兼容、密钥轮换 |
| 6 | 离线可恢复的完整本地快照 | 控制面宕机期间已建立直连可继续，但节点重启后无法重建 | 中：本地快照签名、版本、过期与恢复 |
| 7 | 真实 P2P/relay 指标 | `p2p_success_rate`/`relay_bytes` 恒 0，无法调度或告警 | 中：agent 路径探测、relay 计数、指标聚合 |
| 8 | 全互联规模与 ACL 的动态控制 | peer/配置随成员数平方增长；策略变化要整网下发 | 中：增量配置、peer 分组、ACL/AllowedIPs 一致性测试 |
| 9 | Windows/macOS 的真实接口适配 | 当前仅 Linux 可全量应用；跨平台节点会 degraded | 中高：WireGuardNT/系统扩展、路由命令、服务生命周期 |
| 10 | 应用层明文治理 | WireGuard 只保护 overlay；HTTP/StreamRule/目标 LAN 仍可能明文 | 中：TLS/mTLS 策略、上游 TLS、入口协议审计 |
| 11 | 远程桌面 mode 与真实传输路径绑定 | UI 显示 direct/relay 但实际仍可能走 hbbr 或 StreamRule | 中：客户端策略协议、路径探测、验收用例 |

---

## 4. Neiliconet 设计草案

### 4.1 路径优先级

```text
① LAN/直连候选可用
   → WireGuard 直连（首选）
② 直连探测失败或持续质量差
   → 独立星形 relay 兜底
③ relay 也不可用
   → 明确降级/不可达，不用控制面偷偷转发 mesh 数据
```

- **relay 只在兜底时承载数据**；直连成功时 `relay_bytes` 应为 0。
- Relay 转发的是 WireGuard 数据包/会话密文，不解析、不降级为明文应用代理。
- 目标 VIP、AllowedIPs、ACL 不因直连/relay 切换而变化。

### 4.2 协调面边界

**目标约束：控制面只分发公钥、路由、端点候选和策略；不允许把 Neiliconet mesh 数据面放进控制面。**

- 节点本地生成 WireGuard 私钥；控制面只保存公钥、节点身份、VIP、AllowedIPs、候选端点与签名后的配置版本。
- 端点候选至少包括：手工公网端点、LAN 地址、STUN 反射地址、PCP/NAT-PMP/UPnP 映射地址、IPv6、relay 端点。
- 控制面只负责候选的收集/排序/分发与 ACL；打洞、路径探测、WireGuard roaming 在 agent。
- 现有 `PresharedKey` 是全网络共享值并写入每个 peer（`control-plane/internal/service/mesh/wireguard/wireguard.go:67-72`）；Neiliconet 建议取消网络级共享 PSK，改用每节点 WireGuard 密钥和握手，避免控制面继续分发额外秘密。

### 4.3 端点发现与打洞

1. Agent 采集本机 LAN、STUN、端口映射、IPv6 候选并签名上报。
2. 控制面只分发候选集和 peer 公钥；不决定最终数据包路径。
3. 两个 agent 通过轻量协同信令同时向候选地址发 UDP 探测，WireGuard roaming 记录可用源地址。
4. 路径选择器按“直连 RTT/丢包/稳定性”排序；质量低于阈值才切 relay。
5. 直连成功后保留 relay 作为冷备，不持续占用 relay。

**验收必须覆盖**：同 LAN、单边公网映射、双方 NAT、运营商级 NAT、UDP 被封、IPv4/IPv6 混合、端口变化、网络切换。

### 4.4 星形 relay 兜底

- 新建独立 relay 服务/进程，不复用 control-api；控制面只维护 relay 的地址、区域、健康度和公钥。
- Relay 转发 WireGuard UDP/会话密文，无法解密 payload；转发器只看得到流量元数据。
- 直连探测失败后，agent 把该 peer 的临时 endpoint 指向 relay；一旦直连恢复，自动切回。
- relay 需要有租户隔离、最大并发/带宽、会话超时、审计计数和故障转移。
- `relay_bytes` 必须由 relay 或 agent 的真实计数采集，不能继续是占位 gauge。

### 4.5 控制面降级可用

- Agent 本地持久化：节点私钥、peer 公钥、AllowedIPs、VIP、候选端点、子网路由、ACL 摘要、已应用版本和配置签名。
- 控制面不可达时：不清理已有 WireGuard，不重置路由，不把拉配置失败当成节点离线；只暂停变更。
- 节点/容器重启时：从本地快照恢复已知 peer 与路由；新 peer、密钥轮换、策略变更等待控制面恢复。
- 明确密钥有效期/轮换窗口；过期或被撤销的 peer 不应无限期继续。

### 4.6 域名反代可达性的两种方案（必须二选一或组合）

**前提（醒目）**：控制面必须仍能到达 mesh 的 `<VIP>`。否则“绑定域名 → `virtual_ip`/`node` 目标”会断；域名绑定记录本身还在，但转发会 502/超时。

#### 方案 A：控制面作为 mesh 节点（推荐）

- control-api 旁边放 mesh gateway/sidecar，或让控制面宿主网络加入同一 Neiliconet。
- control-api 的网络命名空间必须有 `<VIP>` 路由；gateway 负责直连优先/relay 兜底。
- 保留现有 target 解析：`node`/`virtual_ip` 仍解析成 `<VIP>:<端口>`，内置反代不必改业务语义。
- 优点：域名转发、StreamRule、子网路由都走统一 overlay；缺点：控制面部署需要 `NET_ADMIN`/TUN 或共享网络命名空间。

#### 方案 B：反代流量走独立 relay gateway

- control-api 不加入 mesh；proxy/stream gateway 通过 relay 建立到目标节点的加密数据通道。
- 反代 target 解析保留 VIP 语义，但在 gateway 内部把 VIP 映射到 mesh 会话/relay 通道。
- 优点：控制面权限最小；缺点：增加一跳和 gateway 复杂度，且必须把“直连优先”逻辑放到 gateway，不能让 control-api 直接 `net.Dial` 一个不可达的 VIP。

两种方案都不能把 mesh 数据面塞回 control-api 协调代码；应把 proxy/stream 数据面部署为独立的 proxy plane 或 gateway。

---

## 5. 明文清单与升级后的加密覆盖

| 链路 | 当前状态（证据） | Neiliconet 升级后 | 仍可能明文/注意 |
|---|---|---|---|
| 节点 ↔ 节点 WireGuard 直连 | 已加密；WireGuard 配置含公钥/AllowedIPs/PSK（`control-plane/internal/service/mesh/wireguard/wireguard.go:58-72`） | 仍全程 WireGuard | WireGuard 保护 overlay，不改变应用协议本身 |
| 节点 ↔ 节点 relay 兜底 | 当前无真实 relay 数据面（`deploy/docker-compose/docker-compose.yml:112-125`） | relay 转发 WireGuard 密文，端到端仍是 WG | relay 看得到流量元数据；不能用应用层明文代理替代 |
| Agent ↔ 控制面配置/心跳 | TLS 可选；配置允许绝对 URL，HTTP 也可用（`agent/internal/config/config.go:224-231`；`agent/internal/client/client.go:49-82`） | 强制 TLS，生产启用 mTLS；私钥不经过控制面 | 若保留 `http://` 或 `insecure_skip_verify`，仍可能被窃听/中间人 |
| 浏览器/客户端 → 域名反代入口 | TLS 可选；默认配置 TLS 关闭（`control-plane/internal/config/config.go:199-212`；`control-plane/configs/config.example.yaml:6-16`） | 入口统一 TLS/HSTS；必要时 mTLS | 仅开 HTTP 时入口明文 |
| 反代 → `internal_ip`/HTTP 上游 | 默认 `http`，可选 `https`（`control-plane/internal/service/proxy/routes.go:189-194`；`control-plane/internal/service/proxy/builtin.go:431-453`） | 仍按业务需要 HTTPS；mesh 到 VIP 段用 WG | 上游应用若只听 HTTP，目标 LAN 段仍明文 |
| 反代 → VIP/节点 | 只有当 control-api 有 VIP 路由才可达；否则失败（`control-plane/internal/service/proxy/routes.go:126-178`；`control-plane/cmd/api/main.go:270-283`） | 方案 A 直连/relay；方案 B 经 gateway/relay | 反代本身可能读取/改写 HTTP 头；WireGuard 不等于应用层 E2E |
| StreamRule 外部入口 → 控制面 → 内网目标 | 原始 TCP `io.Copy`、UDP 会话转发，无 TLS 包装（`control-plane/internal/service/proxy/stream.go:277-429`） | VIP 段可被 WG 保护；入口与目标 LAN 段需按协议另行 TLS/WG | 外部客户端或目标服务使用明文协议时，仍存在明文段 |
| RustDesk hbbs/hbbr | hbbs 负责 ID/信令、hbbr 负责中继；由进程参数启动（`control-plane/internal/service/rustdesk_server.go:337-355`、`control-plane/internal/service/rustdesk_server.go:381-392`） | 保留 RustDesk 自己的会话加密；不把 hbbr 当 Neiliconet WG relay | 具体信令/中继保护依赖 RustDesk 版本与配置，属**推断/需协议验收** |
| 可选 NPS 外部代理 | 配置可生成、外部二进制不存在时 degraded（`control-plane/internal/service/proxy/nps/nps.go:68-105`） | 不作为 Neiliconet mesh 数据面 | NPS `crypt` 不是端到端 AEAD；不能替代 WireGuard |

**“升级后哪些变成密文、哪些仍明文”的直接回答**：

- 变成/保持密文：节点之间 overlay（直连或 relay）WireGuard 密文；启用 TLS/mTLS 后 agent/控制面、浏览器/入口可加密。
- 仍可能明文：未启用 TLS 的 HTTP 入口、StreamRule 的原始 TCP/UDP 业务、`http` 上游、目标服务到 LAN 业务的最后一段、未被 WireGuard 覆盖的外部客户端入口。
- **WireGuard 不会自动把 HTTP/数据库/自定义 TCP 协议变成端到端密文**；必须逐条决定入口 TLS、上游 TLS 或让客户端本身加入 overlay。

### 5.1 WireGuard 内核态直连 vs 用户态星形中继（量级）

> 本仓没有 WireGuard/中继吞吐基准，以下均为**外部公开基准的量级 + 基于架构的推断**，不是本仓实测，不能用于容量承诺。真实结果取决于 CPU 单核性能、NIC、MTU、包长、并发流、加密指令集和网络 RTT。

| 路径 | 吞吐量级 | 延迟/开销量级 | 性质 |
|---|---:|---:|---|
| 内核 WireGuard 直连（现代 x86、万兆内网） | 约 **1–10 Gbps**；单流常见为数 Gbps，多核/多流可更高 | 相比裸 UDP 多约 **0.1–1 ms**（同机房/同区域量级） | 外部公开基准量级；不是本仓实测 |
| 用户态 WireGuard（wireguard-go 类） | 约 **0.2–2 Gbps**，同等硬件通常低于内核实现 | CPU 占用常高 **2–5 倍**，尾延迟更易受调度影响 | 外部基准量级 + **推断** |
| 用户态星形 relay 转发 WG 密文 | 单个普通 Go relay 实例约 **0.1–1 Gbps**；包越小、并发越高，pps/CPU 越早成为瓶颈 | 比直连多“源→relay→目的”这一整段路径，新增 **一个广域 RTT 量级**，并叠加 relay 调度/复制 | **推断**；仓库无 relay 实现或基准 |
| RustDesk hbbr | 由桌面帧率、编码、分辨率和会话数决定，不等同于批量 TCP 吞吐 | P2P 失败时多一跳 relay，画质/尾延迟受 hbbr 容量影响 | **推断**；依赖 RustDesk 运行数据 |
| 现有内置 HTTP 反代（对照，不是 WG） | 本仓基线为 200 并发约 **7.8k–9.6k RPS** | p99 约 **68–90 ms**（容器/网络路径不同） | 本仓既有反代压测记录，不是 Neiliconet 数据面结果 |

**设计含义**：直连成功时应尽量让内核 WireGuard 工作，relay 只在直连失败时接管；若 relay 转发的是 WireGuard 密文，端点仍是 WG 加密，但吞吐和延迟会受 relay 容量限制。不要用“用户态 relay”吞吐去推断“WireGuard 本身慢”，二者是不同瓶颈。

**建议的正式验收**（未来实现后执行）：

- 同硬件、同 MTU、同包长分别测内核 WG 直连、用户态 WG、WG-over-relay。
- 记录单流/多流吞吐、p50/p99 RTT、CPU、pps、丢包、relay `bytes` 与切换次数。
- 直连/relay 切换期间用长连接与 UDP 会话验证不断流；结果写入容量模型，而不是写死在产品承诺中。

---

## 6. 对既有功能的影响评估

### 6.1 绑定域名转发（反代）——用户最关心

**结论：不受协议语义影响，但受“控制面到 VIP 的可达性”硬前提约束。**

- `node`/`virtual_ip` 目标仍可保持 `<VIP>:<端口>`；现有解析逻辑是 `control-plane/internal/service/proxy/routes.go:126-178`。
- 现有反代保留 Host 与转发头（`control-plane/internal/service/proxy/builtin.go:454-466`），Neiliconet 不应改变 HTTP 语义。
- 方案 A（控制面 mesh 节点）或方案 B（反代经 relay gateway）任选其一；没有 VIP 路由时，域名规则会存在但请求 502/超时。
- `internal_ip` 目标不依赖 mesh，可继续工作；`virtual_ip`/`node` 目标才依赖 overlay。
- 上游 `http/https` 仍按规则配置；升级 mesh 不会自动把上游变成 HTTPS。

**验收判据**：从 control-api 的网络命名空间执行 `curl http://<VIP>:<端口>` 成功；同一域名规则对外请求保持 Host、路径、状态码、WebSocket/流式响应语义；杀掉 relay 后直连可用时请求仍成功。

### 6.2 StreamRule 端口转发

**结论：可保留，但必须把“目标 VIP 的下一跳”抽象出来。**

- 当前监听 `:<发布端口>`，直接拨号解析后的 target（`control-plane/internal/service/proxy/stream.go:184-213`、`control-plane/internal/service/proxy/stream.go:277-345`）。
- `node` 目标解析成 VIP（`control-plane/internal/service/stream_rules.go:156-195`），所以直连/relay 路由改变不应改变规则模型。
- TCP/UDP 语义、空闲超时、白名单和半关闭行为要回归（`control-plane/internal/service/proxy/stream.go:251-345`、`control-plane/internal/service/proxy/stream.go:347-429`）。
- 入口原始流量没有自动 TLS；若端口转发用于明文协议，升级 WG 只保护 overlay 段。

### 6.3 远程桌面 hbbs/hbbr

**结论：可继续独立运行；Neiliconet relay 与 RustDesk hbbr 必须分离。**

- hbbs/hbbr 是 RustDesk 的 ID/中继组件，进程启动和密钥参数在 `control-plane/internal/service/rustdesk_server.go:337-392`。
- “单独隧道”是 StreamRule 到节点 `21118`（`control-plane/internal/service/remote_desktop_policies.go:257-275`），不等于 hbbr 数据面。
- `tunnel_mode` 目前是策略字段；要让 direct/relay 真正可验证，需要客户端/会话层的路径选择和指标，不应只改 UI。
- RustDesk 会话保护依赖 RustDesk 自己的协议；Neiliconet WireGuard 不应重复加密或终止其会话。

### 6.4 子网路由

**结论：保留，但要纳入离线快照和 relay 选路。**

- 子网路由进入 peer `AllowedIPs`（`control-plane/internal/service/config/config.go:383-387`），agent 再为 AllowedIPs 写内核路由（`agent/internal/mesh/routes.go:20-43`）。
- 转发开启时 agent 会设置 `ip_forward` 与 MASQUERADE（`agent/internal/route/route.go:95-171`）。
- 直连/relay 切换必须保持相同 AllowedIPs 与路由；不得因 endpoint 改写导致路由消失。
- 需要防止路由环、重复 CIDR、ACL 与 AllowedIPs 不一致。

### 6.5 mTLS/PKI

**结论：可以复用并强化，但当前默认不是强制。**

- Server TLS 配置含 `client_auth=none/request/require` 与客户端 CA（`control-plane/internal/config/config.go:22-30`；默认关闭见 `control-plane/internal/config/config.go:199-220`）。
- `require` 会验证客户端证书，但健康/指标/CA 引导允许无客户端证书（`control-plane/cmd/api/main.go:433-450`）。
- Agent 已支持 CA、客户端证书、服务端名校验和 `insecure_skip_verify` 告警（`agent/internal/client/client.go:49-82`；`agent/cmd/agent/main.go:670-675`）。
- PKI 可签发节点客户端证书，私钥加密落库且回显边界受控（`control-plane/internal/service/pki/pki.go:253-303`；`control-plane/internal/api/pki.go:58-108`）。
- Neiliconet 需要明确：mTLS 保护的是 agent/控制面信令，不替代 WireGuard 数据面，也不自动保护 StreamRule 的业务流量。

---

## 7. 分期计划、验收与风险（人日为量级估算）

> 以下是单名熟悉 Go/Linux/网络的工程师的**估算**，不是承诺；包含代码、测试、联调和文档，不含采购/运维排期。生产级多地域、Windows/macOS 全适配会落在上界之外。

### ① 端点发现、契约与密钥归属（8–12 人日）

**改动面**：`control-plane/internal/service/nodes.go`、`node_enroll.go`、`mesh/wireguard`、`internal/service/config/config.go`、迁移脚本；`agent/cmd/agent/main.go`、`agent/internal/mesh/lanendpoint.go`、`agent/internal/state/state.go`。

**验收**：

- 节点本地生成并保存私钥；API/DB 只出现公钥与元数据。
- 上报/下发包含 LAN、STUN/映射、IPv6、relay 候选；旧 agent 能在兼容模式下继续。
- 同 LAN 仍优先内网；跨网不会把公网 IP+本地端口误当作已验证 UDP 映射。
- 配置快照可签名、可版本化、可恢复。

**风险/回滚**：新旧注册协议不兼容、密钥迁移中断。保留 `legacy-config-renderer`/feature flag，先只读上报候选，不切换数据面。

### ② 打洞与直连路径（12–18 人日）

**改动面**：`agent/internal/mesh` 新增 discovery/path 模块，扩展 `wgctrl.go`/`reconcile.go`，新增 agent 指标；控制面只扩展候选元数据与版本。

**验收**：

- 同 LAN、单边可入站、双方 NAT 的实验矩阵有明确结果。
- 直连成功时不写 relay；路径切换不改 VIP/AllowedIPs/ACL。
- WireGuard endpoint roaming、MTU、重连、网络切换、进程重启有回归。
- 控制面重启/断网 10 分钟，已建立直连流量不中断。

**风险/回滚**：运营商 NAT、UDP 封锁、端点抖动导致反复切换。设路径滞回/最小保持时间；出现故障时回退到旧静态 endpoint 模式。

### ③ 独立星形 relay 兜底（12–20 人日）

**改动面**：独立 relay 服务/协议/部署、`relay_servers` 健康与调度、agent relay client、`p2p_success_rate`/`relay_bytes` 真采集；不改 control-api 的协调职责。

**验收**：

- 人为阻断直连后，自动切 relay；恢复直连后自动回切。
- relay 抓包/日志只能看到 WireGuard 密文或会话密文，不能读出应用 payload。
- relay 带宽/连接/超时/租户隔离生效，`relay_bytes` 有真实计数。
- relay 全部不可用时明确降级，不会把数据悄悄送进控制面。

**风险/回滚**：relay 成为新瓶颈、跨地域成本、协议兼容、滥用。先单 relay 小流量灰度；保留关闭 relay 的开关和静态直连配置。

### ④ 控制面降级可用与域名转发（10–16 人日）

**改动面**：control-api 旁的 mesh gateway/sidecar 或 proxy relay gateway；`proxy/routes.go`/`builtin.go` 的下一跳抽象、`stream_rules.go` 的 VIP 解析、部署网络权限；本地恢复快照。

**验收**：

- 方案 A/B 至少一种满足：control-api 网络命名空间可 `curl <VIP>`。
- 域名 `node`/`virtual_ip` 规则与 `internal_ip` 规则均回归；Host、路径、WebSocket/流式响应不变。
- 控制面宕机后已建立节点直连继续；控制面恢复后增量同步，不重置已建立会话。
- 没有 VIP 路由时故障可观测，不能静默 502。

**风险/回滚**：路由环、容器网络权限、proxy 502、规则缓存失效。保留现有 `builtin` 直拨模式作为回滚；gateway 故障时只影响 mesh 目标，不影响普通 `internal_ip`。

### 可选 ⑤ 跨平台与生产加固（8–15 人日）

**改动面**：Windows WireGuardNT/macOS 系统组件、服务生命周期、密钥轮换、TLS/mTLS 强制、性能/压力测试。

**估算汇总**：

- Linux MVP、单 relay、基本域名/StreamRule 兼容：**42–66 人日**。
- 含跨平台、多地域 relay、强制 mTLS、完整混沌与性能验收：**58–93 人日**。
- 若要求“所有 Windows/macOS 真机认证 + 多地域 SLA”，应另立项目，不应把上述估算当作上限。

---

## 8. 风险与回滚总表

| 风险 | 影响 | 缓解 | 回滚 |
|---|---|---|---|
| 严格 NAT 仍无法直连 | 只能 relay，延迟/成本上升 | 真实 NAT 矩阵、候选冗余、relay 多地域 | 关闭新 path manager，回到静态 endpoint |
| relay 泄密/被滥用 | 数据面可用性与安全受损 | 只转发 WG 密文、鉴权、限速、租户隔离、审计 | 停用 relay，保留直连/现有代理 |
| 控制面/网关无 VIP 路由 | 域名 VIP 目标全部失败 | 方案 A/B 预检、启动探针、显式错误 | 回退 `internal_ip` 或旧代理路径 |
| 私钥迁移失败 | 节点无法重连 | 双写兼容期、分批轮换、备份/恢复演练 | 保留 legacy key renderer |
| ACL/AllowedIPs 不一致 | 越权或黑洞路由 | 单一策略快照、增量 diff、集成测试 | 回滚上一配置版本 |
| 原始 StreamRule 明文 | 被动窃听 | 入口 TLS/WG、目标 TLS、协议审计 | 禁用明文规则或强制应用 TLS |
| Windows/macOS 适配不足 | 节点 degraded | 能力门禁与真实验收 | 只开放 Linux mesh，其他平台仅控制面能力 |

---

## 9. 最终可行性判断与三条取舍建议

### 判断

**可行（有前提），不建议在没有端点发现、独立 relay、控制面 VIP 可达性方案的情况下宣称“Tailscale 式”能力。**

### 三条最关键的取舍建议

1. **直连优先，relay 只做兜底；不要让 control-api 变成数据面。**  
   代价是打洞和 relay 的开发/运维复杂度；收益是延迟、成本和控制面解耦。若业务必须保证域名 VIP 可达，方案 A（控制面/网关加入 mesh）比“让 control-api 直接拨 VIP”更可控。

2. **私钥必须回到节点本地，控制面只留公钥和策略。**  
   代价是注册、轮换、恢复协议要迁移；收益是控制面泄密不等于全网私钥泄密，且控制面宕机不切断已建立直连。不要为了兼容继续让 control-api 长期保存节点私钥。

3. **把“WireGuard 加密”与“应用端到端加密”分开验收。**  
   WireGuard 可解决节点间 overlay；HTTP 反代、StreamRule、目标 LAN、RustDesk 信令仍需逐条决定 TLS/mTLS/应用协议。否则升级后仍会出现明文段，用户会误以为“全程加密”。

---

## 附录 A：必须保留的验收口径

- “现状”只引用本文件列出的代码文件与行号；“推断”不得写成实测事实。
- `p2p_success_rate`/`relay_bytes` 在采集器接入前只能是 `insufficient_data` 或 0，不得伪造成功率/流量。
- 域名反代验收必须同时验证：DNS/Host、HTTP 状态、VIP 路由、直连/relay 切换、控制面宕机降级。
- 不把 hbbs/hbbr、NPS、StreamRule 与 Neiliconet WireGuard relay 混称为同一个中继。
- 不在文档、日志、测试夹具中写真实内网地址、宿主绝对路径或任何密钥。

export type Role = 'platform_admin' | 'tenant_admin' | 'ops' | 'readonly'

export interface AuthUser {
  id: string
  email: string
  role: Role
  tenant_id: string
}

export interface AuthResponse {
  token: string
  refresh_token: string
  user: AuthUser
}

export interface PasswordPolicy {
  min_length: number
  max_length: number
  min_classes: number
  description: string
}

// 「初始化状态」：registration_open 为真表示系统还没有账号，可走首次注册。
export interface SetupStatus {
  initialized: boolean
  registration_open: boolean
  password_policy: PasswordPolicy
}

export interface AccountInfo {
  id: string
  tenant_id: string
  email: string
  role: Role
  status: string
  created_at: string
}

export interface AccountResponse {
  account: AccountInfo
  password_policy: PasswordPolicy
}

export interface AccountEmailResult {
  account: AccountInfo
  env_file_updated: boolean
}

export interface PasswordRotateResult extends AuthResponse {
  env_file_updated: boolean
  notice: string
}

export interface APIToken {
  id: string
  name: string
  token_prefix: string
  scopes: string[]
  expires_at?: string | null
  last_used_at?: string | null
  revoked_at?: string | null
}

export interface APITokenCreateResult {
  token: string
  notice: string
  api_token: APIToken
}

export interface Tenant {
  id: string
  name: string
  plan: string
  created_at: string
}

export interface User {
  id: string
  tenant_id: string
  email: string
  role: Role
  status: 'active' | 'inactive' | string
  created_at: string
}

export interface Node {
  id: string
  tenant_id: string
  name: string
  public_key: string
  virtual_ip?: string | null
  network_id?: string | null
  wireguard_public_key?: string | null
  public_endpoint?: string | null
  os: string
  arch: string
  version: string
  status: 'online' | 'offline' | string
  last_seen?: string | null
  tags: string[]
  created_at: string
  capabilities?: NodeCapabilities
  /** 服务端算出的「有效 Mesh 状态」：以 tunnel 为权威信号，tunnel 不可用即 unavailable。 */
  effective_mesh?: 'ready' | 'degraded' | 'unavailable' | string
  /** 能力自相矛盾（如 mesh=ready 但 tunnel=unavailable）时的说明；无异常为空。 */
  capabilities_note?: string
  /** last_seen 是否已超过心跳超时时间（true 表示「在线」不再可信）。 */
  heartbeat_stale?: boolean
}

export interface NodeCapabilities {
  mesh?: 'ready' | 'degraded' | 'unavailable' | string
  subnet_routes?: 'ready' | 'degraded' | 'unavailable' | string
  tunnel?: 'ready' | 'degraded' | 'unavailable' | string
  reason?: string
}

export interface NodeRegisterResult {
  node_id: string
  agent_token: string
  tenant_id: string
  status: string
  public_key: string
  private_key?: string
}

export interface EnrollTokenCommands {
  linux?: string
  macos?: string
  windows?: string
  docker?: string
}

export interface EnrollToken {
  id: string
  tenant_id: string
  network_id?: string | null
  name_hint: string
  expires_at: string
  max_uses: number
  used_count: number
  status: 'active' | 'used' | 'expired' | 'revoked' | string
  revoked_at?: string | null
  created_by?: string | null
  created_at: string
}

export interface EnrollTokenCreateInput {
  name_hint?: string
  network_id?: string | null
  expires_in_seconds: number
  max_uses: number
}

export interface EnrollTokenCreateResult {
  id: string
  token: string
  expires_at: string
  max_uses: number
  used_count: number
  network_id?: string | null
  name_hint: string
  server: string
  commands: EnrollTokenCommands
  /** Docker 命令使用的 agent 镜像地址（控制面返回，用于在界面上标明镜像来源） */
  agent_image?: string
  /** 令牌（及其内嵌命令）的生成时间：命令按这一刻的配置固化，升级后需重新生成 */
  created_at?: string
}

export interface StreamRule {
  id: string
  tenant_id: string
  name: string
  protocol: 'tcp' | 'udp'
  listen_port: number
  target_type: 'node' | 'virtual_ip' | 'internal_ip'
  target: string
  ip_whitelist: string[]
  enabled: boolean
  created_at: string
}

// 端口转发规则 + 转发引擎的运行时状态（连接数、累计流量、错误原因）。
export interface StreamRuleView extends StreamRule {
  status: 'running' | 'pending' | 'error' | 'disabled' | string
  last_error?: string
  active_connections: number
  bytes_in: number
  bytes_out: number
}

export interface StreamRuleList extends Paged<StreamRuleView> {
  port_range: { min: number; max: number }
}

export interface Paged<T> {
  items: T[]
  total: number
  page?: number
  page_size?: number
}

export interface VirtualNetwork {
  id: string
  tenant_id: string
  name: string
  cidr: string
  network_secret?: string
  created_at: string
}

export interface NetworkMember {
  id: string
  network_id: string
  node_id: string
  virtual_ip: string
  role: string
  joined_at: string
  node?: Node
}

export interface AclRule {
  id: string
  network_id: string
  src: string
  dst: string
  action: 'allow' | 'deny' | string
  protocol: string
  ports: string
  priority: number
}

export interface SubnetRoute {
  id: string
  network_id: string
  node_id: string
  cidr: string
  enabled: boolean
}

export interface Certificate {
  id: string
  tenant_id: string
  domain: string
  issuer: string
  cert_pem: string
  expires_at?: string | null
  status: 'active' | 'pending' | 'failed' | 'revoked' | string
  last_error?: string
  renewed_at?: string | null
  renew_count?: number
  challenge_type?: string
  auto_renew?: boolean
  next_attempt_at?: string | null
  created_at?: string
}

export interface CertificateIssueInput {
  issuer: 'acme'
  domain: string
}

export interface CertificateActionResult {
  id: string
  status: 'pending' | 'active' | 'failed' | 'revoked' | string
}

export interface PKICA {
  ca_cert_pem: string
  id?: string
  name?: string
  not_before?: string
  not_after?: string
  created_at?: string
}

export interface NodeCertificate {
  id: string
  node_id: string
  serial_number: string
  fingerprint: string
  not_before: string
  not_after: string
  issued_at: string
  ca_cert_pem?: string
}

export interface NodeCertificateIssueResult {
  client_cert_pem: string
  client_key_pem: string
  certificate: NodeCertificate
}

export type AlertState = 'firing' | 'resolved'
export type AlertSeverity = 'critical' | 'warning' | 'info'

export interface Alert {
  id: string
  rule: string
  severity: AlertSeverity
  target_type: string
  target_id: string
  title: string
  detail: string
  value: number
  threshold: number
  since: string
  state: AlertState
  started_at?: string | null
  resolved_at?: string | null
  data_status?: 'available' | 'insufficient_data' | string
  tenant_id?: string
  evaluated_at?: string | null
}

export interface AlertEvent {
  id: string
  alert_id: string
  rule: string
  target_type: string
  target_id: string
  state: AlertState
  severity: AlertSeverity
  value: number
  threshold: number
  detail: string
  created_at: string
}

export interface AlertDetail {
  alert: Alert
  events: AlertEvent[]
}

export interface AlertRule {
  id: string
  condition: string
  severity: AlertSeverity
  target_type: string
  threshold: number
  threshold_unit: 'seconds' | 'days' | 'ratio' | 'multiplier' | 'failures' | string
  threshold_description: string
  data_status: 'available' | 'insufficient_data' | string
}

export interface AlertSummary {
  firing: Record<AlertSeverity, number>
  resolved_recent: number
  by_rule: Record<string, number>
}

export interface Domain {
  id: string
  tenant_id: string
  domain: string
  cert_id?: string | null
  status: string
  created_at: string
}

export interface BasicAuth {
  enabled: boolean
  username?: string
  password?: string
  password_hash?: string
}

export interface AccessControl {
  ip_whitelist: string[]
  basic_auth: BasicAuth
  require_jwt: boolean
}

export interface ProxyRule {
  id: string
  tenant_id: string
  domain_id: string
  path: string
  target_type: 'internal_ip' | 'virtual_ip' | 'node' | string
  target: string
  upstream_scheme: 'http' | 'https' | string
  upstream_ca_file?: string
  upstream_insecure_skip_verify: boolean
  access_control: AccessControl
  enabled: boolean
  created_at: string
}

// 「代理主机」= 一行一个域名 + 它的默认代理规则（NPM 风格单步模型）。
// rule_id 为空表示该域名尚未绑定任何代理规则。
export interface ProxyHost {
  id: string
  domain_id: string
  tenant_id: string
  domain: string
  cert_id?: string | null
  status: string
  rule_id?: string | null
  path: string
  target_type: 'internal_ip' | 'virtual_ip' | 'node' | string
  target: string
  upstream_scheme: 'http' | 'https' | string
  upstream_ca_file?: string
  upstream_insecure_skip_verify: boolean
  access_control: AccessControl
  enabled: boolean
  rule_count: number
  created_at: string
}

export interface ProxyHostInput {
  domain: string
  cert_id?: string | null
  status?: string
  path?: string
  target_type: string
  target: string
  upstream_scheme?: 'http' | 'https'
  upstream_ca_file?: string
  upstream_insecure_skip_verify?: boolean
  access_control: AccessControl
  enabled?: boolean
}

export type ProxyHostList = Paged<ProxyHost>

export interface TrafficLog {
  id: string
  tenant_id: string
  node_id: string
  direction: 'in' | 'out' | string
  bytes: number
  protocol: string
  peer: string
  created_at: string
}

export interface AuditLog {
  id: string
  tenant_id?: string | null
  user_id?: string | null
  action: string
  resource: string
  detail: unknown
  ip: string
  created_at: string
}

export interface NodeTrafficTotal {
  in_bytes: number
  out_bytes: number
  window_hours: number
}

export interface NodeTrafficPoint {
  ts: string
  in: number
  out: number
}

export interface NodeMetrics {
  node_id: string
  status: string
  last_seen?: string | null
  heartbeat_interval_seconds: number
  uptime_seconds_since_register: number
  traffic: NodeTrafficTotal
  recent_traffic: NodeTrafficPoint[]
}

export interface RelayServer {
  id: string
  name: string
  endpoint: string
  region: string
  status: 'online' | 'offline' | string
  last_seen?: string | null
}

export interface NetworkTunnelSummary {
  total: number
  up: number
  down: number
  basis: 'member_status' | string
}

export interface NetworkStatus {
  network_id: string
  member_count: number
  online_member_count: number
  tunnels: NetworkTunnelSummary
}

export interface HealthStatus {
  status: string
  db: string
  version: string
  uptime: number
}

export interface ConfigPeer {
  node_id: string
  public_key: string
  endpoint: string
  allowed_ips: string[]
  virtual_ip: string
}

export interface AgentConfig {
  version: number
  node: { id: string; name: string; virtual_ip: string; public_endpoint?: string }
  network?: {
    id: string
    name: string
    cidr: string
    network_secret?: string
    peers: ConfigPeer[]
  } | null
  proxy_rules: ProxyRule[]
  acl: AclRule[]
  routes: SubnetRoute[]
  policy_filtered: boolean
  wireguard_config?: string
}

export interface ConfigVersion {
  id: string
  target_type: 'node' | 'network' | 'proxy' | string
  target_id: string
  version: number
  reason?: string
  summary?: unknown
  created_at: string
}

// 「远程桌面」：自建 RustDesk 服务器的接入参数与设备视图。
// 后端只下发公钥（public_key），private key 永不出现在任何响应里。
export interface RemoteDesktopConfig {
  enabled: boolean
  id_server: string
  relay_server: string
  public_key: string
  /** 服务器参数是否就绪（公钥文件可读且非空）。false 时 UI 显示「服务器未就绪」。 */
  available: boolean
  hint: string
  ports: number[]
}

export interface RemoteDesktopDevice {
  id: string
  name: string
  status: string
  virtual_ip?: string | null
  last_seen?: string | null
  heartbeat_stale: boolean
  platform: string
  os: string
  arch: string
  /** 该设备上报的 RustDesk ID（未上报为空）。 */
  rustdesk_id: string
  rustdesk_hint: string
  /** 客户端连接深链；无 ID 时为空串。Web 侧不使用（连接由客户端发起）。 */
  connect_url: string
  /** 可一键复制的连接参数文本（含服务器与公钥）。 */
  connection_params: string
}

export interface RemoteDesktopDeviceList {
  items: RemoteDesktopDevice[]
  total: number
}

export interface RemoteDesktopPortStatus {
  port: number
  target: string
  reachable: boolean
  error?: string
}

export interface RemoteDesktopStatus {
  id_server_host: string
  relay_server_host: string
  ports: RemoteDesktopPortStatus[]
  reachable: boolean
  checked_at: string
}

// 自托管 hbbs/hbbr 的端点（按需模式下未启用时 listening=false 属正常）。
export interface RemoteDesktopServerEndpoint {
  port: number
  protocol: 'tcp' | 'udp'
  owner: 'hbbs' | 'hbbr'
  listening: boolean
}

export type RemoteDesktopServerMode = 'on_demand' | 'always_on' | 'off'

// 自托管服务端状态（GET /remote-desktop/server-status）。
export interface RemoteDesktopServerStatus {
  mode: RemoteDesktopServerMode
  running: boolean
  /** 是否为 admin 手动保持（不受空闲回收影响）。 */
  manual: boolean
  ports: RemoteDesktopServerEndpoint[]
  listening_ports: number[]
  last_activity?: string | null
  idle_timeout_seconds: number
  idle_remaining_seconds?: number | null
  started_at?: string | null
  key_dir: string
  public_key_path: string
  public_key_ready: boolean
  last_error?: string
}

// 每台设备的能力授权状态（后端权威）。对应
// GET /api/v1/remote-desktop/device-policies 与 PATCH 的元素。
export type RemoteDesktopTunnelMode = 'auto' | 'direct' | 'relay'

export interface RemoteDesktopIsolatedTunnel {
  enabled: boolean
  stream_rule_id: string | null
}

export interface RemoteDesktopMeshMembership {
  joined: boolean
  network_id: string | null
  virtual_ip: string | null
}

export interface RemoteDesktopDevicePolicy {
  node_id: string
  /** 被控方授权开关；false 时任何客户端都不得连接该设备。默认 false（opt-in）。 */
  remote_control_allowed: boolean
  tunnel_mode: RemoteDesktopTunnelMode
  isolated_tunnel: RemoteDesktopIsolatedTunnel
  mesh: RemoteDesktopMeshMembership
  /** 只读展示：ready / degraded / unavailable。 */
  readonly: { subnet_routes: string }
}

export interface RemoteDesktopDevicePolicyList {
  items: RemoteDesktopDevicePolicy[]
  total: number
}

/** PATCH 局部更新请求体：只带被改动的字段。 */
export interface RemoteDesktopDevicePolicyPatch {
  remote_control_allowed?: boolean
  tunnel_mode?: RemoteDesktopTunnelMode
  isolated_tunnel_enabled?: boolean
  mesh_joined?: boolean
  mesh_network_id?: string
}


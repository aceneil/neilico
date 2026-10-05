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

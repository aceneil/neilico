import type { Role } from '@/types/api'

const writeRoles: Role[] = ['platform_admin', 'tenant_admin', 'ops']

export function canManageNodes(role: Role | null): boolean {
  return role ? writeRoles.includes(role) : false
}

export function canManageNetworks(role: Role | null): boolean {
  return role ? writeRoles.includes(role) : false
}

export function canManageProxy(role: Role | null): boolean {
  return role ? writeRoles.includes(role) : false
}

export function canManageAPITokens(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}

export function canManageCertificates(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}

export function canIssueNodeCertificates(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin' || role === 'ops'
}

export function canManageUsers(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}

export function isPlatformAdmin(role: Role | null): boolean {
  return role === 'platform_admin'
}

export function canPreviewAgentConfig(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}


export function canManageRelayServers(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}

// 「远程桌面」的服务器参数是全局基础设施，只有平台管理员可改；其余角色只读。
export function canManageRemoteDesktop(role: Role | null): boolean {
  return role === 'platform_admin'
}

// 「远程桌面」设备授权开关：平台管理员与本租户管理员可改；其余角色只读。
export function canManageRemoteDesktopPolicies(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin'
}

export function canEvaluateAlerts(role: Role | null): boolean {
  return role === 'platform_admin' || role === 'tenant_admin' || role === 'ops'
}

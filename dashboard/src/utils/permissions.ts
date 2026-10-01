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

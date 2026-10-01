import { http } from '@/api/http'
import type {
  AclRule,
  AgentConfig,
  NetworkMember,
  Paged,
  SubnetRoute,
  VirtualNetwork
} from '@/types/api'

export interface NetworkInput { name: string; cidr: string }
export interface MemberInput { node_id: string; virtual_ip?: string; role?: string }
export interface AclInput {
  src: string
  dst: string
  action: string
  protocol: string
  ports: string
  priority: number
}
export interface RouteInput { node_id: string; cidr: string; enabled: boolean }

export const networksApi = {
  list() {
    return http.get<Paged<VirtualNetwork>>('/networks').then((response) => response.data)
  },
  get(id: string) {
    return http.get<VirtualNetwork>(`/networks/${id}`).then((response) => response.data)
  },
  create(input: NetworkInput) {
    return http.post<VirtualNetwork>('/networks', input).then((response) => response.data)
  },
  update(id: string, input: NetworkInput) {
    return http.put<VirtualNetwork>(`/networks/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/networks/${id}`)
  },
  members(id: string) {
    return http.get<Paged<NetworkMember>>(`/networks/${id}/members`).then((response) => response.data)
  },
  addMember(id: string, input: MemberInput) {
    return http.post<NetworkMember>(`/networks/${id}/members`, input).then((response) => response.data)
  },
  removeMember(id: string, nodeId: string) {
    return http.delete<void>(`/networks/${id}/members/${nodeId}`)
  },
  acl(id: string) {
    return http.get<Paged<AclRule>>(`/networks/${id}/acl`).then((response) => response.data)
  },
  addAcl(id: string, input: AclInput) {
    return http.post<AclRule>(`/networks/${id}/acl`, input).then((response) => response.data)
  },
  removeAcl(id: string, ruleId: string) {
    return http.delete<void>(`/networks/${id}/acl/${ruleId}`)
  },
  routes(id: string) {
    return http.get<Paged<SubnetRoute>>(`/networks/${id}/routes`).then((response) => response.data)
  },
  addRoute(id: string, input: RouteInput) {
    return http.post<SubnetRoute>(`/networks/${id}/routes`, input).then((response) => response.data)
  },
  updateRoute(id: string, routeId: string, input: RouteInput) {
    return http.put<SubnetRoute>(`/networks/${id}/routes/${routeId}`, input).then((response) => response.data)
  },
  removeRoute(id: string, routeId: string) {
    return http.delete<void>(`/networks/${id}/routes/${routeId}`)
  },
  agentConfig(nodeId: string) {
    return http
      .get<AgentConfig>('/agent/config', { params: { node_id: nodeId, version: 0 } })
      .then((response) => response.data)
  }
}

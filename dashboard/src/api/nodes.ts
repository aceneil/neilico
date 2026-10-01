import { http } from '@/api/http'
import type { Node, NodeMetrics, NodeRegisterResult, Paged } from '@/types/api'

export interface NodeListQuery {
  status?: string
  tag?: string
  keyword?: string
  page?: number
  page_size?: number
}

export interface NodeRegisterInput {
  name: string
  os: string
  arch: string
  version: string
  tags: string[]
  public_key?: string
}

export const nodesApi = {
  list(params: NodeListQuery = {}) {
    return http.get<Paged<Node>>('/nodes', { params }).then((response) => response.data)
  },
  get(id: string) {
    return http.get<Node>(`/nodes/${id}`).then((response) => response.data)
  },
  metrics(id: string, windowHours = 24) {
    return http
      .get<NodeMetrics>(`/nodes/${id}/metrics`, { params: { window_hours: windowHours } })
      .then((response) => response.data)
  },
  register(input: NodeRegisterInput) {
    return http.post<NodeRegisterResult>('/nodes/register', input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/nodes/${id}`)
  }
}

import { http } from '@/api/http'
import type { AgentConfig, ConfigVersion, HealthStatus, Paged, RelayServer } from '@/types/api'

export const systemApi = {
  health() {
    return http.get<HealthStatus>('/healthz', { baseURL: import.meta.env.DEV ? '/' : (import.meta.env.VITE_API_BASE || '/') }).then((response) => response.data)
  },
  configVersions(targetType: 'node' | 'network' | 'proxy', targetId: string) {
    return http
      .get<Paged<ConfigVersion>>('/configs', { params: { target_type: targetType, target_id: targetId } })
      .then((response) => response.data)
  },
  agentConfig(nodeId: string) {
    return http
      .get<AgentConfig>('/agent/config', { params: { node_id: nodeId, version: 0 } })
      .then((response) => response.data)
  }
}

export interface RelayServerInput {
  name: string
  endpoint: string
  region: string
  last_seen?: string | null
}

export const relayServersApi = {
  list() {
    return http.get<{ items: RelayServer[]; total: number }>('/relay-servers').then((response) => response.data)
  },
  create(input: RelayServerInput) {
    return http.post<RelayServer>('/relay-servers', input).then((response) => response.data)
  },
  update(id: string, input: RelayServerInput) {
    return http.put<RelayServer>(`/relay-servers/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/relay-servers/${id}`)
  }
}

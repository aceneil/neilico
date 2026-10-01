import { http } from '@/api/http'
import type { AgentConfig, ConfigVersion, HealthStatus, Paged } from '@/types/api'

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

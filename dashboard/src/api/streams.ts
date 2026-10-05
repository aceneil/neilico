import { http } from '@/api/http'
import type { StreamRuleList, StreamRule } from '@/types/api'

export interface StreamRuleInput {
  name: string
  protocol: 'tcp' | 'udp'
  listen_port: number
  target_type: 'node' | 'virtual_ip' | 'internal_ip'
  target: string
  ip_whitelist?: string[]
  enabled?: boolean
}

export const streamsApi = {
  list(params: { page?: number; page_size?: number } = {}) {
    return http
      .get<StreamRuleList>('/stream-rules', { params: { page_size: 100, ...params } })
      .then((response) => response.data)
  },
  create(input: StreamRuleInput) {
    return http.post<StreamRule>('/stream-rules', input).then((response) => response.data)
  },
  update(id: string, input: StreamRuleInput) {
    return http.put<StreamRule>(`/stream-rules/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/stream-rules/${id}`)
  }
}

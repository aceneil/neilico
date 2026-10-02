import { http } from '@/api/http'
import type { AccessControl, Paged, ProxyRule } from '@/types/api'

export interface ProxyRuleInput {
  domain_id: string
  path: string
  target_type: string
  target: string
  upstream_scheme: 'http' | 'https'
  upstream_ca_file?: string
  upstream_insecure_skip_verify: boolean
  access_control: AccessControl
  enabled: boolean
}

export const proxyApi = {
  rules(params: { page?: number; page_size?: number } = {}) {
    return http.get<Paged<ProxyRule>>('/proxy-rules', { params }).then((response) => response.data)
  },
  create(input: ProxyRuleInput) {
    return http.post<ProxyRule>('/proxy-rules', input).then((response) => response.data)
  },
  update(id: string, input: ProxyRuleInput) {
    return http.put<ProxyRule>(`/proxy-rules/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/proxy-rules/${id}`)
  },
  providers() {
    return http.get<Record<string, unknown>>('/proxy/providers').then((response) => response.data)
  },
  render() {
    return http.post<unknown>('/proxy/render', {}).then((response) => response.data)
  }
}

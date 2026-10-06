import { http } from '@/api/http'
import type { ProxyHost, ProxyHostInput, ProxyHostList } from '@/types/api'

// Nginx Proxy Manager 风格的「代理主机」：一次提交完成「域名 → 地址:端口」绑定。
export const proxyHostsApi = {
  list(params: { page?: number; page_size?: number } = {}) {
    return http.get<ProxyHostList>('/proxy-hosts', { params }).then((response) => response.data)
  },
  get(id: string) {
    return http.get<ProxyHost>(`/proxy-hosts/${id}`).then((response) => response.data)
  },
  create(input: ProxyHostInput) {
    return http.post<ProxyHost>('/proxy-hosts', input).then((response) => response.data)
  },
  update(id: string, input: ProxyHostInput) {
    return http.put<ProxyHost>(`/proxy-hosts/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/proxy-hosts/${id}`)
  }
}

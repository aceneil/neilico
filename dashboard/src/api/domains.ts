import { http } from '@/api/http'
import type { Domain, Paged } from '@/types/api'

export interface DomainInput {
  domain: string
  cert_id?: string | null
  status?: string
}

export const domainsApi = {
  list(params: { page?: number; page_size?: number } = {}) {
    return http.get<Paged<Domain>>('/domains', { params }).then((response) => response.data)
  },
  create(input: DomainInput) {
    return http.post<Domain>('/domains', input).then((response) => response.data)
  },
  update(id: string, input: DomainInput) {
    return http.put<Domain>(`/domains/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/domains/${id}`)
  }
}

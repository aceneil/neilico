import { http } from '@/api/http'
import type { Paged, Tenant } from '@/types/api'

export interface TenantInput { name: string; plan: string }

export const tenantsApi = {
  list() {
    return http.get<Paged<Tenant>>('/tenants').then((response) => response.data)
  },
  create(input: TenantInput) {
    return http.post<Tenant>('/tenants', input).then((response) => response.data)
  },
  update(id: string, input: TenantInput) {
    return http.put<Tenant>(`/tenants/${id}`, input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/tenants/${id}`)
  }
}

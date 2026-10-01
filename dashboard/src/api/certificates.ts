import { http } from '@/api/http'
import type { Certificate, Paged } from '@/types/api'

export interface CertificateInput {
  cert_pem: string
  key_pem: string
}

export const certificatesApi = {
  list(params: { domain?: string; page?: number; page_size?: number } = {}) {
    return http.get<Paged<Certificate>>('/certificates', { params }).then((response) => response.data)
  },
  import(input: CertificateInput) {
    return http.post<Certificate>('/certificates', input).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/certificates/${id}`)
  }
}

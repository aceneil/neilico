import { http } from '@/api/http'
import type {
  Certificate,
  CertificateActionResult,
  CertificateIssueInput,
  Paged
} from '@/types/api'

export interface CertificateInput {
  cert_pem: string
  key_pem: string
}

export interface RequestOptions {
  signal?: AbortSignal
}

export const certificatesApi = {
  list(params: { domain?: string; page?: number; page_size?: number } = {}) {
    return http.get<Paged<Certificate>>('/certificates', { params }).then((response) => response.data)
  },
  import(input: CertificateInput) {
    return http.post<Certificate>('/certificates', input).then((response) => response.data)
  },
  issue(input: CertificateIssueInput) {
    return http.post<CertificateActionResult>('/certificates', input).then((response) => response.data)
  },
  get(id: string, options: RequestOptions = {}) {
    return http.get<Certificate>(`/certificates/${id}`, { signal: options.signal }).then((response) => response.data)
  },
  renew(id: string) {
    return http.post<CertificateActionResult>(`/certificates/${id}/renew`).then((response) => response.data)
  },
  revoke(id: string) {
    return http.post<CertificateActionResult>(`/certificates/${id}/revoke`).then((response) => response.data)
  },
  remove(id: string) {
    return http.delete<void>(`/certificates/${id}`)
  }
}

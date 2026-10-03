import { http } from '@/api/http'
import type {
  EnrollToken,
  EnrollTokenCreateInput,
  EnrollTokenCreateResult,
  Paged
} from '@/types/api'

export const enrollTokensApi = {
  list() {
    return http.get<Paged<EnrollToken>>('/enroll-tokens').then((response) => response.data)
  },
  create(input: EnrollTokenCreateInput) {
    return http.post<EnrollTokenCreateResult>('/enroll-tokens', input).then((response) => response.data)
  },
  revoke(id: string) {
    return http.delete<void>(`/enroll-tokens/${id}`)
  }
}

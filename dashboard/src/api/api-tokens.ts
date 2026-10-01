import { http } from '@/api/http'
import type { APIToken, APITokenCreateResult, Paged } from '@/types/api'

export interface APITokenInput {
  name: string
  scopes: string[]
  expires_in_days?: number
}

export const apiTokensApi = {
  list() {
    return http.get<Paged<APIToken>>('/api-tokens').then((response) => response.data)
  },
  create(input: APITokenInput) {
    return http.post<APITokenCreateResult>('/api-tokens', input).then((response) => response.data)
  },
  revoke(id: string) {
    return http.delete<{ api_token: APIToken; already_revoked: boolean }>(`/api-tokens/${id}`).then((response) => response.data)
  },
  rotate(id: string) {
    return http.post<APITokenCreateResult>(`/api-tokens/${id}/rotate`).then((response) => response.data)
  }
}

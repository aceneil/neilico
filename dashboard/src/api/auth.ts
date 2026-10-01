import { http } from '@/api/http'
import type { AuthResponse } from '@/types/api'

export const authApi = {
  login(email: string, password: string) {
    return http.post<AuthResponse>('/auth/login', { email, password }).then((response) => response.data)
  },
  refreshToken(refresh_token: string) {
    return http.post<AuthResponse>('/auth/refresh', { refresh_token }).then((response) => response.data)
  }
}

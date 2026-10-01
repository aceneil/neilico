import axios, { AxiosError, type AxiosRequestConfig } from 'axios'
import { message } from 'ant-design-vue'
import { authStorage } from '@/api/auth-storage'
import type { AuthResponse } from '@/types/api'

const apiOrigin = (import.meta.env.VITE_API_BASE || '').replace(/\/$/, '')
const baseURL = import.meta.env.DEV ? '/api/v1' : `${apiOrigin}/api/v1`

export const http = axios.create({
  baseURL,
  timeout: 15000,
  headers: { 'Content-Type': 'application/json' }
})

interface RetryableConfig extends AxiosRequestConfig {
  _retry?: boolean
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const backendMessage = (error.response?.data as { error?: { message?: string } } | undefined)?.error?.message
    return backendMessage || error.message
  }
  return error instanceof Error ? error.message : '请求失败'
}

http.interceptors.request.use((config) => {
  const token = authStorage.token()
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

http.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const config = error.config as RetryableConfig | undefined
    if (error.response?.status === 401 && config && !config._retry && authStorage.refreshToken()) {
      config._retry = true
      try {
        const refreshURL = `${baseURL}/auth/refresh`
        const refreshed = await axios.post<AuthResponse>(
          refreshURL,
          { refresh_token: authStorage.refreshToken() },
          { timeout: 15000 }
        )
        authStorage.updateSession(refreshed.data)
        config.headers = config.headers ?? {}
        config.headers.Authorization = `Bearer ${refreshed.data.token}`
        return http.request(config)
      } catch {
        authStorage.clear()
        if (!window.location.pathname.startsWith('/login')) {
          void import('@/router').then(({ default: router }) => router.push('/login'))
        }
      }
    }

    const status = error.response?.status
    if (status === 401) {
      authStorage.clear()
      if (!window.location.pathname.startsWith('/login')) {
        void import('@/router').then(({ default: router }) => router.push('/login'))
      }
    }
    if ([401, 403, 409, 422].includes(status ?? 0)) {
      message.error(errorMessage(error))
    }
    return Promise.reject(error)
  }
)

export function apiErrorMessage(error: unknown): string {
  return errorMessage(error)
}

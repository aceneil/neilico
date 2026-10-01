import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { authStorage } from '@/api/auth-storage'
import type { AuthResponse, AuthUser, Role } from '@/types/api'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(authStorage.token() || '')
  const refreshTokenValue = ref(authStorage.refreshToken() || '')
  const user = ref<AuthUser | null>(authStorage.user())

  const isAuthenticated = computed(() => Boolean(token.value))
  const role = computed<Role | null>(() => user.value?.role ?? null)

  function applySession(session: AuthResponse, remember = true) {
    authStorage.setSession(session, remember)
    token.value = session.token
    refreshTokenValue.value = session.refresh_token
    user.value = session.user
  }

  async function login(email: string, password: string, remember = true) {
    applySession(await authApi.login(email, password), remember)
  }

  async function refreshSession() {
    const session = await authApi.refreshToken(refreshTokenValue.value)
    authStorage.updateSession(session)
    token.value = session.token
    refreshTokenValue.value = session.refresh_token
    user.value = session.user
    return session
  }

  function logout() {
    authStorage.clear()
    token.value = ''
    refreshTokenValue.value = ''
    user.value = null
  }

  return {
    token,
    refreshToken: refreshTokenValue,
    user,
    role,
    isAuthenticated,
    login,
    logout,
    refreshTokenAction: refreshSession
  }
})

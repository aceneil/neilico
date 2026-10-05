import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { setupApi } from '@/api/account'
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

  // 首次登入 = 注册：创建第一个管理员并直接建立会话。
  async function register(email: string, password: string) {
    applySession(await setupApi.register(email, password), true)
  }

  // 改邮箱后同步本地缓存的用户信息（令牌无需变化）。
  function applyAccount(next: AuthUser) {
    user.value = next
    authStorage.updateUser(next)
  }

  // 轮换密码：后端返回带新 token_version 的会话，替换本地凭据以保持登录。
  function applyRotatedSession(session: AuthResponse) {
    authStorage.updateSession(session)
    token.value = session.token
    refreshTokenValue.value = session.refresh_token
    user.value = session.user
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
    register,
    applyAccount,
    applyRotatedSession,
    logout,
    refreshTokenAction: refreshSession
  }
})

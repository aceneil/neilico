import type { AuthResponse, AuthUser } from '@/types/api'

const TOKEN_KEY = 'umpp.access_token'
const REFRESH_KEY = 'umpp.refresh_token'
const USER_KEY = 'umpp.user'
const REMEMBER_KEY = 'umpp.remember'

function storage(): Storage {
  return localStorage.getItem(REMEMBER_KEY) === 'false' ? sessionStorage : localStorage
}

function read(key: string): string | null {
  return storage().getItem(key) ?? localStorage.getItem(key) ?? sessionStorage.getItem(key)
}

export const authStorage = {
  setSession(session: AuthResponse, remember: boolean) {
    this.clear()
    localStorage.setItem(REMEMBER_KEY, String(remember))
    const target = remember ? localStorage : sessionStorage
    target.setItem(TOKEN_KEY, session.token)
    target.setItem(REFRESH_KEY, session.refresh_token)
    target.setItem(USER_KEY, JSON.stringify(session.user))
  },
  token: () => read(TOKEN_KEY),
  refreshToken: () => read(REFRESH_KEY),
  user(): AuthUser | null {
    const raw = read(USER_KEY)
    if (!raw) return null
    try {
      return JSON.parse(raw) as AuthUser
    } catch {
      return null
    }
  },
  updateSession(session: AuthResponse) {
    const target = storage()
    target.setItem(TOKEN_KEY, session.token)
    target.setItem(REFRESH_KEY, session.refresh_token)
    target.setItem(USER_KEY, JSON.stringify(session.user))
  },
  clear() {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(REFRESH_KEY)
    localStorage.removeItem(USER_KEY)
    sessionStorage.removeItem(TOKEN_KEY)
    sessionStorage.removeItem(REFRESH_KEY)
    sessionStorage.removeItem(USER_KEY)
  }
}

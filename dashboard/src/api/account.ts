import { http } from '@/api/http'
import type {
  AccountEmailResult,
  AccountResponse,
  AuthResponse,
  PasswordRotateResult,
  SetupStatus
} from '@/types/api'

// 「初始化状态」与「首次注册」：注册页在未初始化时可用。
export const setupApi = {
  status() {
    return http.get<SetupStatus>('/setup/status').then((response) => response.data)
  },
  register(email: string, password: string) {
    return http.post<AuthResponse>('/setup/register', { email, password }).then((response) => response.data)
  }
}

// 登录后的「自助账号管理」：改邮箱 / 轮换密码（均需当前密码）。
export const accountApi = {
  get() {
    return http.get<AccountResponse>('/account').then((response) => response.data)
  },
  changeEmail(current_password: string, email: string) {
    return http
      .put<AccountEmailResult>('/account/email', { current_password, email })
      .then((response) => response.data)
  },
  rotatePassword(current_password: string, new_password: string) {
    return http
      .post<PasswordRotateResult>('/account/password/rotate', { current_password, new_password })
      .then((response) => response.data)
  }
}

// 前端与后端一致的密码强度校验（>=16 字符，且至少混合三类字符）。
export function passwordStrengthError(password: string, policy?: { min_length: number; min_classes: number }): string {
  const minLength = policy?.min_length ?? 16
  const minClasses = policy?.min_classes ?? 3
  if (password.length < minLength) return `密码至少需要 ${minLength} 个字符`
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((re) => re.test(password)).length
  if (classes < minClasses) return `密码需包含大写字母、小写字母、数字、符号中的至少 ${minClasses} 类`
  return ''
}

import { http } from '@/api/http'
import type { Paged, Role, User } from '@/types/api'

export interface UserInput {
  tenant_id?: string
  email: string
  password?: string
  role: Role
  status: string
}

export const usersApi = {
  list(params: { page?: number; page_size?: number } = {}) {
    return http.get<Paged<User>>('/users', { params }).then((response) => response.data)
  },
  create(input: UserInput) {
    return http.post<User>('/users', input).then((response) => response.data)
  },
  update(id: string, input: UserInput) {
    return http.put<User>(`/users/${id}`, input).then((response) => response.data)
  }
}

import { http } from '@/api/http'
import type { AuditLog, Paged, TrafficLog } from '@/types/api'

export interface TrafficQuery {
  node_id?: string
  from?: string
  to?: string
  page?: number
  page_size?: number
}

export const logsApi = {
  traffic(params: TrafficQuery = {}) {
    return http.get<Paged<TrafficLog>>('/traffic', { params }).then((response) => response.data)
  }
}

export interface AuditQuery {
  tenant_id?: string
  user_id?: string
  action?: string
  resource?: string
  from?: string
  to?: string
  page?: number
  page_size?: number
}

export const auditLogsApi = {
  list(params: AuditQuery = {}) {
    return http.get<Paged<AuditLog>>('/audit-logs', { params }).then((response) => response.data)
  }
}

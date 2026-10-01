import { http } from '@/api/http'
import type { Alert, AlertDetail, AlertRule, AlertSummary, Paged } from '@/types/api'

export interface AlertQuery {
  state?: 'firing' | 'resolved'
  severity?: 'critical' | 'warning' | 'info'
  rule?: string
  target_type?: string
  target_id?: string
  page?: number
  page_size?: number
}

export interface AlertEvaluationResult {
  items: Alert[]
  total: number
  insufficient_data: Alert[]
  evaluated_at?: string | null
}

export const alertsApi = {
  list(params: AlertQuery = {}) {
    return http.get<Paged<Alert>>('/alerts', { params }).then((response) => response.data)
  },
  rules() {
    return http.get<{ items: AlertRule[]; total: number; evaluation_interval_seconds: number }>('/alerts/rules')
      .then((response) => response.data)
  },
  summary() {
    return http.get<AlertSummary>('/alerts/summary').then((response) => response.data)
  },
  evaluate() {
    return http.post<AlertEvaluationResult>('/alerts/evaluate').then((response) => response.data)
  },
  detail(id: string) {
    return http.get<AlertDetail>(`/alerts/${id}`).then((response) => response.data)
  }
}

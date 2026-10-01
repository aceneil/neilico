import { http } from '@/api/http'
import type { Paged, TrafficLog } from '@/types/api'

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

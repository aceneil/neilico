import { http } from '@/api/http'

export interface WebProxyCandidate {
  kind: 'nginx' | 'caddy' | 'traefik'
  runtime: 'native' | 'docker'
  name: string
  image?: string
  binary?: string
  config_dir?: string
  writable: boolean
  notes?: string[]
}

export interface WebProxyDetectResult {
  candidates: WebProxyCandidate[]
  host_access: boolean
  hints?: string[]
}

export interface WebProxySiteInput {
  kind: string
  runtime: string
  name: string
  domain: string
  target: string
  tls?: boolean
  websocket?: boolean
  dry_run?: boolean
}

export interface WebProxyPlan {
  candidate: WebProxyCandidate
  domain: string
  path: string
  content: string
  validated: boolean
  validation?: string
  applied: boolean
  reloaded: boolean
  rolled_back?: boolean
}

export const webProxyApi = {
  detect() {
    return http.get<WebProxyDetectResult>('/web-proxy/detect').then((r) => r.data)
  },
  preview(input: WebProxySiteInput) {
    return http
      .post<{ candidate: WebProxyCandidate; content: string; path: string }>('/web-proxy/preview', input)
      .then((r) => r.data)
  },
  apply(input: WebProxySiteInput) {
    return http.post<WebProxyPlan>('/web-proxy/apply', input).then((r) => r.data)
  }
}

import { http } from '@/api/http'
import type { NodeCertificate, PKICA } from '@/types/api'

export const pkiApi = {
  ca() {
    return http.get<PKICA>('/pki/ca').then((response) => response.data)
  },
  rotateCA() {
    return http.post<PKICA>('/pki/ca/rotate').then((response) => response.data)
  },
  nodeCertificate(nodeId: string) {
    return http.get<NodeCertificate>(`/nodes/${nodeId}/mtls`).then((response) => response.data)
  },
  issueNodeCertificate(nodeId: string) {
    return http
      .post<{ client_cert_pem: string; client_key_pem: string; certificate: NodeCertificate }>(
        `/nodes/${nodeId}/mtls`
      )
      .then((response) => response.data)
  }
}

import { http } from '@/api/http'
import type {
  RemoteDesktopConfig,
  RemoteDesktopDeviceList,
  RemoteDesktopStatus
} from '@/types/api'

export interface RemoteDesktopConfigInput {
  enabled?: boolean
  id_server?: string
  relay_server?: string
}

export const remoteDesktopApi = {
  /** 服务器参数（ID/中继服务器 + 公钥）。所有登录用户可读。 */
  config() {
    return http.get<RemoteDesktopConfig>('/remote-desktop/config').then((response) => response.data)
  },
  /** 修改服务器参数（仅 platform_admin）。 */
  update(input: RemoteDesktopConfigInput) {
    return http.put<RemoteDesktopConfig>('/remote-desktop/config', input).then((response) => response.data)
  },
  /** 设备列表 + 每台的可复制连接参数。 */
  devices() {
    return http.get<RemoteDesktopDeviceList>('/remote-desktop/devices').then((response) => response.data)
  },
  /** 服务器端口探活（21115/21116/21117）。 */
  status() {
    return http.get<RemoteDesktopStatus>('/remote-desktop/status').then((response) => response.data)
  }
}

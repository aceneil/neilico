import { http } from '@/api/http'
import type {
  RemoteDesktopConfig,
  RemoteDesktopDeviceList,
  RemoteDesktopDevicePolicy,
  RemoteDesktopDevicePolicyList,
  RemoteDesktopDevicePolicyPatch,
  RemoteDesktopServerStatus,
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
  },
  /** 每台设备的授权状态（可被远程 / 隧道模式 / 单独隧道 / Mesh）。登录可读。 */
  devicePolicies() {
    return http
      .get<RemoteDesktopDevicePolicyList>('/remote-desktop/device-policies')
      .then((response) => response.data)
  },
  /** 局部更新单台设备的授权状态（仅 admin）。 */
  updatePolicy(nodeId: string, patch: RemoteDesktopDevicePolicyPatch) {
    return http
      .patch<{ item: RemoteDesktopDevicePolicy }>(`/remote-desktop/device-policies/${nodeId}`, patch)
      .then((response) => response.data.item)
  },
  /** 自托管 hbbs/hbbr 的实时状态（运行中/已停止/空闲倒计时）。登录可读。 */
  serverStatus() {
    return http
      .get<RemoteDesktopServerStatus>('/remote-desktop/server-status')
      .then((response) => response.data)
  },
  /** 手动拉起自托管服务端（仅 admin）。 */
  serverStart() {
    return http
      .post<RemoteDesktopServerStatus>('/remote-desktop/server/start')
      .then((response) => response.data)
  },
  /** 手动停止自托管服务端（仅 admin）。 */
  serverStop() {
    return http
      .post<RemoteDesktopServerStatus>('/remote-desktop/server/stop')
      .then((response) => response.data)
  }
}

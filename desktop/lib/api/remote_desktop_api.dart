import '../models/device.dart';
import '../models/device_policy.dart';
import '../models/remote_desktop_config.dart';
import 'api_client.dart';

/// 「远程桌面」接口。
///
/// 已就绪（控制面 P2 已提供）：
///   GET /api/v1/remote-desktop/config
///   GET /api/v1/remote-desktop/devices
///   GET /api/v1/remote-desktop/status
///
/// 约定中、后端尚未提供（调用会抛 [ApiException.isNotImplemented]）：
///   GET   /api/v1/remote-desktop/device-policies
///   PATCH /api/v1/remote-desktop/device-policies/{node_id}
/// 详见 `desktop/docs/api-contract.md`。
class RemoteDesktopApi {
  RemoteDesktopApi(this._client);

  final ApiClient _client;

  /// 服务器参数（只含公钥）。所有登录用户可读。
  Future<RemoteDesktopConfig> config() async {
    final json = await _client.getJson('/api/v1/remote-desktop/config');
    return RemoteDesktopConfig.fromJson(json);
  }

  /// 设备列表 + 每台设备的可复制连接参数。
  Future<RemoteDesktopDeviceList> devices() async {
    final json = await _client.getJson('/api/v1/remote-desktop/devices');
    return RemoteDesktopDeviceList.fromJson(json);
  }

  /// 服务器端口探活（21115/21116/21117）。
  Future<RemoteDesktopStatus> status() async {
    final json = await _client.getJson('/api/v1/remote-desktop/status');
    return RemoteDesktopStatus.fromJson(json);
  }

  /// 每台设备的授权状态（可被远程 / 隧道模式 / 单独隧道 / Mesh）。
  ///
  /// 后端未实现时抛 [ApiException.isNotImplemented]，由调用方转成
  /// [DevicePolicySnapshot.unavailable] —— **不返回本地默认值**。
  Future<DevicePolicySnapshot> devicePolicies() async {
    final json = await _client.getJson('/api/v1/remote-desktop/device-policies');
    return DevicePolicySnapshot.fromJson(json);
  }

  /// 修改单台设备的授权状态（局部更新）。
  Future<DevicePolicy> updatePolicy(String nodeId, Map<String, dynamic> patch) async {
    final json = await _client.patchJson(
      '/api/v1/remote-desktop/device-policies/$nodeId',
      body: patch,
    );
    final payload = json['item'] ?? json;
    return DevicePolicy.fromJson(payload is Map ? payload.cast<String, dynamic>() : json);
  }
}

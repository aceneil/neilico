import '../util/json.dart';

/// 设备（节点）在远控视图下的状态。
enum DeviceStatus {
  online,
  offline,
  unknown;

  static DeviceStatus parse(Object? raw) {
    switch (asString(raw).trim().toLowerCase()) {
      case 'online':
      case 'active':
      case 'ready':
        return DeviceStatus.online;
      case 'offline':
      case 'inactive':
      case 'down':
        return DeviceStatus.offline;
      default:
        return DeviceStatus.unknown;
    }
  }

  String get label => switch (this) {
        DeviceStatus.online => '在线',
        DeviceStatus.offline => '离线',
        DeviceStatus.unknown => '未知',
      };
}

/// 单台设备的远控视图。
///
/// 字段来源：`GET /api/v1/remote-desktop/devices` 的 `items[]`
/// （`heartbeat_stale` 由服务端按心跳超时计算，客户端不自行推算）。
class RemoteDesktopDevice {
  const RemoteDesktopDevice({
    required this.id,
    required this.name,
    required this.status,
    required this.platform,
    required this.os,
    required this.arch,
    required this.rustdeskId,
    required this.connectUrl,
    required this.connectionParams,
    required this.heartbeatStale,
    this.virtualIp,
    this.lastSeen,
  });

  final String id;
  final String name;
  final DeviceStatus status;
  final String platform;
  final String os;
  final String arch;
  final String rustdeskId;
  final String connectUrl;
  final String connectionParams;
  final bool heartbeatStale;
  final String? virtualIp;
  final DateTime? lastSeen;

  bool get hasRustdeskId => rustdeskId.trim().isNotEmpty;

  bool get hasVirtualIp => (virtualIp ?? '').trim().isNotEmpty;

  /// 在线且心跳未过期；心跳过期时服务端已标记 `heartbeat_stale`，此处不重复判超时。
  bool get isReachable => status == DeviceStatus.online && !heartbeatStale;

  /// 展示名：名称为空时回落到短 ID，避免网格出现空白卡片。
  String get displayName => name.trim().isNotEmpty ? name.trim() : '未命名设备 $shortId';

  String get shortId => id.length <= 8 ? id : id.substring(0, 8);

  /// 平台标签：优先用后端给的 platform；缺失时用 os/arch 兜底。
  String get platformLabel {
    if (platform.trim().isNotEmpty) return platform.trim();
    if (os.trim().isEmpty && arch.trim().isEmpty) return '未知';
    if (arch.trim().isEmpty) return os.trim();
    if (os.trim().isEmpty) return arch.trim();
    return '${os.trim()}/${arch.trim()}';
  }

  factory RemoteDesktopDevice.fromJson(Map<String, dynamic> json) => RemoteDesktopDevice(
        id: asString(json['id']),
        name: asString(json['name']),
        status: DeviceStatus.parse(json['status']),
        platform: asString(json['platform']),
        os: asString(json['os']),
        arch: asString(json['arch']),
        rustdeskId: asString(json['rustdesk_id']),
        connectUrl: asString(json['connect_url']),
        connectionParams: asString(json['connection_params']),
        heartbeatStale: asBool(json['heartbeat_stale']),
        virtualIp: asNullableString(json['virtual_ip']),
        lastSeen: asDateTime(json['last_seen']),
      );
}

/// `GET /api/v1/remote-desktop/devices` 的响应体。
class RemoteDesktopDeviceList {
  const RemoteDesktopDeviceList({required this.items, required this.total});

  final List<RemoteDesktopDevice> items;
  final int total;

  factory RemoteDesktopDeviceList.fromJson(Map<String, dynamic> json) {
    final items = parseList(json['items'], RemoteDesktopDevice.fromJson);
    final total = json.containsKey('total') ? asInt(json['total'], fallback: items.length) : items.length;
    return RemoteDesktopDeviceList(items: items, total: total);
  }
}

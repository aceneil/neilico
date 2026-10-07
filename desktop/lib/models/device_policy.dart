import '../util/json.dart';

/// 隧道模式（对应后端的 `tunnel_mode`）。
///
/// NEILICO 的取舍：优先走我们自己的 Mesh 虚拟 IP（直连），
/// 失败/不可达时才回落到 hbbr 中继；`auto` 表示由服务端择优。
enum TunnelMode {
  auto,
  direct,
  relay;

  /// 与后端约定的线格式。
  String get wire => switch (this) {
        TunnelMode.auto => 'auto',
        TunnelMode.direct => 'direct',
        TunnelMode.relay => 'relay',
      };

  String get label => switch (this) {
        TunnelMode.auto => '自动',
        TunnelMode.direct => '直连（Mesh）',
        TunnelMode.relay => '中继',
      };

  String get description => switch (this) {
        TunnelMode.auto => '服务端择优：Mesh 可达走直连，否则回落中继',
        TunnelMode.direct => '强制走 Mesh 虚拟 IP 直连，不经 hbbr',
        TunnelMode.relay => '强制经 hbbr 中继转发',
      };

  static TunnelMode parse(Object? raw) {
    switch (asString(raw).trim().toLowerCase()) {
      case 'direct':
      case 'p2p':
      case 'mesh':
        return TunnelMode.direct;
      case 'relay':
      case 'relayed':
        return TunnelMode.relay;
      default:
        return TunnelMode.auto;
    }
  }
}

/// Mesh 成员身份。
class MeshMembership {
  const MeshMembership({required this.joined, this.networkId, this.virtualIp});

  final bool joined;
  final String? networkId;
  final String? virtualIp;

  factory MeshMembership.fromJson(Map<String, dynamic> json) => MeshMembership(
        joined: asBool(json['joined']),
        networkId: asNullableString(json['network_id']),
        virtualIp: asNullableString(json['virtual_ip']),
      );

  Map<String, dynamic> toJson() => {
        'joined': joined,
        'network_id': networkId,
        'virtual_ip': virtualIp,
      };
}

/// 「单独隧道」：为该设备单独开一条隧道 / 端口转发，与全局网络隔离。
/// 复用现有 StreamRule 能力（`stream_rule_id` 指向已有的转发规则），不另造一套。
class IsolatedTunnel {
  const IsolatedTunnel({required this.enabled, this.streamRuleId});

  final bool enabled;
  final String? streamRuleId;

  factory IsolatedTunnel.fromJson(Map<String, dynamic> json) => IsolatedTunnel(
        enabled: asBool(json['enabled']),
        streamRuleId: asNullableString(json['stream_rule_id']),
      );

  Map<String, dynamic> toJson() => {
        'enabled': enabled,
        'stream_rule_id': streamRuleId,
      };
}

/// 每台设备的能力授权状态（**权威状态在后端**）。
///
/// 该模型对应「需要的接口」——控制面尚未提供时的约定见
/// `desktop/docs/api-contract.md`：
///
///   GET   /api/v1/remote-desktop/device-policies
///   PATCH /api/v1/remote-desktop/device-policies/{node_id}
class DevicePolicy {
  const DevicePolicy({
    required this.nodeId,
    required this.remoteControlAllowed,
    required this.tunnelMode,
    required this.isolatedTunnel,
    required this.mesh,
    this.subnetRoutesStatus = '',
  });

  final String nodeId;

  /// 是否允许被远程控制。置 false 时**任何**客户端都不得对其发起连接。
  final bool remoteControlAllowed;
  final TunnelMode tunnelMode;
  final IsolatedTunnel isolatedTunnel;
  final MeshMembership mesh;

  /// 子网路由状态（只读展示：ready / degraded / unavailable）。
  final String subnetRoutesStatus;

  factory DevicePolicy.fromJson(Map<String, dynamic> json) => DevicePolicy(
        nodeId: asString(json['node_id'] ?? json['id']),
        remoteControlAllowed: asBool(json['remote_control_allowed'], fallback: true),
        tunnelMode: TunnelMode.parse(json['tunnel_mode']),
        isolatedTunnel: IsolatedTunnel.fromJson(asMap(json['isolated_tunnel'])),
        mesh: MeshMembership.fromJson(asMap(json['mesh'])),
        subnetRoutesStatus: asString(asMap(json['readonly'])['subnet_routes']),
      );

  DevicePolicy copyWith({
    bool? remoteControlAllowed,
    TunnelMode? tunnelMode,
    IsolatedTunnel? isolatedTunnel,
    MeshMembership? mesh,
  }) =>
      DevicePolicy(
        nodeId: nodeId,
        remoteControlAllowed: remoteControlAllowed ?? this.remoteControlAllowed,
        tunnelMode: tunnelMode ?? this.tunnelMode,
        isolatedTunnel: isolatedTunnel ?? this.isolatedTunnel,
        mesh: mesh ?? this.mesh,
        subnetRoutesStatus: subnetRoutesStatus,
      );

  /// 局部更新请求体（PATCH）。只带上被修改的字段。
  Map<String, dynamic> toPatchJson({
    bool? remoteControlAllowed,
    TunnelMode? tunnelMode,
    bool? isolatedTunnelEnabled,
    bool? meshJoined,
  }) {
    final body = <String, dynamic>{};
    if (remoteControlAllowed != null) body['remote_control_allowed'] = remoteControlAllowed;
    if (tunnelMode != null) body['tunnel_mode'] = tunnelMode.wire;
    if (isolatedTunnelEnabled != null) body['isolated_tunnel_enabled'] = isolatedTunnelEnabled;
    if (meshJoined != null) body['mesh_joined'] = meshJoined;
    return body;
  }
}

/// 一次拉取到的「全部设备授权状态」快照。
///
/// 关键设计：**要么整份可用，要么明确不可用并给出原因**。
/// 后端接口未就绪（404/501）时，客户端把开关全部置灰并展示 [reason]，
/// 绝不本地编造开关状态（mission 硬性要求）。
class DevicePolicySnapshot {
  const DevicePolicySnapshot._({
    required this.available,
    required this.reason,
    required this.byNode,
  });

  final bool available;
  final String reason;
  final Map<String, DevicePolicy> byNode;

  bool get isAvailable => available;

  static const DevicePolicySnapshot pending = DevicePolicySnapshot._(
    available: false,
    reason: '后端暂未提供设备授权接口（/api/v1/remote-desktop/device-policies）',
    byNode: <String, DevicePolicy>{},
  );

  static DevicePolicySnapshot unavailable(String reason) => DevicePolicySnapshot._(
        available: false,
        reason: reason,
        byNode: const <String, DevicePolicy>{},
      );

  static DevicePolicySnapshot ready(Iterable<DevicePolicy> policies) => DevicePolicySnapshot._(
        available: true,
        reason: '',
        byNode: {for (final policy in policies) policy.nodeId: policy},
      );

  DevicePolicy? forNode(String nodeId) => byNode[nodeId];

  DevicePolicySnapshot upsert(DevicePolicy policy) => DevicePolicySnapshot._(
        available: available,
        reason: reason,
        byNode: {...byNode, policy.nodeId: policy},
      );

  factory DevicePolicySnapshot.fromJson(Map<String, dynamic> json) {
    final items = parseList(json['items'], DevicePolicy.fromJson);
    return DevicePolicySnapshot.ready(items);
  }
}

import 'device.dart';
import 'device_policy.dart';
import 'remote_desktop_config.dart';

/// 开关被置灰的原因。UI 必须把原因如实展示给用户（不允许「静默禁用」）。
enum GateReason {
  ok,
  backendPending,
  deviceOffline,
  noVirtualIp,
  noMeshNetwork,
  serverNotReady,
  noRustdeskId,
  deniedByPolicy,
}

extension GateReasonText on GateReason {
  String get label => switch (this) {
        GateReason.ok => '',
        GateReason.backendPending => '后端接口未就绪',
        GateReason.deviceOffline => '设备离线或心跳过期',
        GateReason.noVirtualIp => '设备未分配虚拟 IP',
        GateReason.noMeshNetwork => '设备未加入任何虚拟网络',
        GateReason.serverNotReady => '远控服务器未就绪',
        GateReason.noRustdeskId => '设备未上报 RustDesk ID',
        GateReason.deniedByPolicy => '该设备未开启「可被远程」',
      };
}

/// 一个受后端约束的开关/入口：值 + 是否可操作 + 不可操作的原因。
class Gated<T> {
  const Gated.ready(this.value, {this.reason = GateReason.ok, this.detail = ''})
      : enabled = true;

  const Gated.blocked(this.reason, {this.detail = ''})
      : value = null,
        enabled = false;

  final T? value;
  final bool enabled;
  final GateReason reason;
  final String detail;

  /// 面向用户的提示文本：优先用更具体的 [detail]，否则用枚举文案。
  String get message => detail.trim().isNotEmpty ? detail.trim() : reason.label;
}

/// 单台设备的开关面板视图模型。
///
/// 全部规则集中在这里（纯逻辑、可单测）：UI 只负责渲染，不做判断。
/// 关键约束：
///  - 授权状态以后端为准；后端不可用时**全部置灰**并给出原因。
///  - 「可被远程」为 false 时，连接入口必须一并置灰（关闭时任何客户端都不得发起）。
class DeviceSwitchPanel {
  const DeviceSwitchPanel({
    required this.remoteControlAllowed,
    required this.tunnelMode,
    required this.isolatedTunnel,
    required this.meshJoined,
    required this.connect,
  });

  final Gated<bool> remoteControlAllowed;
  final Gated<TunnelMode> tunnelMode;
  final Gated<bool> isolatedTunnel;
  final Gated<bool> meshJoined;

  /// 连接入口：值为即将调起的 `rustdesk://<id>`。
  final Gated<String> connect;

  /// 除连接入口外，是否所有开关都处于可用状态（用于整卡提示）。
  bool get anyEditable =>
      remoteControlAllowed.enabled ||
      tunnelMode.enabled ||
      isolatedTunnel.enabled ||
      meshJoined.enabled;

  static DeviceSwitchPanel build({
    required RemoteDesktopDevice device,
    required DevicePolicySnapshot snapshot,
    required RemoteDesktopConfig config,
  }) {
    final policy = snapshot.forNode(device.id);

    // ① 后端未就绪：不猜、不伪造，一律置灰。
    if (!snapshot.available) {
      final blocked = Gated<bool>.blocked(GateReason.backendPending, detail: snapshot.reason);
      return DeviceSwitchPanel(
        remoteControlAllowed: blocked,
        tunnelMode: const Gated<TunnelMode>.blocked(GateReason.backendPending),
        isolatedTunnel: blocked,
        meshJoined: blocked,
        connect: Gated<String>.blocked(GateReason.backendPending, detail: snapshot.reason),
      );
    }

    // ② 后端可用但缺该设备的条目：同样不猜。
    if (policy == null) {
      const blocked = Gated<bool>.blocked(
        GateReason.backendPending,
        detail: '后端未返回该设备的授权状态',
      );
      return const DeviceSwitchPanel(
        remoteControlAllowed: blocked,
        tunnelMode: Gated<TunnelMode>.blocked(
          GateReason.backendPending,
          detail: '后端未返回该设备的授权状态',
        ),
        isolatedTunnel: blocked,
        meshJoined: blocked,
        connect: Gated<String>.blocked(
          GateReason.backendPending,
          detail: '后端未返回该设备的授权状态',
        ),
      );
    }

    final reachable = device.isReachable;

    // 「可被远程」是纯授权开关，设备离线时也允许改（不依赖在线）。
    final remoteControl = Gated<bool>.ready(policy.remoteControlAllowed);

    // 隧道模式切换需要在线重协商。
    final tunnel = reachable
        ? Gated<TunnelMode>.ready(policy.tunnelMode)
        : const Gated<TunnelMode>.blocked(GateReason.deviceOffline);

    // 单独隧道：既需要在线，也需要有虚拟 IP 作为转发目标。
    final isolated = !reachable
        ? const Gated<bool>.blocked(GateReason.deviceOffline)
        : (device.hasVirtualIp
            ? Gated<bool>.ready(policy.isolatedTunnel.enabled)
            : const Gated<bool>.blocked(GateReason.noVirtualIp));

    // Mesh 加入/退出：需要存在可加入的网络（来自后端授权状态）。
    final mesh = policy.mesh.networkId == null && !policy.mesh.joined
        ? const Gated<bool>.blocked(GateReason.noMeshNetwork)
        : Gated<bool>.ready(policy.mesh.joined);

    // 连接入口的完整门禁：策略允许 → 有 ID → 服务器就绪。
    final Gated<String> connect;
    if (!policy.remoteControlAllowed) {
      connect = const Gated<String>.blocked(GateReason.deniedByPolicy);
    } else if (!device.hasRustdeskId) {
      connect = const Gated<String>.blocked(GateReason.noRustdeskId);
    } else if (!config.usable) {
      connect = Gated<String>.blocked(GateReason.serverNotReady, detail: config.hint);
    } else {
      connect = Gated<String>.ready(
        device.connectUrl.isNotEmpty ? device.connectUrl : 'rustdesk://${device.rustdeskId}',
      );
    }

    return DeviceSwitchPanel(
      remoteControlAllowed: remoteControl,
      tunnelMode: tunnel,
      isolatedTunnel: isolated,
      meshJoined: mesh,
      connect: connect,
    );
  }
}

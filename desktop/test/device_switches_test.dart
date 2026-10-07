import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/models/device.dart';
import 'package:neilico_desktop/models/device_policy.dart';
import 'package:neilico_desktop/models/device_switches.dart';
import 'package:neilico_desktop/models/remote_desktop_config.dart';

import 'support/fake_backend.dart';

RemoteDesktopConfig usableConfig() => RemoteDesktopConfig.fromJson({
      'enabled': true,
      'id_server': 'rd.neilico.local',
      'relay_server': 'rd.neilico.local',
      'public_key': 'PUBKEY',
      'available': true,
      'hint': '服务器已就绪',
    });

RemoteDesktopConfig notReadyConfig() => RemoteDesktopConfig.fromJson({
      'enabled': true,
      'available': false,
      'hint': '服务器未就绪：未能读取公钥文件',
    });

DevicePolicySnapshot snapshotWith(Map<String, dynamic> policy) =>
    DevicePolicySnapshot.fromJson({
      'items': [policy],
      'total': 1,
    });

void main() {
  final online = RemoteDesktopDevice.fromJson(deviceJson());
  final offline = RemoteDesktopDevice.fromJson(deviceJson(status: 'offline'));
  final noVirtualIp = RemoteDesktopDevice.fromJson(deviceJson(virtualIp: null));

  group('后端接口未就绪时：一律置灰并说明原因', () {
    test('pending 快照 => 全部 blocked + backendPending', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: DevicePolicySnapshot.pending,
        config: usableConfig(),
      );
      expect(panel.remoteControlAllowed.enabled, isFalse);
      expect(panel.tunnelMode.enabled, isFalse);
      expect(panel.isolatedTunnel.enabled, isFalse);
      expect(panel.meshJoined.enabled, isFalse);
      expect(panel.connect.enabled, isFalse);
      expect(panel.remoteControlAllowed.reason, GateReason.backendPending);
      expect(panel.connect.message, contains('device-policies'));
      expect(panel.anyEditable, isFalse);
    });

    test('快照可用但没有该设备条目 => 仍置灰', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: snapshotWith(policyJson(nodeId: 'other-node')),
        config: usableConfig(),
      );
      expect(panel.remoteControlAllowed.enabled, isFalse);
      expect(panel.remoteControlAllowed.message, contains('未返回该设备'));
    });

    test('unavailable(reason) 保留后端给的原因', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: DevicePolicySnapshot.unavailable('当前账号无权查看设备授权状态'),
        config: usableConfig(),
      );
      expect(panel.isolatedTunnel.message, '当前账号无权查看设备授权状态');
    });
  });

  group('后端就绪 + 设备在线：开关可用', () {
    test('全部可编辑，连接入口给出 rustdesk:// 地址', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: snapshotWith(policyJson(nodeId: online.id, meshJoined: true)),
        config: usableConfig(),
      );
      expect(panel.anyEditable, isTrue);
      expect(panel.remoteControlAllowed.value, isTrue);
      expect(panel.tunnelMode.value, TunnelMode.auto);
      expect(panel.isolatedTunnel.value, isFalse);
      expect(panel.meshJoined.value, isTrue);
      expect(panel.connect.enabled, isTrue);
      expect(panel.connect.value, 'rustdesk://123456789');
    });
  });

  group('连接门禁', () {
    test('「可被远程」关闭 => 连接置灰 deniedByPolicy', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: snapshotWith(policyJson(nodeId: online.id, remoteControlAllowed: false)),
        config: usableConfig(),
      );
      expect(panel.connect.enabled, isFalse);
      expect(panel.connect.reason, GateReason.deniedByPolicy);
      // 授权开关本身仍可改（纯策略操作，不依赖在线）。
      expect(panel.remoteControlAllowed.enabled, isTrue);
    });

    test('服务器未就绪 => 连接置灰 serverNotReady 且带上 hint', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: snapshotWith(policyJson(nodeId: online.id)),
        config: notReadyConfig(),
      );
      expect(panel.connect.enabled, isFalse);
      expect(panel.connect.reason, GateReason.serverNotReady);
      expect(panel.connect.message, contains('未就绪'));
    });

    test('设备未上报 RustDesk ID => 连接置灰 noRustdeskId', () {
      final panel = DeviceSwitchPanel.build(
        device: RemoteDesktopDevice.fromJson(deviceJson(rustdeskId: '')),
        snapshot: snapshotWith(policyJson(nodeId: online.id)),
        config: usableConfig(),
      );
      expect(panel.connect.enabled, isFalse);
      expect(panel.connect.reason, GateReason.noRustdeskId);
    });
  });

  group('设备离线 / 缺虚拟 IP 的门禁', () {
    test('离线：隧道模式与单独隧道置灰（离线），授权开关仍可改', () {
      final panel = DeviceSwitchPanel.build(
        device: offline,
        snapshot: snapshotWith(policyJson(nodeId: offline.id)),
        config: usableConfig(),
      );
      expect(panel.remoteControlAllowed.enabled, isTrue);
      expect(panel.tunnelMode.reason, GateReason.deviceOffline);
      expect(panel.isolatedTunnel.reason, GateReason.deviceOffline);
    });

    test('在线但无虚拟 IP：单独隧道置灰 noVirtualIp', () {
      final panel = DeviceSwitchPanel.build(
        device: noVirtualIp,
        snapshot: snapshotWith(policyJson(nodeId: noVirtualIp.id)),
        config: usableConfig(),
      );
      expect(panel.isolatedTunnel.enabled, isFalse);
      expect(panel.isolatedTunnel.reason, GateReason.noVirtualIp);
      expect(panel.tunnelMode.enabled, isTrue);
    });

    test('未加入且无网络：Mesh 置灰 noMeshNetwork', () {
      final panel = DeviceSwitchPanel.build(
        device: online,
        snapshot: snapshotWith(
          policyJson(nodeId: online.id, meshJoined: false, networkId: null),
        ),
        config: usableConfig(),
      );
      expect(panel.meshJoined.enabled, isFalse);
      expect(panel.meshJoined.reason, GateReason.noMeshNetwork);
    });
  });

  group('策略解析与局部更新', () {
    test('未声明的 remote_control_allowed 默认允许（后端语义）', () {
      final policy = DevicePolicy.fromJson({'node_id': 'n-1'});
      expect(policy.remoteControlAllowed, isTrue);
      expect(policy.tunnelMode, TunnelMode.auto);
    });

    test('tunnel_mode 线格式与解析互为逆', () {
      for (final mode in TunnelMode.values) {
        expect(DevicePolicy.fromJson({'tunnel_mode': mode.wire}).tunnelMode, mode);
      }
      expect(DevicePolicy.fromJson({'tunnel_mode': 'relayed'}).tunnelMode, TunnelMode.relay);
      expect(DevicePolicy.fromJson({'tunnel_mode': 'mesh'}).tunnelMode, TunnelMode.direct);
    });

    test('toPatchJson 只带被改动的字段', () {
      const policy = DevicePolicy(
        nodeId: 'n-1',
        remoteControlAllowed: true,
        tunnelMode: TunnelMode.auto,
        isolatedTunnel: IsolatedTunnel(enabled: false),
        mesh: MeshMembership(joined: false),
      );
      expect(policy.toPatchJson(tunnelMode: TunnelMode.relay), {'tunnel_mode': 'relay'});
      expect(policy.toPatchJson(remoteControlAllowed: false), {'remote_control_allowed': false});
      expect(policy.toPatchJson(isolatedTunnelEnabled: true), {'isolated_tunnel_enabled': true});
      expect(policy.toPatchJson(meshJoined: true), {'mesh_joined': true});
    });
  });
}

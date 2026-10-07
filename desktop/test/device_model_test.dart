import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/models/device.dart';
import 'package:neilico_desktop/models/remote_desktop_config.dart';

import 'support/fake_backend.dart';

void main() {
  group('RemoteDesktopDevice 解析', () {
    test('完整字段', () {
      final device = RemoteDesktopDevice.fromJson(deviceJson());
      expect(device.name, '办公室主机');
      expect(device.status, DeviceStatus.online);
      expect(device.virtualIp, '10.42.0.7');
      expect(device.platformLabel, 'linux/x86_64');
      expect(device.rustdeskId, '123456789');
      expect(device.connectUrl, 'rustdesk://123456789');
      expect(device.hasRustdeskId, isTrue);
      expect(device.isReachable, isTrue);
      expect(device.lastSeen, isNotNull);
    });

    test('缺失/空值不崩：名称回落短 ID，虚拟 IP 与 ID 为空', () {
      final device = RemoteDesktopDevice.fromJson({
        'id': 'abcdefgh-1234-5678-9012-345678901234',
        'name': '',
        'status': null,
        'virtual_ip': '',
        'last_seen': null,
        'heartbeat_stale': null,
        'os': '',
        'arch': '',
        'rustdesk_id': '',
      });
      expect(device.displayName, '未命名设备 abcdefgh');
      expect(device.status, DeviceStatus.unknown);
      expect(device.virtualIp, isNull);
      expect(device.hasVirtualIp, isFalse);
      expect(device.hasRustdeskId, isFalse);
      expect(device.heartbeatStale, isFalse);
      expect(device.platformLabel, '未知');
      expect(device.lastSeen, isNull);
    });

    test('心跳过期 => 不可达（即使状态是 online）', () {
      final device = RemoteDesktopDevice.fromJson(
        deviceJson(heartbeatStale: true),
      );
      expect(device.status, DeviceStatus.online);
      expect(device.isReachable, isFalse);
    });
  });

  group('设备列表解析', () {
    test('items + total', () {
      final list = RemoteDesktopDeviceList.fromJson({
        'items': [deviceJson(), deviceJson(id: 'b', name: '笔记本')],
        'total': 2,
      });
      expect(list.items, hasLength(2));
      expect(list.total, 2);
    });

    test('缺少 total 时用条数兜底，非法元素被跳过', () {
      final list = RemoteDesktopDeviceList.fromJson({
        'items': [deviceJson(), 'not-a-map', 42],
      });
      expect(list.items, hasLength(1));
      expect(list.total, 1);
    });

    test('items 缺失时返回空列表', () {
      final list = RemoteDesktopDeviceList.fromJson(const {});
      expect(list.items, isEmpty);
      expect(list.total, 0);
    });
  });

  group('远控服务器参数', () {
    test('就绪时可使用，且只有公钥', () {
      final config = RemoteDesktopConfig.fromJson({
        'enabled': true,
        'id_server': 'rd.neilico.local',
        'relay_server': 'rd.neilico.local',
        'public_key': 'PUBKEY',
        'available': true,
        'hint': '',
        'ports': [21115, 21116, 21117],
      });
      expect(config.usable, isTrue);
      expect(config.publicKey, 'PUBKEY');
      expect(config.ports, hasLength(3));
    });

    test('未就绪时 usable=false（不伪造可用）', () {
      final config = RemoteDesktopConfig.fromJson({
        'enabled': true,
        'available': false,
        'hint': '服务器未就绪：未能读取公钥文件',
      });
      expect(config.usable, isFalse);
      expect(config.hint, contains('未就绪'));
    });
  });
}

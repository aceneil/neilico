import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/models/device.dart';
import 'package:neilico_desktop/models/device_policy.dart';
import 'package:neilico_desktop/models/device_switches.dart';
import 'package:neilico_desktop/models/remote_desktop_config.dart';
import 'package:neilico_desktop/theme/neilico_theme.dart';
import 'package:neilico_desktop/widgets/switch_panel.dart';

import '../support/fake_backend.dart';

Widget host(Widget child) => MaterialApp(
      theme: NeilicoTheme.light(),
      home: Scaffold(body: SingleChildScrollView(child: child)),
    );

RemoteDesktopConfig usableConfig() => RemoteDesktopConfig.fromJson({
      'enabled': true,
      'id_server': 'rd.neilico.local',
      'public_key': 'PUBKEY',
      'available': true,
      'hint': '服务器已就绪',
    });

void main() {
  testWidgets('后端未就绪：四个开关全部禁用，且写明原因（不静默禁用）', (tester) async {
    final device = RemoteDesktopDevice.fromJson(deviceJson());
    final panel = DeviceSwitchPanel.build(
      device: device,
      snapshot: DevicePolicySnapshot.pending,
      config: usableConfig(),
    );

    await tester.pumpWidget(host(SwitchPanel(panel: panel, onRemoteControlAllowed: (_) {})));

    final remote = tester.widget<SwitchListTile>(find.byKey(const Key('switch-remote-control')));
    final isolated = tester.widget<SwitchListTile>(find.byKey(const Key('switch-isolated-tunnel')));
    final mesh = tester.widget<SwitchListTile>(find.byKey(const Key('switch-mesh')));
    expect(remote.onChanged, isNull);
    expect(isolated.onChanged, isNull);
    expect(mesh.onChanged, isNull);

    // 隧道模式的分段控件也被禁用。
    final segmented = tester.widget<SegmentedButton<TunnelMode>>(find.byKey(const Key('switch-tunnel-mode')));
    expect(segmented.onSelectionChanged, isNull);

    expect(find.textContaining('device-policies'), findsWidgets, reason: '必须说明「后端接口未就绪」');
  });

  testWidgets('后端就绪：开关可操作并回传用户意图', (tester) async {
    final device = RemoteDesktopDevice.fromJson(deviceJson());
    final panel = DeviceSwitchPanel.build(
      device: device,
      snapshot: DevicePolicySnapshot.fromJson({
        'items': [policyJson(nodeId: device.id, meshJoined: true)],
      }),
      config: usableConfig(),
    );

    bool? remoteChange;
    bool? isolatedChange;
    bool? meshChange;
    TunnelMode? tunnelChange;

    await tester.pumpWidget(host(SwitchPanel(
      panel: panel,
      onRemoteControlAllowed: (value) => remoteChange = value,
      onIsolatedTunnel: (value) => isolatedChange = value,
      onMeshJoined: (value) => meshChange = value,
      onTunnelMode: (value) => tunnelChange = value,
    )));

    final remote = tester.widget<SwitchListTile>(find.byKey(const Key('switch-remote-control')));
    expect(remote.onChanged, isNotNull);
    expect(remote.value, isTrue);

    await tester.tap(find.byKey(const Key('switch-remote-control')));
    await tester.pump();
    expect(remoteChange, isFalse);

    await tester.tap(find.byKey(const Key('switch-isolated-tunnel')));
    await tester.pump();
    expect(isolatedChange, isTrue);

    await tester.tap(find.byKey(const Key('switch-mesh')));
    await tester.pump();
    expect(meshChange, isFalse);

    await tester.tap(find.text('中继'));
    await tester.pump();
    expect(tunnelChange, TunnelMode.relay);
  });

  testWidgets('设备离线：隧道与单独隧道禁用并给出离线原因', (tester) async {
    final device = RemoteDesktopDevice.fromJson(deviceJson(status: 'offline'));
    final panel = DeviceSwitchPanel.build(
      device: device,
      snapshot: DevicePolicySnapshot.fromJson({
        'items': [policyJson(nodeId: device.id)],
      }),
      config: usableConfig(),
    );

    await tester.pumpWidget(host(SwitchPanel(panel: panel)));

    final isolated = tester.widget<SwitchListTile>(find.byKey(const Key('switch-isolated-tunnel')));
    expect(isolated.onChanged, isNull);
    expect(find.textContaining('设备离线或心跳过期'), findsWidgets);
  });
}

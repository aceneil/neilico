import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/screens/device_detail_screen.dart';
import 'package:neilico_desktop/screens/devices_screen.dart';
import 'package:neilico_desktop/theme/neilico_theme.dart';

import '../support/fake_backend.dart';
import '../support/harness.dart';

const String deviceId = '11111111-1111-1111-1111-111111111111';

Widget host(Widget child) => MaterialApp(theme: NeilicoTheme.light(), home: child);

void setDesktopSurface(WidgetTester tester) {
  tester.view.physicalSize = const Size(1440, 900);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('设备网格：渲染设备卡片且无溢出异常', (tester) async {
    setDesktopSurface(tester);
    final backend = FakeBackend(
      devices: [
        deviceJson(),
        deviceJson(id: 'b', name: '备用笔记本', status: 'offline'),
        deviceJson(id: 'c', name: '机架服务器', heartbeatStale: true),
      ],
      policies: [policyJson(nodeId: deviceId)],
    );
    final controller = await readyController(backend);

    await tester.pumpWidget(host(DevicesScreen(controller: controller)));
    await tester.pump();

    expect(find.text('办公室主机'), findsOneWidget);
    expect(find.text('备用笔记本'), findsOneWidget);
    expect(find.text('机架服务器'), findsOneWidget);
    expect(tester.takeException(), isNull, reason: '网格固定高度下不允许 RenderFlex 溢出');
  });

  testWidgets('设备网格：后端授权接口未就绪时，卡片不显示假状态', (tester) async {
    setDesktopSurface(tester);
    final backend = FakeBackend(
      devices: [deviceJson()],
      policiesStatus: 404,
    );
    final controller = await readyController(backend);

    await tester.pumpWidget(host(DevicesScreen(controller: controller)));
    await tester.pump();

    expect(controller.policies.isAvailable, isFalse);
    expect(find.textContaining('待后端就绪'), findsOneWidget);
    expect(find.textContaining('接口'), findsWidgets);
  });

  testWidgets('设备详情：授权接口未就绪 => 连接按钮禁用且说明原因', (tester) async {
    setDesktopSurface(tester);
    final backend = FakeBackend(devices: [deviceJson()], policiesStatus: 404);
    final controller = await readyController(backend);

    await tester.pumpWidget(host(DeviceDetailScreen(controller: controller, nodeId: deviceId)));
    await tester.pump();

    final button = tester.widget<FilledButton>(find.byKey(const Key('connect-button')));
    expect(button.onPressed, isNull);
    expect(find.textContaining('device-policies'), findsWidgets);
  });

  testWidgets('设备详情：后端就绪 => 连接按钮可用，点击调起 rustdesk://', (tester) async {
    setDesktopSurface(tester);
    final backend = FakeBackend(
      devices: [deviceJson()],
      policies: [policyJson(nodeId: deviceId)],
    );
    final launcher = FakeLauncher();
    final controller = await readyController(backend, launcher: launcher);

    await tester.pumpWidget(host(DeviceDetailScreen(controller: controller, nodeId: deviceId)));
    await tester.pump();

    final button = tester.widget<FilledButton>(find.byKey(const Key('connect-button')));
    expect(button.onPressed, isNotNull);

    await tester.tap(find.byKey(const Key('connect-button')));
    await tester.pump();
    expect(launcher.launched, ['rustdesk://123456789']);
  });

  testWidgets('设备详情：可被远程关闭 => 连接按钮禁用', (tester) async {
    setDesktopSurface(tester);
    final backend = FakeBackend(
      devices: [deviceJson()],
      policies: [policyJson(nodeId: deviceId, remoteControlAllowed: false)],
    );
    final controller = await readyController(backend);

    await tester.pumpWidget(host(DeviceDetailScreen(controller: controller, nodeId: deviceId)));
    await tester.pump();

    final button = tester.widget<FilledButton>(find.byKey(const Key('connect-button')));
    expect(button.onPressed, isNull);
    expect(find.textContaining('未开启「可被远程」'), findsWidgets);
  });
}

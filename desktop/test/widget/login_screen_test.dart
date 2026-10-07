import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/app.dart';
import 'package:neilico_desktop/screens/login_screen.dart';
import 'package:neilico_desktop/services/app_controller.dart';
import 'package:neilico_desktop/theme/neilico_theme.dart';

import '../support/fake_backend.dart';
import '../support/harness.dart';

Widget wrap(Widget child) => MaterialApp(theme: NeilicoTheme.light(), home: child);

void main() {
  testWidgets('空表单提交：显示字段级校验错误，不发请求', (tester) async {
    final backend = FakeBackend();
    final controller = await loggedOutController(backend);

    await tester.pumpWidget(wrap(LoginScreen(controller: controller)));
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pump();

    expect(find.text('请输入邮箱'), findsOneWidget);
    expect(find.text('请输入密码'), findsOneWidget);
    expect(backend.requests, isEmpty, reason: '校验不通过时不应发起网络请求');
    expect(controller.phase, AppPhase.loggedOut);
  });

  testWidgets('非法邮箱：提示格式错误', (tester) async {
    final controller = await loggedOutController(FakeBackend());
    await tester.pumpWidget(wrap(LoginScreen(controller: controller)));

    await tester.enterText(find.byKey(const Key('login-email')), 'not-an-email');
    await tester.enterText(find.byKey(const Key('login-password')), 'secret123');
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pump();

    expect(find.text('邮箱格式不正确'), findsOneWidget);
    expect(controller.phase, AppPhase.loggedOut);
  });

  testWidgets('后端 401：显示「邮箱或密码不正确」', (tester) async {
    final backend = FakeBackend(loginStatus: 401);
    final controller = await loggedOutController(backend);
    await tester.pumpWidget(wrap(LoginScreen(controller: controller)));

    await tester.enterText(find.byKey(const Key('login-email')), 'ops@neilico.local');
    await tester.enterText(find.byKey(const Key('login-password')), 'wrong-password');
    await tester.tap(find.byKey(const Key('login-submit')));
    await settle(tester.pump);

    expect(find.text('邮箱或密码不正确'), findsOneWidget);
    expect(controller.isAuthenticated, isFalse);
    expect(controller.phase, AppPhase.loggedOut);
  });

  testWidgets('登录成功：进入设备网格并展示设备', (tester) async {
    tester.view.physicalSize = const Size(1400, 900);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final backend = FakeBackend(devices: [deviceJson(name: '办公室主机')]);
    final controller = await loggedOutController(backend);
    expect(controller.phase, AppPhase.loggedOut);

    await tester.pumpWidget(NeilicoApp(controller: controller));
    await tester.pump();
    expect(find.byKey(const Key('login-submit')), findsOneWidget);

    await tester.enterText(find.byKey(const Key('login-email')), 'ops@neilico.local');
    await tester.enterText(find.byKey(const Key('login-password')), 'secret123');
    await tester.tap(find.byKey(const Key('login-submit')));
    await settle(tester.pump);
    await tester.pump();

    expect(controller.phase, AppPhase.ready);
    expect(controller.devices, hasLength(1));
    expect(find.text('办公室主机'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

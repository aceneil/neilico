import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'api/api_client.dart';
import 'app.dart';
import 'services/app_controller.dart';
import 'services/app_settings.dart';
import 'services/secure_token_store.dart';
import 'services/session_store.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  await windowManager.ensureInitialized();
  const windowOptions = WindowOptions(
    size: Size(1280, 840),
    minimumSize: Size(1024, 680),
    center: true,
    title: 'NEILICO',
    titleBarStyle: TitleBarStyle.normal,
  );
  await windowManager.waitUntilReadyToShow(windowOptions, () async {
    await windowManager.show();
    await windowManager.focus();
  });

  final controller = AppController(
    apiClient: ApiClient(baseUrl: AppSettings.defaultApiBase),
    // 生产实现：令牌进操作系统密钥库（libsecret / Keychain / DPAPI）。
    sessionStore: SessionStore(SecureTokenStore()),
  );
  await controller.bootstrap();

  runApp(NeilicoApp(controller: controller));
}

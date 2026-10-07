import 'package:neilico_desktop/api/api_client.dart';
import 'package:neilico_desktop/models/auth.dart';
import 'package:neilico_desktop/services/app_controller.dart';
import 'package:neilico_desktop/services/connect_launcher.dart';
import 'package:neilico_desktop/services/kernel/kernel_locator.dart';
import 'package:neilico_desktop/services/secure_token_store.dart';
import 'package:neilico_desktop/services/session_store.dart';
import 'package:neilico_desktop/services/token_store.dart';

import 'fake_backend.dart';

/// 假的连接调起器：记录被请求的 URL，不真的启动进程。
class FakeLauncher implements ConnectLauncher {
  final List<String> launched = <String>[];
  bool ok = true;

  @override
  Future<LaunchResult> launch(String url) async {
    launched.add(url);
    return LaunchResult(ok: ok, message: ok ? '已请求系统打开 $url' : '调起失败');
  }
}

AuthSession testSession({String token = 'access-token-1'}) => AuthSession(
      token: token,
      refreshToken: 'refresh-token-1',
      user: const AuthUser(
        id: 'u-1',
        email: 'ops@neilico.local',
        role: 'platform_admin',
        tenantId: 't-1',
      ),
    );

/// 构造一个已登录、已拉取完数据的控制器（数据来自 [FakeBackend]）。
Future<AppController> readyController(
  FakeBackend backend, {
  TokenStore? tokenStore,
  ConnectLauncher? launcher,
  String apiBase = 'http://test.local',
}) async {
  final store = SessionStore(tokenStore ?? MemoryTokenStore());
  await store.save(PersistedSession(apiBase: apiBase, session: testSession()));
  final controller = AppController(
    apiClient: ApiClient(baseUrl: apiBase, httpClient: backend.client()),
    sessionStore: store,
    launcher: launcher ?? FakeLauncher(),
    // 测试里不碰真实文件系统。
    kernelLocator: KernelLocator(exists: (_) => false),
  );
  await controller.bootstrap();
  return controller;
}

/// 已登录但尚未拉取数据的控制器（用于登录流程测试）。
///
/// 会先跑一次 [AppController.bootstrap]（存储为空 => 落到 loggedOut）。
Future<AppController> loggedOutController(FakeBackend backend, {TokenStore? tokenStore}) async {
  final controller = AppController(
    apiClient: ApiClient(baseUrl: 'http://test.local', httpClient: backend.client()),
    sessionStore: SessionStore(tokenStore ?? MemoryTokenStore()),
    launcher: FakeLauncher(),
    kernelLocator: KernelLocator(exists: (_) => false),
  );
  await controller.bootstrap();
  return controller;
}

/// 确定性地推进若干帧，避免 pumpAndSettle 被无限动画（加载圈）拖死。
Future<void> settle(Future<void> Function(Duration) pump) async {
  for (var index = 0; index < 12; index++) {
    await pump(const Duration(milliseconds: 20));
  }
}

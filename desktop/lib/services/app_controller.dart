import 'package:flutter/foundation.dart';

import '../api/api_client.dart';
import '../api/auth_api.dart';
import '../api/remote_desktop_api.dart';
import '../models/auth.dart';
import '../models/device.dart';
import '../models/device_policy.dart';
import '../models/device_switches.dart';
import '../models/remote_desktop_config.dart';
import 'connect_launcher.dart';
import 'kernel/kernel_locator.dart';
import 'secure_token_store.dart';
import 'session_store.dart';

/// 应用的三个顶层阶段。
enum AppPhase { booting, loggedOut, ready }

/// 一次「写操作」的结果：UI 据此弹提示，不做乐观欺骗。
class ActionResult {
  const ActionResult({required this.ok, required this.message});

  final bool ok;
  final String message;
}

/// 全局状态容器（轻量：仅 ChangeNotifier，无第三方状态管理框架）。
///
/// 职责边界：把「后端是权威」这件事落到实处 —— 所有开关状态都来自
/// [policies]，本类从不凭空造一个默认值。
class AppController extends ChangeNotifier {
  AppController({
    required ApiClient apiClient,
    AuthApi? authApi,
    RemoteDesktopApi? remoteApi,
    SessionStore? sessionStore,
    ConnectLauncher? launcher,
    KernelLocator? kernelLocator,
  })  : _api = apiClient,
        _auth = authApi ?? AuthApi(apiClient),
        _remote = remoteApi ?? RemoteDesktopApi(apiClient),
        _sessionStore = sessionStore ?? SessionStore(MemoryTokenStore()),
        _launcher = launcher ?? ProcessConnectLauncher(),
        _kernelLocator = kernelLocator ?? KernelLocator();

  final ApiClient _api;
  final AuthApi _auth;
  final RemoteDesktopApi _remote;
  final SessionStore _sessionStore;
  final ConnectLauncher _launcher;
  final KernelLocator _kernelLocator;

  AppPhase _phase = AppPhase.booting;
  AuthSession? _session;
  RemoteDesktopConfig _config = RemoteDesktopConfig.unknown;
  List<RemoteDesktopDevice> _devices = const <RemoteDesktopDevice>[];
  DevicePolicySnapshot _policies = DevicePolicySnapshot.pending;
  KernelLocation _kernel = const KernelLocation.missing('尚未探测内核进程', <String>[]);
  bool _busy = false;
  String? _error;

  AppPhase get phase => _phase;

  AuthSession? get session => _session;

  RemoteDesktopConfig get config => _config;

  List<RemoteDesktopDevice> get devices => _devices;

  DevicePolicySnapshot get policies => _policies;

  KernelLocation get kernel => _kernel;

  bool get busy => _busy;

  String? get error => _error;

  String get apiBase => _api.baseUrl;

  bool get isAuthenticated => _session != null;

  /// 启动时恢复会话（令牌来自操作系统密钥库，非明文文件）。
  Future<void> bootstrap() async {
    _kernel = _kernelLocator.locate();
    final persisted = await _sessionStore.load();
    if (persisted == null) {
      _phase = AppPhase.loggedOut;
      notifyListeners();
      return;
    }
    _api.setBaseUrl(persisted.apiBase.isEmpty ? _api.baseUrl : persisted.apiBase);
    _api.setToken(persisted.session.token);
    _session = persisted.session;
    _phase = AppPhase.ready;
    notifyListeners();
    await reload();
  }

  /// 登录。成功返回 null，失败返回可展示的错误文案。
  Future<String?> login({
    required String email,
    required String password,
    String? apiBase,
  }) async {
    _busy = true;
    _error = null;
    notifyListeners();
    try {
      if (apiBase != null && apiBase.trim().isNotEmpty) {
        _api.setBaseUrl(apiBase);
      }
      final session = await _auth.login(email: email, password: password);
      _api.setToken(session.token);
      _session = session;
      _phase = AppPhase.ready;
      await _sessionStore.save(
        PersistedSession(apiBase: _api.baseUrl, session: session),
      );
      notifyListeners();
      await reload();
      return null;
    } on ApiException catch (error) {
      final message = error.isUnauthorized ? '邮箱或密码不正确' : error.message;
      _error = message;
      return message;
    } catch (error) {
      _error = '登录失败：$error';
      return _error;
    } finally {
      _busy = false;
      notifyListeners();
    }
  }

  Future<void> logout() async {
    await _sessionStore.clear();
    _api.setToken(null);
    _session = null;
    _devices = const <RemoteDesktopDevice>[];
    _policies = DevicePolicySnapshot.pending;
    _config = RemoteDesktopConfig.unknown;
    _error = null;
    _phase = AppPhase.loggedOut;
    notifyListeners();
  }

  /// 并行刷新：服务器参数 + 设备列表 + 设备授权状态。
  /// 单个接口失败不影响其它接口（授权接口未就绪是预期情况）。
  Future<void> reload() async {
    if (_session == null) return;
    _busy = true;
    _error = null;
    notifyListeners();
    try {
      await Future.wait([_loadConfig(), _loadDevices(), _loadPolicies()]);
    } finally {
      _busy = false;
      notifyListeners();
    }
  }

  Future<void> _loadConfig() async {
    try {
      _config = await _remote.config();
    } on ApiException catch (error) {
      if (await _handleUnauthorized(error)) return;
      _config = RemoteDesktopConfig.unknown;
      _error = error.message;
    } catch (error) {
      _error = '获取服务器参数失败：$error';
    }
  }

  Future<void> _loadDevices() async {
    try {
      final list = await _remote.devices();
      _devices = list.items;
    } on ApiException catch (error) {
      if (await _handleUnauthorized(error)) return;
      _devices = const <RemoteDesktopDevice>[];
      _error = error.message;
    } catch (error) {
      _error = '获取设备列表失败：$error';
    }
  }

  Future<void> _loadPolicies() async {
    try {
      _policies = await _remote.devicePolicies();
    } on ApiException catch (error) {
      if (await _handleUnauthorized(error)) return;
      if (error.isNotImplemented) {
        // 后端尚未提供：如实置灰，不返回本地默认值。
        _policies = DevicePolicySnapshot.pending;
      } else if (error.isForbidden) {
        _policies = DevicePolicySnapshot.unavailable('当前账号无权查看设备授权状态');
      } else {
        _policies = DevicePolicySnapshot.unavailable(error.message);
      }
    } catch (error) {
      _policies = DevicePolicySnapshot.unavailable('获取设备授权状态失败：$error');
    }
  }

  /// 令牌失效的统一切换：清会话并回到登录页。
  Future<bool> _handleUnauthorized(ApiException error) async {
    if (!error.isUnauthorized) return false;
    await logout();
    _error = '登录状态已失效，请重新登录';
    notifyListeners();
    return true;
  }

  /// 设备开关面板（纯逻辑在 [DeviceSwitchPanel.build]，这里只做装配）。
  DeviceSwitchPanel panelFor(RemoteDesktopDevice device) => DeviceSwitchPanel.build(
        device: device,
        snapshot: _policies,
        config: _config,
      );

  Future<ActionResult> setRemoteControlAllowed(String nodeId, bool value) =>
      _patchPolicy(
        nodeId,
        (policy) => policy.toPatchJson(remoteControlAllowed: value),
        (policy) => policy.copyWith(remoteControlAllowed: value),
      );

  Future<ActionResult> setTunnelMode(String nodeId, TunnelMode value) =>
      _patchPolicy(
        nodeId,
        (policy) => policy.toPatchJson(tunnelMode: value),
        (policy) => policy.copyWith(tunnelMode: value),
      );

  Future<ActionResult> setIsolatedTunnel(String nodeId, bool value) =>
      _patchPolicy(
        nodeId,
        (policy) => policy.toPatchJson(isolatedTunnelEnabled: value),
        (policy) => policy.copyWith(
          isolatedTunnel: IsolatedTunnel(
            enabled: value,
            streamRuleId: policy.isolatedTunnel.streamRuleId,
          ),
        ),
      );

  Future<ActionResult> setMeshJoined(String nodeId, bool value) =>
      _patchPolicy(
        nodeId,
        (policy) => policy.toPatchJson(meshJoined: value),
        (policy) => policy.copyWith(
          mesh: MeshMembership(
            joined: value,
            networkId: policy.mesh.networkId,
            virtualIp: policy.mesh.virtualIp,
          ),
        ),
      );

  Future<ActionResult> _patchPolicy(
    String nodeId,
    Map<String, dynamic> Function(DevicePolicy policy) buildPatch,
    DevicePolicy Function(DevicePolicy policy) onSuccess,
  ) async {
    final current = _policies.forNode(nodeId);
    if (current == null) {
      return const ActionResult(ok: false, message: '后端未返回该设备的授权状态，暂不可修改');
    }
    try {
      final updated = await _remote.updatePolicy(nodeId, buildPatch(current));
      _policies = _policies.upsert(updated.nodeId.isEmpty ? onSuccess(current) : updated);
      notifyListeners();
      return const ActionResult(ok: true, message: '已提交，状态以控制面为准');
    } on ApiException catch (error) {
      if (error.isNotImplemented) {
        return ActionResult(ok: false, message: '后端接口未就绪，暂不可修改：${error.message}');
      }
      if (error.isForbidden) {
        return const ActionResult(ok: false, message: '当前账号无权修改该设备授权');
      }
      return ActionResult(ok: false, message: error.message);
    } catch (error) {
      return ActionResult(ok: false, message: '修改失败：$error');
    }
  }

  /// 发起远程连接：走门禁（策略 / ID / 服务器），再交给系统调起。
  Future<ActionResult> connect(RemoteDesktopDevice device) async {
    final panel = panelFor(device);
    if (!panel.connect.enabled) {
      return ActionResult(ok: false, message: panel.connect.message);
    }
    final url = panel.connect.value!;
    final result = await _launcher.launch(url);
    return ActionResult(ok: result.ok, message: result.message);
  }

  /// 复制给内核用的连接参数（只含公钥，绝不含私钥）。
  String connectionParamsFor(RemoteDesktopDevice device) => device.connectionParams;
}

import '../models/auth.dart';
import 'api_client.dart';

/// 认证接口（控制面 `/api/v1/auth/*`）。
class AuthApi {
  AuthApi(this._client);

  final ApiClient _client;

  /// POST /api/v1/auth/login —— 成功返回令牌与用户；失败抛 [ApiException]。
  Future<AuthSession> login({required String email, required String password}) async {
    final json = await _client.postJson(
      '/api/v1/auth/login',
      body: {'email': email.trim(), 'password': password},
      authenticated: false,
    );
    return AuthSession.fromJson(json);
  }

  /// POST /api/v1/auth/refresh —— 用刷新令牌换一组新令牌。
  Future<AuthSession> refresh(String refreshToken) async {
    final json = await _client.postJson(
      '/api/v1/auth/refresh',
      body: {'refresh_token': refreshToken},
      authenticated: false,
    );
    return AuthSession.fromJson(json);
  }
}

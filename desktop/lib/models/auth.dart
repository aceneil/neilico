import '../util/json.dart';

/// 控制面用户（GET /api/v1/auth/login 的 `user` 字段）。
class AuthUser {
  const AuthUser({
    required this.id,
    required this.email,
    required this.role,
    required this.tenantId,
  });

  final String id;
  final String email;
  final String role;
  final String tenantId;

  bool get isPlatformAdmin => role == 'platform_admin';

  factory AuthUser.fromJson(Map<String, dynamic> json) => AuthUser(
        id: asString(json['id']),
        email: asString(json['email']),
        role: asString(json['role'], fallback: 'readonly'),
        tenantId: asString(json['tenant_id']),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'email': email,
        'role': role,
        'tenant_id': tenantId,
      };
}

/// 一次登录会话：访问令牌 + 刷新令牌 + 用户。
///
/// 安全约定：该对象只存在于内存与安全存储中；**绝不**写入日志或明文文件。
class AuthSession {
  const AuthSession({
    required this.token,
    required this.refreshToken,
    required this.user,
  });

  final String token;
  final String refreshToken;
  final AuthUser user;

  factory AuthSession.fromJson(Map<String, dynamic> json) => AuthSession(
        token: asString(json['token']),
        refreshToken: asString(json['refresh_token']),
        user: AuthUser.fromJson(asMap(json['user'])),
      );

  AuthSession copyWith({String? token, String? refreshToken}) => AuthSession(
        token: token ?? this.token,
        refreshToken: refreshToken ?? this.refreshToken,
        user: user,
      );
}

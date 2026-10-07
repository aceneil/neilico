/// 客户端运行时配置。
class AppSettings {
  const AppSettings._();

  /// 控制面默认地址。构建时可用
  /// `--dart-define=NEILICO_API_BASE=https://neilico.example.com` 覆盖；
  /// 登录页也允许临时修改（会随会话一起持久化）。
  static const String defaultApiBase = String.fromEnvironment(
    'NEILICO_API_BASE',
    defaultValue: 'http://127.0.0.1:8080',
  );
}

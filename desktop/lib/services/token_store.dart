/// 令牌 / 会话的持久化抽象。
///
/// 实现约定：**绝不明文落盘**。生产实现走操作系统密钥库
/// （macOS Keychain / Windows DPAPI / Linux libsecret），见 [SecureTokenStore]。
abstract class TokenStore {
  Future<String?> read();

  Future<void> write(String value);

  Future<void> clear();
}

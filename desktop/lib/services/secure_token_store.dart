import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'token_store.dart';

/// 基于操作系统密钥库的令牌存储。
///
/// - macOS：Keychain
/// - Windows：DPAPI（CryptProtectData）
/// - Linux：libsecret（Secret Service / gnome-keyring）
///
/// 令牌**从不**以明文写入磁盘文件。
class SecureTokenStore implements TokenStore {
  SecureTokenStore({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  static const String storageKey = 'neilico.desktop.session';

  final FlutterSecureStorage _storage;

  @override
  Future<String?> read() => _storage.read(key: storageKey);

  @override
  Future<void> write(String value) => _storage.write(key: storageKey, value: value);

  @override
  Future<void> clear() => _storage.delete(key: storageKey);
}

/// 纯内存实现：用于单元测试，也作为「不愿落盘」场景下的安全兜底
/// （进程退出即丢失，绝不会留下明文文件）。
class MemoryTokenStore implements TokenStore {
  String? _value;

  @override
  Future<String?> read() async => _value;

  @override
  Future<void> write(String value) async => _value = value;

  @override
  Future<void> clear() async => _value = null;
}

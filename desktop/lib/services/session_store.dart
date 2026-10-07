import 'dart:convert';

import '../models/auth.dart';
import '../util/json.dart';
import 'token_store.dart';

/// 一次登录要持久化的内容：会话 + 控制面地址。
class PersistedSession {
  const PersistedSession({required this.apiBase, required this.session});

  final String apiBase;
  final AuthSession session;
}

/// 会话序列化：整体作为**一个**不透明字符串交给 [TokenStore]，
/// 由密钥库统一加密，不额外产生明文文件。
class SessionStore {
  SessionStore(this._store);

  final TokenStore _store;

  Future<PersistedSession?> load() async {
    final raw = await _store.read();
    if (raw == null || raw.trim().isEmpty) return null;
    try {
      final decoded = asMap(jsonDecode(raw));
      final sessionJson = asMap(decoded['session']);
      final token = asString(sessionJson['token']);
      if (token.isEmpty) return null;
      return PersistedSession(
        apiBase: asString(decoded['api_base']),
        session: AuthSession.fromJson(sessionJson),
      );
    } catch (_) {
      // 内容损坏（例如换了密钥库后端）：直接丢弃，让用户重新登录。
      await _store.clear();
      return null;
    }
  }

  Future<void> save(PersistedSession value) async {
    final payload = jsonEncode({
      'api_base': value.apiBase,
      'session': {
        'token': value.session.token,
        'refresh_token': value.session.refreshToken,
        'user': value.session.user.toJson(),
      },
    });
    await _store.write(payload);
  }

  Future<void> clear() => _store.clear();
}

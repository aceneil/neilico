import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/services/secure_token_store.dart';
import 'package:neilico_desktop/services/session_store.dart';

import 'support/harness.dart';

void main() {
  test('会话往返保存与读取', () async {
    final store = SessionStore(MemoryTokenStore());
    await store.save(PersistedSession(apiBase: 'https://neilico.example.com', session: testSession()));

    final loaded = await store.load();
    expect(loaded, isNotNull);
    expect(loaded!.apiBase, 'https://neilico.example.com');
    expect(loaded.session.token, 'access-token-1');
    expect(loaded.session.refreshToken, 'refresh-token-1');
    expect(loaded.session.user.email, 'ops@neilico.local');
    expect(loaded.session.user.isPlatformAdmin, isTrue);
  });

  test('空存储 => 未登录', () async {
    expect(await SessionStore(MemoryTokenStore()).load(), isNull);
  });

  test('内容损坏时丢弃并清空（不抛异常）', () async {
    final backing = MemoryTokenStore();
    await backing.write('这不是 JSON');
    final store = SessionStore(backing);

    expect(await store.load(), isNull);
    expect(await backing.read(), isNull, reason: '坏数据必须被清掉，避免每次启动都失败');
  });

  test('缺少 token 视为未登录', () async {
    final backing = MemoryTokenStore();
    await backing.write('{"api_base":"http://a","session":{"refresh_token":"r"}}');
    expect(await SessionStore(backing).load(), isNull);
  });

  test('clear 后读不到', () async {
    final backing = MemoryTokenStore();
    final store = SessionStore(backing);
    await store.save(PersistedSession(apiBase: 'http://a', session: testSession()));
    await store.clear();
    expect(await store.load(), isNull);
  });
}

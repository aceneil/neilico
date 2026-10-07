import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:neilico_desktop/api/api_client.dart';

void main() {
  test('base 末尾斜杠被规范化，路径拼接正确且带上 Bearer 令牌', () async {
    late http.Request captured;
    final client = ApiClient(
      baseUrl: 'http://control.local///',
      httpClient: MockClient((request) async {
        captured = request;
        return http.Response('{"ok":true}', 200);
      }),
    );
    client.setToken('tok-123');

    final json = await client.getJson('/api/v1/remote-desktop/devices');

    expect(json['ok'], isTrue);
    expect(captured.url.toString(), 'http://control.local/api/v1/remote-desktop/devices');
    expect(captured.headers['authorization'], 'Bearer tok-123');
  });

  test('未设置令牌时不带 Authorization 头', () async {
    late http.Request captured;
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((request) async {
        captured = request;
        return http.Response('{}', 200);
      }),
    );

    await client.getJson('/api/v1/remote-desktop/config');
    expect(captured.headers.containsKey('authorization'), isFalse);
  });

  test('401 映射为 isUnauthorized', () async {
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((_) async => http.Response('{"message":"invalid email or password"}', 401)),
    );
    await expectLater(
      client.postJson('/api/v1/auth/login', body: const {'email': 'a', 'password': 'b'}),
      throwsA(isA<ApiException>()
          .having((error) => error.isUnauthorized, 'isUnauthorized', isTrue)
          .having((error) => error.message, 'message', contains('invalid email'))),
    );
  });

  test('404 映射为 isNotImplemented（授权接口未就绪的判定依据）', () async {
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((_) async => http.Response('{"error":"not_found"}', 404)),
    );
    await expectLater(
      client.getJson('/api/v1/remote-desktop/device-policies'),
      throwsA(isA<ApiException>().having((error) => error.isNotImplemented, 'isNotImplemented', isTrue)),
    );
  });

  test('非 JSON 响应抛出明确异常（带状态码）', () async {
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((_) async => http.Response('<html>gateway</html>', 502)),
    );
    await expectLater(
      client.getJson('/api/v1/remote-desktop/devices'),
      throwsA(isA<ApiException>().having((error) => error.statusCode, 'statusCode', 502)),
    );
  });

  test('网络异常被包装成可展示文案', () async {
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((_) async => throw const SocketExceptionStub()),
    );
    await expectLater(
      client.getJson('/api/v1/health'),
      throwsA(isA<ApiException>().having((error) => error.message, 'message', contains('无法连接控制面'))),
    );
  });

  test('PATCH 发送 JSON 请求体', () async {
    late http.Request captured;
    final client = ApiClient(
      baseUrl: 'http://control.local',
      httpClient: MockClient((request) async {
        captured = request;
        return http.Response('{"node_id":"n-1"}', 200);
      }),
    );
    client.setToken('t');

    await client.patchJson('/api/v1/remote-desktop/device-policies/n-1', body: const {'mesh_joined': true});

    expect(captured.method, 'PATCH');
    expect(captured.body, '{"mesh_joined":true}');
    expect(captured.headers['content-type'], contains('application/json'));
  });
}

/// 用于模拟传输层异常。
class SocketExceptionStub implements Exception {
  const SocketExceptionStub();
}

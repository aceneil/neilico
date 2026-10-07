import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

/// 控制面调用统一的异常类型。
class ApiException implements Exception {
  ApiException(this.message, {this.statusCode, this.code});

  final String message;
  final int? statusCode;
  final String? code;

  /// 401：令牌缺失/过期/被吊销。
  bool get isUnauthorized => statusCode == 401;

  /// 404 / 405 / 501：接口尚未实现。上层据此把 UI 置灰并说明原因，
  /// 而不是当成「空数据」。
  bool get isNotImplemented =>
      statusCode == 404 || statusCode == 405 || statusCode == 501;

  bool get isForbidden => statusCode == 403;

  @override
  String toString() => 'ApiException($statusCode/$code): $message';
}

/// 极简 REST 客户端：只做「拼接 base、带令牌、解 JSON、映射错误」四件事。
///
/// 令牌只保存在内存（[_token]），由持有者负责写入安全存储。
class ApiClient {
  ApiClient({required String baseUrl, http.Client? httpClient})
      : _baseUrl = _normalizeBase(baseUrl),
        _http = httpClient ?? http.Client();

  static const Duration timeout = Duration(seconds: 20);

  final http.Client _http;
  String _baseUrl;
  String? _token;

  String get baseUrl => _baseUrl;

  String? get token => _token;

  void setBaseUrl(String value) {
    _baseUrl = _normalizeBase(value);
  }

  void setToken(String? value) {
    _token = (value == null || value.trim().isEmpty) ? null : value;
  }

  void close() => _http.close();

  static String _normalizeBase(String value) {
    var text = value.trim();
    while (text.endsWith('/')) {
      text = text.substring(0, text.length - 1);
    }
    return text;
  }

  Uri _uri(String path) {
    final suffix = path.startsWith('/') ? path : '/$path';
    return Uri.parse('$_baseUrl$suffix');
  }

  Future<Map<String, dynamic>> getJson(String path) => _send('GET', path);

  Future<Map<String, dynamic>> postJson(
    String path, {
    Object? body,
    bool authenticated = true,
  }) =>
      _send('POST', path, body: body, authenticated: authenticated);

  Future<Map<String, dynamic>> patchJson(
    String path, {
    Object? body,
  }) =>
      _send('PATCH', path, body: body);

  Future<Map<String, dynamic>> _send(
    String method,
    String path, {
    Object? body,
    bool authenticated = true,
  }) async {
    final request = http.Request(method, _uri(path))
      ..headers['accept'] = 'application/json';
    if (body != null) {
      request.headers['content-type'] = 'application/json';
      request.body = jsonEncode(body);
    }
    if (authenticated && _token != null) {
      request.headers['authorization'] = 'Bearer $_token';
    }

    final http.StreamedResponse response;
    try {
      response = await _http.send(request).timeout(timeout);
    } on TimeoutException {
      throw ApiException('请求超时（${timeout.inSeconds}s）：请检查控制面地址与网络');
    } catch (error) {
      throw ApiException('无法连接控制面：$error');
    }

    final text = await response.stream.bytesToString();
    final decoded = _decode(text, response.statusCode).value;

    if (response.statusCode >= 200 && response.statusCode < 300) {
      return decoded;
    }
    throw ApiException(
      _errorMessage(decoded, response.statusCode),
      statusCode: response.statusCode,
      code: _errorCode(decoded),
    );
  }

  _Decoded _decode(String text, int statusCode) {
    final trimmed = text.trim();
    if (trimmed.isEmpty) return const _Decoded(<String, dynamic>{});
    try {
      final value = jsonDecode(trimmed);
      if (value is Map<String, dynamic>) return _Decoded(value);
      if (value is Map) return _Decoded(value.cast<String, dynamic>());
      // 少数接口直接返回数组：包一层，调用方按需取 data。
      return _Decoded(<String, dynamic>{'data': value});
    } catch (_) {
      throw ApiException('响应不是合法 JSON（HTTP $statusCode）', statusCode: statusCode);
    }
  }

  String? _errorCode(Map<String, dynamic> body) {
    for (final key in const ['code', 'error']) {
      final value = body[key];
      if (value is String && value.trim().isNotEmpty) return value.trim();
    }
    return null;
  }

  String _errorMessage(Map<String, dynamic> body, int statusCode) {
    for (final key in const ['message', 'detail', 'error']) {
      final value = body[key];
      if (value is String && value.trim().isNotEmpty) return value.trim();
    }
    if (statusCode == 401) return '登录状态已失效，请重新登录';
    if (statusCode == 403) return '当前账号没有执行该操作的权限';
    if (statusCode == 404) return '接口不存在（后端尚未提供）';
    return '控制面返回 HTTP $statusCode';
  }
}

class _Decoded {
  const _Decoded(this.value);

  final Map<String, dynamic> value;
}

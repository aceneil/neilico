/// 与后端 JSON 交互时的容错取值工具。
///
/// 控制面的字段可能缺失或为 null（旧版本、部分租户、离线节点），
/// 解析层必须容忍，绝不因为一个 null 让整个设备网格崩掉。
library;

String asString(Object? value, {String fallback = ''}) {
  if (value == null) return fallback;
  if (value is String) return value;
  return value.toString();
}

String? asNullableString(Object? value) {
  if (value == null) return null;
  if (value is String) {
    final trimmed = value.trim();
    return trimmed.isEmpty ? null : trimmed;
  }
  return value.toString();
}

bool asBool(Object? value, {bool fallback = false}) {
  if (value is bool) return value;
  if (value is num) return value != 0;
  if (value is String) {
    final normalized = value.trim().toLowerCase();
    if (normalized == 'true' || normalized == '1') return true;
    if (normalized == 'false' || normalized == '0') return false;
  }
  return fallback;
}

int asInt(Object? value, {int fallback = 0}) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value.trim()) ?? fallback;
  return fallback;
}

/// 解析 ISO-8601 时间戳（后端统一返回 UTC）。无法解析时返回 null。
DateTime? asDateTime(Object? value) {
  if (value == null) return null;
  if (value is DateTime) return value;
  final text = value.toString().trim();
  if (text.isEmpty) return null;
  return DateTime.tryParse(text)?.toUtc();
}

Map<String, dynamic> asMap(Object? value) {
  if (value is Map<String, dynamic>) return value;
  if (value is Map) return value.cast<String, dynamic>();
  return const <String, dynamic>{};
}

List<dynamic> asList(Object? value) {
  if (value is List) return value;
  return const <dynamic>[];
}

/// 把数组字段解析成模型列表，跳过非法元素。
List<T> parseList<T>(Object? value, T Function(Map<String, dynamic>) fromJson) {
  final result = <T>[];
  for (final item in asList(value)) {
    if (item is Map) {
      result.add(fromJson(item.cast<String, dynamic>()));
    }
  }
  return result;
}

/// 时间与文本的展示格式化。纯函数，便于单测。
library;

/// 相对时间：把最后心跳渲染成「x 分钟前」。
String formatRelative(DateTime? value, {DateTime? now}) {
  if (value == null) return '从未上报';
  final reference = (now ?? DateTime.now()).toUtc();
  final target = value.toUtc();
  final diff = reference.difference(target);
  if (diff.isNegative) return '刚刚';
  if (diff.inSeconds < 60) return '${diff.inSeconds} 秒前';
  if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
  if (diff.inHours < 24) return '${diff.inHours} 小时前';
  return '${diff.inDays} 天前';
}

/// 绝对时间（本地时区，秒级精度裁剪到分钟）。
String formatAbsolute(DateTime? value) {
  if (value == null) return '—';
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}';
}

/// 心跳是否陈旧：以服务端下发的 `heartbeat_stale` 为准，
/// 这里只是把布尔渲染成人话。
String staleLabel(bool stale) => stale ? '心跳已过期' : '心跳正常';

/// 子网路由状态的本地化文案。
String subnetRouteLabel(String status) {
  switch (status.trim().toLowerCase()) {
    case 'ready':
      return '就绪';
    case 'degraded':
      return '降级';
    case 'unavailable':
      return '不可用';
    case '':
      return '未知';
    default:
      return status.trim();
  }
}

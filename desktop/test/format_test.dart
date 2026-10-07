import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/util/format.dart';

void main() {
  final now = DateTime.utc(2026, 10, 7, 12, 0, 0);

  test('相对时间分级', () {
    expect(formatRelative(null), '从未上报');
    expect(formatRelative(now.subtract(const Duration(seconds: 5)), now: now), '5 秒前');
    expect(formatRelative(now.subtract(const Duration(minutes: 3)), now: now), '3 分钟前');
    expect(formatRelative(now.subtract(const Duration(hours: 5)), now: now), '5 小时前');
    expect(formatRelative(now.subtract(const Duration(days: 2)), now: now), '2 天前');
  });

  test('未来时间显示「刚刚」而不是负数', () {
    expect(formatRelative(now.add(const Duration(minutes: 5)), now: now), '刚刚');
  });

  test('心跳与子网路由文案', () {
    expect(staleLabel(true), '心跳已过期');
    expect(staleLabel(false), '心跳正常');
    expect(subnetRouteLabel('ready'), '就绪');
    expect(subnetRouteLabel('degraded'), '降级');
    expect(subnetRouteLabel('unavailable'), '不可用');
    expect(subnetRouteLabel(''), '未知');
  });
}

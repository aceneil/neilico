import 'dart:io';

/// 发起远程连接的方式。
///
/// 本阶段（内核尚未接入）采用「调起系统已安装的 RustDesk 客户端」：
/// 用 `rustdesk://<id>` 交给操作系统处理。不自己实现画面/加密链路。
enum DesktopPlatform { linux, macos, windows }

DesktopPlatform? currentDesktopPlatform() {
  if (Platform.isLinux) return DesktopPlatform.linux;
  if (Platform.isMacOS) return DesktopPlatform.macos;
  if (Platform.isWindows) return DesktopPlatform.windows;
  return null;
}

/// 构造「打开 URL」的命令行。抽成纯函数便于单测。
class UrlOpenCommand {
  const UrlOpenCommand._();

  static List<String>? forPlatform(String url, DesktopPlatform platform) {
    switch (platform) {
      case DesktopPlatform.linux:
        return ['xdg-open', url];
      case DesktopPlatform.macos:
        return ['open', url];
      case DesktopPlatform.windows:
        return ['cmd', '/c', 'start', '', url];
    }
  }
}

/// 连接调起结果。
class LaunchResult {
  const LaunchResult({required this.ok, required this.message});

  final bool ok;
  final String message;

  static const LaunchResult unsupported = LaunchResult(
    ok: false,
    message: '当前平台不支持自动调起，请手动复制 rustdesk:// 地址到 RustDesk 客户端',
  );
}

/// 可注入的连接调起器（测试里替换成假实现）。
abstract class ConnectLauncher {
  Future<LaunchResult> launch(String url);
}

/// 生产实现：交给操作系统的 URL 处理器（xdg-open / open / start）。
///
/// 之所以不链接 RustDesk 内核：见 `rust-core/README.md`（方案②进程隔离，
/// AGPL 合规边界）。
class ProcessConnectLauncher implements ConnectLauncher {
  @override
  Future<LaunchResult> launch(String url) async {
    final platform = currentDesktopPlatform();
    if (platform == null) return LaunchResult.unsupported;
    final command = UrlOpenCommand.forPlatform(url, platform);
    if (command == null) return LaunchResult.unsupported;
    try {
      await Process.start(
        command.first,
        command.sublist(1),
        mode: ProcessStartMode.detached,
      );
      return LaunchResult(ok: true, message: '已请求系统打开 $url');
    } catch (error) {
      return LaunchResult(ok: false, message: '调起失败：$error');
    }
  }
}

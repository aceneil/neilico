import 'dart:io';

import '../../models/remote_desktop_config.dart';

/// 启动内核进程所需的参数。
///
/// 说明（方案②进程隔离）：
///  - NEILICO 客户端**不链接** RustDesk 内核，只把它当成一个外部可执行文件；
///  - 服务器参数（ID 服务器 / 中继 / 公钥）由控制面下发，通过配置文件传给内核；
///  - 公钥是唯一下发的密钥材料，私钥永不进入客户端。
class KernelLaunchConfig {
  const KernelLaunchConfig({
    required this.idServer,
    required this.relayServer,
    required this.publicKey,
    this.deviceId,
  });

  final String idServer;
  final String relayServer;
  final String publicKey;

  /// 本机期望使用的 RustDesk ID（可选，由内核持久化）。
  final String? deviceId;

  bool get isUsable =>
      idServer.trim().isNotEmpty && publicKey.trim().isNotEmpty;

  factory KernelLaunchConfig.fromRemoteDesktopConfig(
    RemoteDesktopConfig config, {
    String? deviceId,
  }) =>
      KernelLaunchConfig(
        idServer: config.idServer,
        relayServer: config.relayServer,
        publicKey: config.publicKey,
        deviceId: deviceId,
      );

  /// 渲染成内核配置文件（TOML）。
  ///
  /// **安全**：只写公钥。若 [publicKey] 为空，调用方必须先用 [isUsable] 拦截。
  String toToml() {
    final buffer = StringBuffer()
      ..writeln('# NEILICO 生成的运行时配置 —— 请勿手工编辑')
      ..writeln('[server]')
      ..writeln('id_server = ${_quote(idServer)}')
      ..writeln('relay_server = ${_quote(relayServer)}')
      ..writeln('public_key = ${_quote(publicKey)}')
      ..writeln()
      ..writeln('[render]')
      ..writeln('# 画面渲染由内核负责，NEILICO 不参与编解码')
      ..writeln('kernel_only = true');
    if (deviceId != null && deviceId!.trim().isNotEmpty) {
      buffer
        ..writeln()
        ..writeln('[identity]')
        ..writeln('device_id = ${_quote(deviceId!)}');
    }
    return buffer.toString();
  }

  static String _quote(String value) => '"${value.replaceAll('"', r'\"')}"';
}

/// 内核进程句柄/状态。
enum KernelProcessState { stopped, running, unsupported }

/// 内核进程边界的最小骨架实现。
///
/// 本轮**不要求真的把内核编出来**；这里的代码是把内核接上时的落点：
/// 启动、探活、停止。在此之前 UI 会显示「内核未安装」并置灰启动入口。
class KernelProcess {
  KernelProcess({required this.binaryPath, required this.config});

  final String binaryPath;
  final KernelLaunchConfig config;

  Process? _process;

  KernelProcessState get state =>
      _process == null ? KernelProcessState.stopped : KernelProcessState.running;

  /// 将执行的命令行（供 UI 展示「内核将如何被拉起」，便于排查）。
  List<String> buildArguments(String configPath) => <String>[
        '--config',
        configPath,
        '--service',
      ];

  /// 启动内核进程。配置文件由调用方负责写入临时目录。
  Future<bool> start(String configPath) async {
    if (!config.isUsable || _process != null) return false;
    _process = await Process.start(binaryPath, buildArguments(configPath));
    return true;
  }

  /// 停止内核进程（先 SIGTERM，平台支持时再杀）。
  Future<void> stop() async {
    final process = _process;
    if (process == null) return;
    _process = null;
    process.kill(ProcessSignal.sigterm);
    await process.exitCode.timeout(
      const Duration(seconds: 5),
      onTimeout: () {
        process.kill(ProcessSignal.sigkill);
        return -1;
      },
    );
  }
}

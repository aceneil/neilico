import 'dart:io';

/// 内核二进制的定位结果。找不到时 [reason] 必须说明「找过哪些位置」，
/// 便于用户排查（不允许静默失败）。
class KernelLocation {
  const KernelLocation.found(this.path)
      : reason = '',
        searched = const <String>[];

  const KernelLocation.missing(this.reason, this.searched) : path = null;

  final String? path;
  final String reason;
  final List<String> searched;

  bool get found => path != null && path!.isNotEmpty;
}

/// 按**约定路径**寻找 rust-core 内核可执行文件。
///
/// 方案②（进程隔离）下，客户端只知道一个可执行文件路径，不链接内核代码。
/// 约定（与 `rust-core/README.md` 保持一致）：
///   1. 环境变量 `NEILICO_KERNEL_BIN`（绝对路径，最高优先级）
///   2. 与客户端主程序同目录：`neilico-kernel`（Windows 为 `neilico-kernel.exe`）
///   3. 开发形态：`<repo>/rust-core/dist/<os>-<arch>/neilico-kernel`
///
/// 找不到时**不报错**，只是给出原因——UI 据此显示「内核未安装」并置灰启动入口。
class KernelLocator {
  KernelLocator({
    Map<String, String>? environment,
    String? executablePath,
    String? workingDirectory,
    List<String>? extraCandidates,
    bool Function(String path)? exists,
  })  : _env = environment ?? Platform.environment,
        _executablePath = executablePath ?? Platform.resolvedExecutable,
        _workingDirectory = workingDirectory ?? Directory.current.path,
        _extra = extraCandidates ?? const <String>[],
        _exists = exists ?? ((path) => File(path).existsSync());

  static const String binaryName = 'neilico-kernel';

  static String binaryFileName({required bool isWindows}) =>
      isWindows ? '$binaryName.exe' : binaryName;

  final Map<String, String> _env;
  final String _executablePath;
  final String _workingDirectory;
  final List<String> _extra;
  final bool Function(String path) _exists;

  /// 平台目录名，例如 `linux-x86_64`。与 rust-core 构建产物命名保持一致。
  static String platformTriple({String? os, String? arch}) {
    final osName = os ?? Platform.operatingSystem;
    final archName = arch ?? _archLabel();
    return '$osName-$archName';
  }

  static String _archLabel() {
    final version = Platform.version;
    // dart:io 在部分平台上没有直接的架构访问器，用非 NUL 的常见映射兜底。
    if (version.contains('arm64') || version.contains('aarch64')) return 'aarch64';
    if (version.contains('arm')) return 'armv7';
    return 'x86_64';
  }

  KernelLocation locate() {
    final searched = <String>[];
    final fileName = binaryFileName(isWindows: Platform.isWindows);

    void consider(String? candidate) {
      if (candidate == null || candidate.trim().isEmpty) return;
      searched.add(candidate);
    }

    final envCandidate = _env['NEILICO_KERNEL_BIN'];
    consider(envCandidate);
    if (envCandidate != null && envCandidate.trim().isNotEmpty && _exists(envCandidate.trim())) {
      return KernelLocation.found(envCandidate.trim());
    }

    final executableDir = File(_executablePath).parent.path;
    final triple = platformTriple();

    // 每一项都是「目录」，真正的文件名在循环里拼接。
    final builtIn = <String>[
      executableDir,
      _join(_join(executableDir, '..'), _join('rust-core/dist', triple)),
      _join(_join(_workingDirectory, 'rust-core'), _join('dist', triple)),
      _join(_join(_workingDirectory, '..'), _join('rust-core/dist', triple)),
      ..._extra,
    ];

    for (final candidate in builtIn) {
      final path = _join(candidate, fileName);
      consider(path);
      if (_exists(path)) return KernelLocation.found(path);
    }

    return KernelLocation.missing(
      '未找到内核进程（$fileName）。请先构建 rust-core 构建产物，'
      '或把内核放到客户端同级目录，或用 NEILICO_KERNEL_BIN 指定绝对路径。',
      searched,
    );
  }

  static String _join(String left, String right) {
    if (left.endsWith('/')) return '$left$right';
    return '$left/$right';
  }
}

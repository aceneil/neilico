import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/services/connect_launcher.dart';
import 'package:neilico_desktop/services/kernel/kernel_locator.dart';

void main() {
  final kernelFile = KernelLocator.binaryFileName(isWindows: Platform.isWindows);

  group('内核定位（方案②：只认可执行文件路径）', () {
    test('NEILICO_KERNEL_BIN 优先', () {
      const envPath = '/opt/neilico/neilico-kernel';
      final locator = KernelLocator(
        environment: {'NEILICO_KERNEL_BIN': envPath},
        executablePath: '/app/neilico_desktop',
        workingDirectory: '/work',
        exists: (path) => path == envPath,
      );
      final result = locator.locate();
      expect(result.found, isTrue);
      expect(result.path, envPath);
    });

    test('回落到主程序同级目录', () {
      final expected = '/app/$kernelFile';
      final locator = KernelLocator(
        environment: const {},
        executablePath: '/app/neilico_desktop',
        workingDirectory: '/work',
        exists: (path) => path == expected,
      );
      expect(locator.locate().path, expected);
    });

    test('回落到仓库 rust-core/dist/<platform> 构建产物', () {
      final expected = '/work/rust-core/dist/${KernelLocator.platformTriple()}/$kernelFile';
      final locator = KernelLocator(
        environment: const {},
        executablePath: '/app/neilico_desktop',
        workingDirectory: '/work',
        exists: (path) => path == expected,
      );
      final result = locator.locate();
      expect(result.found, isTrue);
      expect(result.path, expected);
    });

    test('找不到时给出原因与搜索过的路径清单', () {
      final locator = KernelLocator(
        environment: const {},
        executablePath: '/app/neilico_desktop',
        workingDirectory: '/work',
        exists: (_) => false,
      );
      final result = locator.locate();
      expect(result.found, isFalse);
      expect(result.reason, contains('未找到内核进程'));
      expect(result.searched, isNotEmpty);
    });

    test('平台目录命名包含 os-arch', () {
      expect(KernelLocator.platformTriple(os: 'linux', arch: 'x86_64'), 'linux-x86_64');
      expect(KernelLocator.platformTriple(os: 'macos', arch: 'aarch64'), 'macos-aarch64');
    });
  });

  group('rustdesk:// 调起命令', () {
    test('三端命令参数正确', () {
      expect(
        UrlOpenCommand.forPlatform('rustdesk://123', DesktopPlatform.linux),
        ['xdg-open', 'rustdesk://123'],
      );
      expect(
        UrlOpenCommand.forPlatform('rustdesk://123', DesktopPlatform.macos),
        ['open', 'rustdesk://123'],
      );
      expect(
        UrlOpenCommand.forPlatform('rustdesk://123', DesktopPlatform.windows),
        ['cmd', '/c', 'start', '', 'rustdesk://123'],
      );
    });
  });
}

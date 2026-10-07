import 'package:flutter/material.dart';

/// NEILICO 设计令牌。
///
/// 约定：**任何 widget 都不得硬编码颜色 / 间距 / 圆角**，一律引用这里的令牌，
/// 以保证日/夜主题一致、4K 缩放下比例统一。
class NeilicoTokens {
  const NeilicoTokens._();

  /// 品牌主色：NEILICO 的青绿（刻意区别于 RustDesk 的蓝）。
  static const Color brandSeed = Color(0xFF12A594);

  /// 次要强调色：用于连接 / 主行动按钮。
  static const Color accentSeed = Color(0xFF2D7FF9);

  /// 语义色（状态点）。深浅色主题下对比度都足够。
  static const Color online = Color(0xFF19A974);
  static const Color offline = Color(0xFF8A94A6);
  static const Color warning = Color(0xFFE0A32E);
  static const Color danger = Color(0xFFD64545);

  // 8pt 间距栅格。
  static const double spaceXs = 4;
  static const double spaceSm = 8;
  static const double spaceMd = 16;
  static const double spaceLg = 24;
  static const double spaceXl = 32;

  static const double radiusSm = 8;
  static const double radiusMd = 12;
  static const double radiusLg = 18;

  /// 设备网格的响应式列宽（一屏尽量放下，4K 也不会拉得过大）。
  static const double cardMinWidth = 320;
  static const double cardMaxWidth = 460;

  /// 超过该宽度视为「宽屏」，用双栏（左：设备网格；右：详情）以外的地方可自行伸缩。
  static const double wideBreakpoint = 1180;
}

/// 由令牌构建 ThemeData；light / dark 共用同一套结构。
class NeilicoTheme {
  const NeilicoTheme._();

  static ThemeData light() => _build(Brightness.light);

  static ThemeData dark() => _build(Brightness.dark);

  static ThemeData _build(Brightness brightness) {
    final scheme = ColorScheme.fromSeed(
      seedColor: NeilicoTokens.brandSeed,
      brightness: brightness,
    );
    final base = ThemeData(colorScheme: scheme, useMaterial3: true);

    return base.copyWith(
      scaffoldBackgroundColor: scheme.surface,
      cardTheme: CardThemeData(
        elevation: 0,
        color: scheme.surfaceContainerLow,
        clipBehavior: Clip.antiAlias,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(NeilicoTokens.radiusMd),
          side: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainerHighest,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
          borderSide: BorderSide(color: scheme.primary, width: 1.6),
        ),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: NeilicoTokens.spaceMd,
          vertical: NeilicoTokens.spaceMd,
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          minimumSize: const Size(0, 48),
          padding: const EdgeInsets.symmetric(horizontal: NeilicoTokens.spaceLg),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
          ),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          minimumSize: const Size(0, 44),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
          ),
        ),
      ),
      appBarTheme: AppBarTheme(
        backgroundColor: scheme.surface,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        centerTitle: false,
      ),
      dividerTheme: DividerThemeData(color: scheme.outlineVariant, space: 1, thickness: 1),
      snackBarTheme: const SnackBarThemeData(behavior: SnackBarBehavior.floating),
    );
  }
}

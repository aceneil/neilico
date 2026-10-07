import 'package:flutter/material.dart';

import 'screens/devices_screen.dart';
import 'screens/login_screen.dart';
import 'services/app_controller.dart';
import 'theme/neilico_theme.dart';

/// 应用外壳：主题 + 路由 + 顶层阶段切换。
class NeilicoApp extends StatefulWidget {
  const NeilicoApp({super.key, required this.controller});

  final AppController controller;

  @override
  State<NeilicoApp> createState() => _NeilicoAppState();
}

class _NeilicoAppState extends State<NeilicoApp> {
  final GlobalKey<NavigatorState> _navigator = GlobalKey<NavigatorState>();

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_handlePhaseChange);
  }

  @override
  void dispose() {
    widget.controller.removeListener(_handlePhaseChange);
    super.dispose();
  }

  /// 退出登录或令牌失效时，把详情页等压栈路由弹回根，
  /// 避免用户停留在已经无权访问的页面。
  void _handlePhaseChange() {
    if (widget.controller.phase != AppPhase.ready) {
      _navigator.currentState?.popUntil((route) => route.isFirst);
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'NEILICO',
      debugShowCheckedModeBanner: false,
      navigatorKey: _navigator,
      theme: NeilicoTheme.light(),
      darkTheme: NeilicoTheme.dark(),
      themeMode: ThemeMode.system,
      home: AnimatedBuilder(
        animation: widget.controller,
        builder: (context, _) {
          switch (widget.controller.phase) {
            case AppPhase.booting:
              return const _BootScreen();
            case AppPhase.loggedOut:
              return LoginScreen(controller: widget.controller);
            case AppPhase.ready:
              return DevicesScreen(controller: widget.controller);
          }
        },
      ),
    );
  }
}

class _BootScreen extends StatelessWidget {
  const _BootScreen();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 52,
              height: 52,
              decoration: BoxDecoration(
                color: theme.colorScheme.primary,
                borderRadius: BorderRadius.circular(NeilicoTokens.radiusMd),
              ),
              alignment: Alignment.center,
              child: Text(
                'N',
                style: theme.textTheme.headlineSmall?.copyWith(
                  color: theme.colorScheme.onPrimary,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(height: NeilicoTokens.spaceLg),
            const SizedBox(
              width: 22,
              height: 22,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(height: NeilicoTokens.spaceMd),
            Text('正在恢复会话…', style: theme.textTheme.bodyMedium),
          ],
        ),
      ),
    );
  }
}

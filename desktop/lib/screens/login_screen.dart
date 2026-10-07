import 'package:flutter/material.dart';

import '../services/app_controller.dart';
import '../theme/neilico_theme.dart';
import '../util/validators.dart';

/// 登录页：对接控制面 `/api/v1/auth/login`。
///
/// 令牌由 [AppController] 交给操作系统密钥库保存，界面不接触明文。
class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, required this.controller});

  final AppController controller;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();
  late final TextEditingController _server = TextEditingController(text: widget.controller.apiBase);
  bool _obscure = true;
  bool _showServer = false;
  String? _errorText;

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    _server.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _errorText = null);
    if (!(_formKey.currentState?.validate() ?? false)) return;
    final message = await widget.controller.login(
      email: _email.text,
      password: _password.text,
      apiBase: _server.text,
    );
    if (!mounted) return;
    if (message != null) {
      setState(() => _errorText = message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final busy = widget.controller.busy;

    return Scaffold(
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(NeilicoTokens.spaceXl),
                child: Form(
                  key: _formKey,
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        children: [
                          Container(
                            width: 40,
                            height: 40,
                            decoration: BoxDecoration(
                              color: theme.colorScheme.primary,
                              borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
                            ),
                            alignment: Alignment.center,
                            child: Text(
                              'N',
                              style: theme.textTheme.titleLarge?.copyWith(
                                color: theme.colorScheme.onPrimary,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                          ),
                          const SizedBox(width: NeilicoTokens.spaceMd),
                          Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text('NEILICO', style: theme.textTheme.titleLarge),
                              Text(
                                '设备管理端 · 远程桌面',
                                style: theme.textTheme.bodySmall?.copyWith(
                                  color: theme.colorScheme.onSurfaceVariant,
                                ),
                              ),
                            ],
                          ),
                        ],
                      ),
                      const SizedBox(height: NeilicoTokens.spaceXl),
                      TextFormField(
                        key: const Key('login-email'),
                        controller: _email,
                        autofillHints: const [AutofillHints.username],
                        keyboardType: TextInputType.emailAddress,
                        textInputAction: TextInputAction.next,
                        decoration: const InputDecoration(
                          labelText: '邮箱',
                          prefixIcon: Icon(Icons.alternate_email),
                        ),
                        validator: LoginValidators.email,
                      ),
                      const SizedBox(height: NeilicoTokens.spaceMd),
                      TextFormField(
                        key: const Key('login-password'),
                        controller: _password,
                        obscureText: _obscure,
                        autofillHints: const [AutofillHints.password],
                        textInputAction: TextInputAction.done,
                        onFieldSubmitted: (_) => _submit(),
                        decoration: InputDecoration(
                          labelText: '密码',
                          prefixIcon: const Icon(Icons.lock_outline),
                          suffixIcon: IconButton(
                            tooltip: _obscure ? '显示密码' : '隐藏密码',
                            icon: Icon(_obscure ? Icons.visibility_outlined : Icons.visibility_off_outlined),
                            onPressed: () => setState(() => _obscure = !_obscure),
                          ),
                        ),
                        validator: LoginValidators.password,
                      ),
                      const SizedBox(height: NeilicoTokens.spaceSm),
                      Align(
                        alignment: Alignment.centerLeft,
                        child: TextButton.icon(
                          onPressed: () => setState(() => _showServer = !_showServer),
                          icon: Icon(_showServer ? Icons.expand_less : Icons.expand_more, size: 18),
                          label: const Text('控制面地址'),
                        ),
                      ),
                      if (_showServer)
                        TextFormField(
                          key: const Key('login-server'),
                          controller: _server,
                          decoration: const InputDecoration(
                            labelText: '控制面地址',
                            hintText: 'https://neilico.example.com',
                            prefixIcon: Icon(Icons.dns_outlined),
                          ),
                          validator: LoginValidators.serverBase,
                        ),
                      if (_errorText != null) ...[
                        const SizedBox(height: NeilicoTokens.spaceMd),
                        _ErrorBanner(message: _errorText!),
                      ],
                      const SizedBox(height: NeilicoTokens.spaceLg),
                      FilledButton(
                        key: const Key('login-submit'),
                        onPressed: busy ? null : _submit,
                        child: busy
                            ? const SizedBox(
                                width: 20,
                                height: 20,
                                child: CircularProgressIndicator(strokeWidth: 2),
                              )
                            : const Text('登录'),
                      ),
                      const SizedBox(height: NeilicoTokens.spaceMd),
                      Text(
                        '令牌仅保存在操作系统密钥库，绝不明文落盘；界面不显示、不回显任何密钥。',
                        textAlign: TextAlign.center,
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(NeilicoTokens.spaceSm + 4),
      decoration: BoxDecoration(
        color: NeilicoTokens.danger.withValues(alpha: 0.10),
        borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
        border: Border.all(color: NeilicoTokens.danger.withValues(alpha: 0.35)),
      ),
      child: Row(
        children: [
          const Icon(Icons.error_outline, size: 18, color: NeilicoTokens.danger),
          const SizedBox(width: NeilicoTokens.spaceSm),
          Expanded(
            child: Text(message, style: theme.textTheme.bodySmall),
          ),
        ],
      ),
    );
  }
}

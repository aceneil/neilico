/// 登录表单校验。抽成纯函数便于单测（无 widget / 无网络依赖）。
class LoginValidators {
  const LoginValidators._();

  /// 宽松但明确的邮箱规则：本地部分@域名.后缀，禁止空白。
  static final RegExp _email = RegExp(r'^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$');

  static String? email(String? value) {
    final text = (value ?? '').trim();
    if (text.isEmpty) return '请输入邮箱';
    if (!_email.hasMatch(text)) return '邮箱格式不正确';
    return null;
  }

  static String? password(String? value) {
    final text = value ?? '';
    if (text.isEmpty) return '请输入密码';
    if (text.length < 6) return '密码至少 6 位';
    return null;
  }

  /// 服务器地址（控制面 base URL）校验。
  static String? serverBase(String? value) {
    final text = (value ?? '').trim();
    if (text.isEmpty) return '请输入控制面地址';
    final uri = Uri.tryParse(text);
    if (uri == null || !uri.hasScheme || (uri.scheme != 'http' && uri.scheme != 'https')) {
      return '地址需以 http:// 或 https:// 开头';
    }
    if (uri.host.isEmpty) return '地址缺少主机名';
    return null;
  }
}

import 'package:flutter_test/flutter_test.dart';
import 'package:neilico_desktop/util/validators.dart';

void main() {
  group('邮箱校验', () {
    test('为空时报错', () {
      expect(LoginValidators.email(''), isNotNull);
      expect(LoginValidators.email(null), isNotNull);
      expect(LoginValidators.email('   '), isNotNull);
    });

    test('缺少域名或含空格时报错', () {
      expect(LoginValidators.email('ops'), isNotNull);
      expect(LoginValidators.email('ops@'), isNotNull);
      expect(LoginValidators.email('ops@neilico'), isNotNull);
      expect(LoginValidators.email('ops @neilico.local'), isNotNull);
    });

    test('合法邮箱通过', () {
      expect(LoginValidators.email('ops@neilico.local'), isNull);
      expect(LoginValidators.email('  dev@neilico.local  '), isNull);
    });
  });

  group('密码校验', () {
    test('为空或过短时报错', () {
      expect(LoginValidators.password(''), isNotNull);
      expect(LoginValidators.password('12345'), isNotNull);
    });

    test('达到长度要求即通过', () {
      expect(LoginValidators.password('123456'), isNull);
    });
  });

  group('控制面地址校验', () {
    test('缺少协议头时报错', () {
      expect(LoginValidators.serverBase('neilico.local'), isNotNull);
      expect(LoginValidators.serverBase('ftp://neilico.local'), isNotNull);
      expect(LoginValidators.serverBase(''), isNotNull);
    });

    test('http/https 通过', () {
      expect(LoginValidators.serverBase('http://127.0.0.1:8080'), isNull);
      expect(LoginValidators.serverBase('https://neilico.example.com'), isNull);
    });
  });
}

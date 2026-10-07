import '../util/json.dart';

/// 远控服务器参数（`GET /api/v1/remote-desktop/config`）。
///
/// 安全约定：**只有公钥**（`public_key`）。私钥永远不下发到客户端。
class RemoteDesktopConfig {
  const RemoteDesktopConfig({
    required this.enabled,
    required this.idServer,
    required this.relayServer,
    required this.publicKey,
    required this.available,
    required this.hint,
    required this.ports,
  });

  final bool enabled;
  final String idServer;
  final String relayServer;
  final String publicKey;

  /// 服务器参数是否就绪（后端能读到公钥文件且非空）。false 时 UI 必须明确提示「未就绪」。
  final bool available;
  final String hint;
  final List<int> ports;

  static const RemoteDesktopConfig unknown = RemoteDesktopConfig(
    enabled: false,
    idServer: '',
    relayServer: '',
    publicKey: '',
    available: false,
    hint: '尚未从控制面获取服务器参数',
    ports: <int>[],
  );

  /// 客户端可用它来发起连接：服务器就绪且配置启用。
  bool get usable => enabled && available;

  factory RemoteDesktopConfig.fromJson(Map<String, dynamic> json) => RemoteDesktopConfig(
        enabled: asBool(json['enabled']),
        idServer: asString(json['id_server']),
        relayServer: asString(json['relay_server']),
        publicKey: asString(json['public_key']),
        available: asBool(json['available']),
        hint: asString(json['hint']),
        ports: asList(json['ports']).map((value) => asInt(value)).toList(growable: false),
      );
}

/// 单个端口的探活结果。
class RemoteDesktopPortStatus {
  const RemoteDesktopPortStatus({
    required this.port,
    required this.target,
    required this.reachable,
    this.error,
  });

  final int port;
  final String target;
  final bool reachable;
  final String? error;

  factory RemoteDesktopPortStatus.fromJson(Map<String, dynamic> json) => RemoteDesktopPortStatus(
        port: asInt(json['port']),
        target: asString(json['target']),
        reachable: asBool(json['reachable']),
        error: asNullableString(json['error']),
      );
}

/// `GET /api/v1/remote-desktop/status` 的探活结果。
class RemoteDesktopStatus {
  const RemoteDesktopStatus({
    required this.idServerHost,
    required this.relayServerHost,
    required this.ports,
    required this.reachable,
    this.checkedAt,
  });

  final String idServerHost;
  final String relayServerHost;
  final List<RemoteDesktopPortStatus> ports;
  final bool reachable;
  final DateTime? checkedAt;

  factory RemoteDesktopStatus.fromJson(Map<String, dynamic> json) => RemoteDesktopStatus(
        idServerHost: asString(json['id_server_host']),
        relayServerHost: asString(json['relay_server_host']),
        ports: parseList(json['ports'], RemoteDesktopPortStatus.fromJson),
        reachable: asBool(json['reachable']),
        checkedAt: asDateTime(json['checked_at']),
      );
}

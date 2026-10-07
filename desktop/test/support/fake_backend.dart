import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// 造一台设备的 JSON（对齐后端 `RemoteDesktopDevice` 的字段）。
Map<String, dynamic> deviceJson({
  String id = '11111111-1111-1111-1111-111111111111',
  String name = '办公室主机',
  String status = 'online',
  String? virtualIp = '10.42.0.7',
  String? lastSeen = '2026-10-07T02:00:00Z',
  bool heartbeatStale = false,
  String os = 'linux',
  String arch = 'x86_64',
  String rustdeskId = '123456789',
}) =>
    {
      'id': id,
      'name': name,
      'status': status,
      'virtual_ip': virtualIp,
      'last_seen': lastSeen,
      'heartbeat_stale': heartbeatStale,
      'platform': os.isEmpty && arch.isEmpty ? '未知' : '$os/$arch',
      'os': os,
      'arch': arch,
      'rustdesk_id': rustdeskId,
      'rustdesk_hint': rustdeskId,
      'connect_url': rustdeskId.isEmpty ? '' : 'rustdesk://$rustdeskId',
      'connection_params': '设备: $name\nKey: PUBLICKEY',
    };

/// 造一份设备授权状态 JSON（约定中的 `/device-policies` 结构）。
Map<String, dynamic> policyJson({
  required String nodeId,
  bool remoteControlAllowed = true,
  String tunnelMode = 'auto',
  bool isolatedTunnel = false,
  bool meshJoined = false,
  String? networkId = 'net-1',
  String? virtualIp = '10.42.0.7',
  String subnetRoutes = 'ready',
}) =>
    {
      'node_id': nodeId,
      'remote_control_allowed': remoteControlAllowed,
      'tunnel_mode': tunnelMode,
      'isolated_tunnel': {'enabled': isolatedTunnel, 'stream_rule_id': null},
      'mesh': {'joined': meshJoined, 'network_id': networkId, 'virtual_ip': virtualIp},
      'readonly': {'subnet_routes': subnetRoutes},
    };

/// 可编排的假控制面：按路径返回预置响应，并记录收到的请求。
class FakeBackend {
  FakeBackend({
    this.devices = const <Map<String, dynamic>>[],
    this.policies = const <Map<String, dynamic>>[],
    this.loginStatus = 200,
    this.policiesStatus = 200,
    this.configAvailable = true,
    this.serverEnabled = true,
    this.devicesStatus = 200,
  });

  final List<Map<String, dynamic>> devices;
  final List<Map<String, dynamic>> policies;
  final int loginStatus;
  final int policiesStatus;
  final bool configAvailable;
  final bool serverEnabled;
  final int devicesStatus;

  final List<http.Request> requests = <http.Request>[];

  String get lastPath => requests.isEmpty ? '' : requests.last.url.path;

  http.Client client() => MockClient((request) async {
        requests.add(request);
        final path = request.url.path;

        switch (path) {
          case '/api/v1/auth/login':
            if (loginStatus != 200) {
              return _json({'error': 'invalid_credentials', 'message': 'invalid email or password'}, loginStatus);
            }
            return _json({
              'token': 'access-token-1',
              'refresh_token': 'refresh-token-1',
              'user': {
                'id': 'u-1',
                'email': 'ops@neilico.local',
                'role': 'platform_admin',
                'tenant_id': 't-1',
              },
            });
          case '/api/v1/auth/refresh':
            return _json({
              'token': 'access-token-2',
              'refresh_token': 'refresh-token-2',
              'user': {
                'id': 'u-1',
                'email': 'ops@neilico.local',
                'role': 'platform_admin',
                'tenant_id': 't-1',
              },
            });
          case '/api/v1/remote-desktop/config':
            return _json({
              'enabled': serverEnabled,
              'id_server': 'rd.neilico.local',
              'relay_server': 'rd.neilico.local',
              'public_key': configAvailable ? 'PUBLICKEY' : '',
              'available': configAvailable,
              'hint': configAvailable ? '服务器已就绪' : '服务器未就绪：未能读取公钥文件',
              'ports': [21115, 21116, 21117],
            });
          case '/api/v1/remote-desktop/devices':
            if (devicesStatus != 200) {
              return _json({'error': 'internal', 'message': 'boom'}, devicesStatus);
            }
            return _json({'items': devices, 'total': devices.length});
          case '/api/v1/remote-desktop/device-policies':
            if (policiesStatus != 200) {
              return _json({'error': 'not_found', 'message': 'endpoint not implemented'}, policiesStatus);
            }
            return _json({'items': policies, 'total': policies.length});
          case '/api/v1/remote-desktop/status':
            return _json({
              'id_server_host': 'rd.neilico.local',
              'relay_server_host': 'rd.neilico.local',
              'ports': [
                {'port': 21115, 'target': 'rd.neilico.local:21115', 'reachable': true},
              ],
              'reachable': true,
              'checked_at': '2026-10-07T02:00:00Z',
            });
        }

        if (path.startsWith('/api/v1/remote-desktop/device-policies/')) {
          final nodeId = path.split('/').last;
          return _json({
            'item': policyJson(nodeId: nodeId, remoteControlAllowed: false, tunnelMode: 'relay'),
          });
        }

        return _json({'error': 'not_found', 'message': 'no such path: $path'}, 404);
      });

  static http.Response _json(Map<String, dynamic> body, [int status = 200]) =>
      http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});
}

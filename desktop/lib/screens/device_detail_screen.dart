import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../models/device.dart';
import '../models/device_switches.dart';
import '../services/app_controller.dart';
import '../theme/neilico_theme.dart';
import '../util/format.dart';
import '../widgets/info_row.dart';
import '../widgets/status_pill.dart';
import '../widgets/switch_panel.dart';

/// 单台设备的详情与开关面板。
///
/// 开关的权威状态来自后端（[AppController.policies]）；后端未就绪时
/// [SwitchPanel] 会把控件置灰并写明原因。
class DeviceDetailScreen extends StatelessWidget {
  const DeviceDetailScreen({
    super.key,
    required this.controller,
    required this.nodeId,
  });

  final AppController controller;
  final String nodeId;

  RemoteDesktopDevice? _device() {
    for (final device in controller.devices) {
      if (device.id == nodeId) return device;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final device = _device();
    if (device == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('设备详情')),
        body: const EmptyState(
          icon: Icons.search_off,
          title: '设备已不在当前列表中',
          description: '可能已被删除，或刷新后不在本页数据里。',
        ),
      );
    }

    final panel = controller.panelFor(device);
    final policy = controller.policies.forNode(device.id);

    return Scaffold(
      appBar: AppBar(
        title: Text(device.displayName),
        actions: [
          IconButton(
            tooltip: '刷新',
            onPressed: controller.busy ? null : controller.reload,
            icon: const Icon(Icons.refresh),
          ),
          const SizedBox(width: NeilicoTokens.spaceSm),
        ],
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 880),
          child: ListView(
            padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
            children: [
              _HeaderCard(device: device),
              const SizedBox(height: NeilicoTokens.spaceMd),
              _ConnectCard(controller: controller, device: device, panel: panel),
              const SizedBox(height: NeilicoTokens.spaceMd),
              _SectionCard(
                title: '能力开关',
                subtitle: controller.policies.isAvailable
                    ? '开关状态以控制面为准，修改后立即同步。'
                    : controller.policies.reason,
                child: SwitchPanel(
                  panel: panel,
                  onRemoteControlAllowed: (value) => _apply(
                    context,
                    controller.setRemoteControlAllowed(device.id, value),
                  ),
                  onTunnelMode: (value) => _apply(
                    context,
                    controller.setTunnelMode(device.id, value),
                  ),
                  onIsolatedTunnel: (value) => _apply(
                    context,
                    controller.setIsolatedTunnel(device.id, value),
                  ),
                  onMeshJoined: (value) => _apply(
                    context,
                    controller.setMeshJoined(device.id, value),
                  ),
                ),
              ),
              const SizedBox(height: NeilicoTokens.spaceMd),
              _SectionCard(
                title: '只读信息',
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    InfoRow(label: '平台 / 架构', value: device.platformLabel),
                    InfoRow(label: '状态', value: device.status.label),
                    InfoRow(
                      label: '最后心跳',
                      value: '${formatRelative(device.lastSeen)}（${formatAbsolute(device.lastSeen)}）',
                    ),
                    InfoRow(label: '心跳判定', value: staleLabel(device.heartbeatStale)),
                    InfoRow(label: '虚拟 IP', value: device.virtualIp ?? '未分配'),
                    InfoRow(
                      label: 'Mesh 网络',
                      value: policy?.mesh.networkId ?? '—',
                    ),
                    InfoRow(
                      label: '子网路由',
                      value: subnetRouteLabel(policy?.subnetRoutesStatus ?? ''),
                    ),
                    InfoRow(label: 'RustDesk ID', value: device.hasRustdeskId ? device.rustdeskId : '未上报'),
                  ],
                ),
              ),
              const SizedBox(height: NeilicoTokens.spaceMd),
              _SectionCard(
                title: '连接参数',
                subtitle: '仅含公钥，可直接填进内核进程；NEILICO 不保存任何私钥。',
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SelectableText(
                      device.connectionParams.isEmpty ? '（后端未下发连接参数）' : device.connectionParams,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(fontFamily: 'monospace'),
                    ),
                    const SizedBox(height: NeilicoTokens.spaceMd),
                    OutlinedButton.icon(
                      onPressed: device.connectionParams.isEmpty
                          ? null
                          : () => _copy(context, '连接参数', device.connectionParams),
                      icon: const Icon(Icons.copy_all_outlined),
                      label: const Text('复制连接参数'),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: NeilicoTokens.spaceMd),
              _KernelCard(controller: controller, device: device),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _apply(BuildContext context, Future<ActionResult> action) async {
    final result = await action;
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(result.message)),
    );
  }

  Future<void> _copy(BuildContext context, String label, String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('已复制$label')),
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.device});

  final RemoteDesktopDevice device;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tone = device.heartbeatStale
        ? PillTone.warning
        : (device.isReachable ? PillTone.online : PillTone.offline);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
        child: Row(
          children: [
            Icon(Icons.computer_outlined, size: 36, color: theme.colorScheme.primary),
            const SizedBox(width: NeilicoTokens.spaceMd),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(device.displayName, style: theme.textTheme.titleLarge),
                  const SizedBox(height: NeilicoTokens.spaceXs),
                  Text(
                    '${device.platformLabel} · ${device.virtualIp ?? '未分配虚拟 IP'}',
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
            StatusPill(
              label: device.heartbeatStale ? '${device.status.label}（陈旧）' : device.status.label,
              tone: tone,
            ),
          ],
        ),
      ),
    );
  }
}

class _ConnectCard extends StatelessWidget {
  const _ConnectCard({required this.controller, required this.device, required this.panel});

  final AppController controller;
  final RemoteDesktopDevice device;
  final DeviceSwitchPanel panel;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final gate = panel.connect;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('发起远程连接', style: theme.textTheme.titleMedium),
            const SizedBox(height: NeilicoTokens.spaceXs),
            Text(
              gate.enabled
                  ? '将通过系统调起 RustDesk 客户端：${gate.value}'
                  : gate.message,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: NeilicoTokens.spaceMd),
            Row(
              children: [
                FilledButton.icon(
                  key: const Key('connect-button'),
                  onPressed: gate.enabled
                      ? () async {
                          final result = await controller.connect(device);
                          if (!context.mounted) return;
                          ScaffoldMessenger.of(context).showSnackBar(
                            SnackBar(content: Text(result.message)),
                          );
                        }
                      : null,
                  icon: const Icon(Icons.play_arrow),
                  label: const Text('连接'),
                ),
                const SizedBox(width: NeilicoTokens.spaceSm),
                if (!gate.enabled)
                  const Icon(Icons.lock_outline, size: 16),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _KernelCard extends StatelessWidget {
  const _KernelCard({required this.controller, required this.device});

  final AppController controller;
  final RemoteDesktopDevice device;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final kernel = controller.kernel;
    return _SectionCard(
      title: '内核进程（rust-core）',
      subtitle: '方案②进程隔离：内核作为独立进程接入，不链接、不修改其源码。',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InfoRow(
            label: '内核状态',
            value: kernel.found ? '已找到可执行文件' : '未安装',
          ),
          if (kernel.found)
            InfoRow(label: '路径', value: kernel.path!, mono: true)
          else
            Padding(
              padding: const EdgeInsets.only(top: NeilicoTokens.spaceXs),
              child: Text(
                kernel.reason,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          InfoRow(
            label: '服务器参数',
            value: controller.config.usable
                ? '${controller.config.idServer}（公钥已就绪）'
                : controller.config.hint,
          ),
        ],
      ),
    );
  }
}

class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.child, this.subtitle});

  final String title;
  final String? subtitle;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title, style: theme.textTheme.titleMedium),
            if (subtitle != null) ...[
              const SizedBox(height: NeilicoTokens.spaceXs),
              Text(
                subtitle!,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
            const SizedBox(height: NeilicoTokens.spaceMd),
            child,
          ],
        ),
      ),
    );
  }
}

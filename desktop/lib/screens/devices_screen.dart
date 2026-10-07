import 'package:flutter/material.dart';

import '../models/device.dart';
import '../services/app_controller.dart';
import '../theme/neilico_theme.dart';
import '../widgets/device_card.dart';
import '../widgets/info_row.dart';
import '../widgets/status_pill.dart';
import 'device_detail_screen.dart';

/// 设备网格页：登录后的主界面。
class DevicesScreen extends StatelessWidget {
  const DevicesScreen({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final devices = controller.devices;

    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Text('NEILICO', style: theme.textTheme.titleLarge),
            const SizedBox(width: NeilicoTokens.spaceSm),
            Text(
              '设备网格',
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
        actions: [
          if (controller.session != null)
            Padding(
              padding: const EdgeInsets.only(right: NeilicoTokens.spaceSm),
              child: Center(
                child: Text(
                  controller.session!.user.email,
                  style: theme.textTheme.bodySmall,
                ),
              ),
            ),
          IconButton(
            tooltip: '刷新',
            onPressed: controller.busy ? null : controller.reload,
            icon: const Icon(Icons.refresh),
          ),
          IconButton(
            tooltip: '退出登录',
            onPressed: controller.logout,
            icon: const Icon(Icons.logout),
          ),
          const SizedBox(width: NeilicoTokens.spaceSm),
        ],
      ),
      body: Padding(
        padding: const EdgeInsets.all(NeilicoTokens.spaceLg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _StatusStrip(controller: controller),
            if (controller.error != null) ...[
              const SizedBox(height: NeilicoTokens.spaceMd),
              _InlineNote(
                icon: Icons.warning_amber_outlined,
                text: controller.error!,
                tone: NeilicoTokens.warning,
              ),
            ],
            const SizedBox(height: NeilicoTokens.spaceLg),
            Expanded(
              child: devices.isEmpty
                  ? EmptyState(
                      icon: controller.busy ? Icons.hourglass_empty : Icons.devices_other_outlined,
                      title: controller.busy ? '正在加载设备…' : '还没有设备',
                      description: controller.busy
                          ? null
                          : '在控制面生成接入令牌，把节点 Agent 安装到目标设备后即可在此管理。',
                      action: controller.busy
                          ? null
                          : OutlinedButton.icon(
                              onPressed: controller.reload,
                              icon: const Icon(Icons.refresh),
                              label: const Text('重新加载'),
                            ),
                    )
                  : _DeviceGrid(
                      devices: devices,
                      controller: controller,
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusStrip extends StatelessWidget {
  const _StatusStrip({required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final config = controller.config;
    final policies = controller.policies;

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(NeilicoTokens.spaceMd),
        child: Wrap(
          spacing: NeilicoTokens.spaceLg,
          runSpacing: NeilicoTokens.spaceSm,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('远控服务器', style: theme.textTheme.labelLarge),
                const SizedBox(width: NeilicoTokens.spaceSm),
                StatusPill(
                  label: config.usable ? '已就绪' : (config.enabled ? '未就绪' : '未启用'),
                  tone: config.usable ? PillTone.online : PillTone.warning,
                ),
              ],
            ),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('设备授权', style: theme.textTheme.labelLarge),
                const SizedBox(width: NeilicoTokens.spaceSm),
                StatusPill(
                  label: policies.isAvailable ? '已同步' : '待后端就绪',
                  tone: policies.isAvailable ? PillTone.online : PillTone.neutral,
                ),
              ],
            ),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('内核进程', style: theme.textTheme.labelLarge),
                const SizedBox(width: NeilicoTokens.spaceSm),
                StatusPill(
                  label: controller.kernel.found ? '已安装' : '未安装',
                  tone: controller.kernel.found ? PillTone.online : PillTone.offline,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _DeviceGrid extends StatelessWidget {
  const _DeviceGrid({required this.devices, required this.controller});

  final List<RemoteDesktopDevice> devices;
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final columns = (constraints.maxWidth / NeilicoTokens.cardMinWidth).floor().clamp(1, 4);
        return GridView.builder(
          itemCount: devices.length,
          gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: columns,
            mainAxisSpacing: NeilicoTokens.spaceMd,
            crossAxisSpacing: NeilicoTokens.spaceMd,
            mainAxisExtent: 208,
          ),
          itemBuilder: (context, index) {
            final device = devices[index];
            return DeviceCard(
              device: device,
              panel: controller.panelFor(device),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute<void>(
                  builder: (_) => DeviceDetailScreen(
                    controller: controller,
                    nodeId: device.id,
                  ),
                ),
              ),
            );
          },
        );
      },
    );
  }
}

/// 通用内联提示条（用于错误 / 未就绪说明）。
class _InlineNote extends StatelessWidget {
  const _InlineNote({required this.icon, required this.text, required this.tone});

  final IconData icon;
  final String text;
  final Color tone;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(NeilicoTokens.spaceSm + 4),
      decoration: BoxDecoration(
        color: tone.withValues(alpha: 0.10),
        borderRadius: BorderRadius.circular(NeilicoTokens.radiusSm),
        border: Border.all(color: tone.withValues(alpha: 0.30)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 18, color: tone),
          const SizedBox(width: NeilicoTokens.spaceSm),
          Expanded(child: Text(text, style: theme.textTheme.bodySmall)),
        ],
      ),
    );
  }
}

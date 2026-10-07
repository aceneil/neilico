import 'package:flutter/material.dart';

import '../models/device.dart';
import '../models/device_switches.dart';
import '../theme/neilico_theme.dart';
import '../util/format.dart';
import 'status_pill.dart';

/// 设备网格里的一张卡片。只做展示 + 点击进入详情。
class DeviceCard extends StatelessWidget {
  const DeviceCard({
    super.key,
    required this.device,
    required this.panel,
    required this.onTap,
  });

  final RemoteDesktopDevice device;
  final DeviceSwitchPanel panel;
  final VoidCallback onTap;

  PillTone get _tone {
    if (device.heartbeatStale) return PillTone.warning;
    return switch (device.status) {
      DeviceStatus.online => PillTone.online,
      DeviceStatus.offline => PillTone.offline,
      DeviceStatus.unknown => PillTone.neutral,
    };
  }

  String get _statusLabel =>
      device.heartbeatStale ? '${device.status.label}（陈旧）' : device.status.label;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(NeilicoTokens.spaceMd),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      device.displayName,
                      style: theme.textTheme.titleMedium,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: NeilicoTokens.spaceSm),
                  StatusPill(label: _statusLabel, tone: _tone),
                ],
              ),
              const SizedBox(height: NeilicoTokens.spaceSm),
              Text(
                device.platformLabel,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: NeilicoTokens.spaceXs),
              _KeyValue(icon: Icons.lan_outlined, text: device.virtualIp ?? '未分配虚拟 IP'),
              _KeyValue(icon: Icons.favorite_border, text: formatRelative(device.lastSeen)),
              const Spacer(),
              const Divider(height: NeilicoTokens.spaceMd),
              _SwitchSummary(panel: panel),
            ],
          ),
        ),
      ),
    );
  }
}

class _KeyValue extends StatelessWidget {
  const _KeyValue({required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(top: NeilicoTokens.spaceXs),
      child: Row(
        children: [
          Icon(icon, size: 14, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: NeilicoTokens.spaceXs + 2),
          Expanded(
            child: Text(
              text,
              style: theme.textTheme.bodySmall,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

/// 卡片底部的开关摘要。后端未就绪时明确写「待后端就绪」，不显示假状态。
class _SwitchSummary extends StatelessWidget {
  const _SwitchSummary({required this.panel});

  final DeviceSwitchPanel panel;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (!panel.remoteControlAllowed.enabled) {
      return Row(
        children: [
          Icon(Icons.lock_outline, size: 14, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: NeilicoTokens.spaceXs + 2),
          Expanded(
            child: Text(
              panel.remoteControlAllowed.message,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      );
    }

    final remote = panel.remoteControlAllowed.value == true ? '可被远程' : '禁止远程';
    final tunnel = panel.tunnelMode.value?.label ?? '—';
    final mesh = panel.meshJoined.value == true ? 'Mesh 已加入' : 'Mesh 未加入';
    final isolated = panel.isolatedTunnel.value == true ? '单独隧道开' : '单独隧道关';
    return Text(
      '$remote · 隧道：$tunnel · $mesh · $isolated',
      style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
    );
  }
}

/// 供其它位置复用的只读「授权状态」行。
class PolicyLine extends StatelessWidget {
  const PolicyLine({super.key, required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(top: NeilicoTokens.spaceXs),
        child: Text('$label：$value'),
      );
}

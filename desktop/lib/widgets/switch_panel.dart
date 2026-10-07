import 'package:flutter/material.dart';

import '../models/device_policy.dart';
import '../models/device_switches.dart';
import '../theme/neilico_theme.dart';

/// 每台设备的开关面板。
///
/// 设计要点：**置灰必须说明原因**。后端授权接口未就绪、设备离线、
/// 缺虚拟 IP 等情况下，控件保持可见但不可操作，并在下方给出原因文案。
class SwitchPanel extends StatelessWidget {
  const SwitchPanel({
    super.key,
    required this.panel,
    this.onRemoteControlAllowed,
    this.onTunnelMode,
    this.onIsolatedTunnel,
    this.onMeshJoined,
  });

  final DeviceSwitchPanel panel;
  final ValueChanged<bool>? onRemoteControlAllowed;
  final ValueChanged<TunnelMode>? onTunnelMode;
  final ValueChanged<bool>? onIsolatedTunnel;
  final ValueChanged<bool>? onMeshJoined;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _BoolTile(
          tileKey: const Key('switch-remote-control'),
          title: '可被远程',
          description: '关闭后，任何客户端都不得对该设备发起远程控制',
          gate: panel.remoteControlAllowed,
          onChanged: onRemoteControlAllowed,
        ),
        const Divider(height: NeilicoTokens.spaceLg),
        _TunnelModeTile(
          gate: panel.tunnelMode,
          onChanged: onTunnelMode,
        ),
        const Divider(height: NeilicoTokens.spaceLg),
        _BoolTile(
          tileKey: const Key('switch-isolated-tunnel'),
          title: '单独隧道',
          description: '为该设备单独开一条隧道 / 端口转发，与全局网络隔离',
          gate: panel.isolatedTunnel,
          onChanged: onIsolatedTunnel,
        ),
        const Divider(height: NeilicoTokens.spaceLg),
        _BoolTile(
          tileKey: const Key('switch-mesh'),
          title: 'Mesh 加入',
          description: '加入 / 退出虚拟网络，参与 Mesh 直连',
          gate: panel.meshJoined,
          onChanged: onMeshJoined,
        ),
      ],
    );
  }
}

class _BoolTile extends StatelessWidget {
  const _BoolTile({
    required this.tileKey,
    required this.title,
    required this.description,
    required this.gate,
    required this.onChanged,
  });

  /// 挂在真正的开关控件上（便于测试与无障碍定位）。
  final Key tileKey;
  final String title;
  final String description;
  final Gated<bool> gate;
  final ValueChanged<bool>? onChanged;

  @override
  Widget build(BuildContext context) {
    final enabled = gate.enabled && onChanged != null;
    return SwitchListTile(
      key: tileKey,
      contentPadding: EdgeInsets.zero,
      value: gate.value ?? false,
      onChanged: enabled ? (value) => onChanged!(value) : null,
      title: Text(title),
      subtitle: _GateSubtitle(text: description, gate: gate, enabled: enabled),
    );
  }
}

class _TunnelModeTile extends StatelessWidget {
  const _TunnelModeTile({required this.gate, required this.onChanged});

  final Gated<TunnelMode> gate;
  final ValueChanged<TunnelMode>? onChanged;

  @override
  Widget build(BuildContext context) {
    final enabled = gate.enabled && onChanged != null;
    final theme = Theme.of(context);
    final selected = gate.value ?? TunnelMode.auto;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('隧道模式', style: theme.textTheme.titleSmall),
        const SizedBox(height: NeilicoTokens.spaceXs),
        Text(
          selected.description,
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: NeilicoTokens.spaceSm),
        Opacity(
          opacity: enabled ? 1 : 0.55,
          child: SegmentedButton<TunnelMode>(
            key: const Key('switch-tunnel-mode'),
            showSelectedIcon: false,
            segments: TunnelMode.values
                .map((mode) => ButtonSegment<TunnelMode>(
                      value: mode,
                      label: Text(mode.label),
                    ))
                .toList(growable: false),
            selected: <TunnelMode>{selected},
            onSelectionChanged: enabled
                ? (selection) {
                    if (selection.isNotEmpty) onChanged!(selection.first);
                  }
                : null,
          ),
        ),
        _GateSubtitle(text: '', gate: gate, enabled: enabled),
      ],
    );
  }
}

/// 可用时显示说明；不可用时显示「锁 + 原因」。
class _GateSubtitle extends StatelessWidget {
  const _GateSubtitle({required this.text, required this.gate, required this.enabled});

  final String text;
  final Gated<Object?> gate;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (enabled) {
      if (text.isEmpty) return const SizedBox.shrink();
      return Text(
        text,
        style: theme.textTheme.bodySmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.only(top: NeilicoTokens.spaceXs),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.lock_outline, size: 14, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: NeilicoTokens.spaceXs + 2),
          Expanded(
            child: Text(
              gate.message,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
                fontStyle: FontStyle.italic,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

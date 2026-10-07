import 'package:flutter/material.dart';

import '../theme/neilico_theme.dart';

/// 统一的信息行：左侧标签、右侧值，超长省略号。
class InfoRow extends StatelessWidget {
  const InfoRow({super.key, required this.label, required this.value, this.mono = false, this.trailing});

  final String label;
  final String value;
  final bool mono;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final style = theme.textTheme.bodyMedium?.copyWith(
      fontFamily: mono ? 'monospace' : null,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: NeilicoTokens.spaceXs + 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 108,
            child: Text(
              label,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: style,
              overflow: TextOverflow.ellipsis,
              maxLines: 3,
            ),
          ),
          ?trailing,
        ],
      ),
    );
  }
}

/// 空状态占位（无设备 / 加载失败等）。
class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    required this.icon,
    required this.title,
    this.description,
    this.action,
  });

  final IconData icon;
  final String title;
  final String? description;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 420),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: NeilicoTokens.spaceMd),
            Text(title, style: theme.textTheme.titleMedium, textAlign: TextAlign.center),
            if (description != null) ...[
              const SizedBox(height: NeilicoTokens.spaceSm),
              Text(
                description!,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                textAlign: TextAlign.center,
              ),
            ],
            if (action != null) ...[
              const SizedBox(height: NeilicoTokens.spaceLg),
              action!,
            ],
          ],
        ),
      ),
    );
  }
}

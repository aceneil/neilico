import 'package:flutter/material.dart';

import '../theme/neilico_theme.dart';

/// 状态标签的语义色调。
enum PillTone { online, offline, warning, danger, neutral }

/// 小圆点 + 文案的状态标签。颜色一律取令牌，不硬编码。
class StatusPill extends StatelessWidget {
  const StatusPill({super.key, required this.label, this.tone = PillTone.neutral});

  final String label;
  final PillTone tone;

  Color _color() => switch (tone) {
        PillTone.online => NeilicoTokens.online,
        PillTone.offline => NeilicoTokens.offline,
        PillTone.warning => NeilicoTokens.warning,
        PillTone.danger => NeilicoTokens.danger,
        PillTone.neutral => NeilicoTokens.offline,
      };

  @override
  Widget build(BuildContext context) {
    final color = _color();
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: NeilicoTokens.spaceSm + 2,
        vertical: NeilicoTokens.spaceXs,
      ),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(NeilicoTokens.radiusLg),
        border: Border.all(color: color.withValues(alpha: 0.35)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(color: color, shape: BoxShape.circle),
          ),
          const SizedBox(width: NeilicoTokens.spaceXs + 2),
          Text(
            label,
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
                  color: scheme.onSurface,
                  fontWeight: FontWeight.w600,
                ),
          ),
        ],
      ),
    );
  }
}

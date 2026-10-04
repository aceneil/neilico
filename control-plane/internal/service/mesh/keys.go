package mesh

import (
	"encoding/base64"
	"strings"
)

const wireGuardKeySize = 32

// NormalizeKey 把密钥规整成 WireGuard 解析器能接受的形式（32 字节的标准 base64）。
//
// 为什么要它：网络预共享密钥历史上是用 base64.RawURLEncoding 生成的（含 '-'/'_'、
// 无 '=' 填充），而 wg 只认标准 base64，于是 agent 应用配置时报
//
//	failed to parse base64-encoded key: illegal base64 data at input byte 20
//
// 结果**整个网络下所有节点**的配置都应用不上、mesh 起不来（实测）。
//
// 这里对新旧数据都做兼容：已经是标准 base64 的原样返回；否则尝试按 base64url /
// 无填充变体解码后重新编码。无法识别时原样返回（不在这一层吞掉问题）。
func NormalizeKey(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if raw, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(raw) == wireGuardKeySize {
		return trimmed
	}
	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
	} {
		if raw, err := encoding.DecodeString(trimmed); err == nil && len(raw) == wireGuardKeySize {
			return base64.StdEncoding.EncodeToString(raw)
		}
	}
	return trimmed
}

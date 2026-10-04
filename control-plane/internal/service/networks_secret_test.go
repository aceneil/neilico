package service

import (
	"encoding/base64"
	"strings"
	"testing"
)

// generateNetworkSecret 的结果会被当作 WireGuard 的 PresharedKey 下发，
// 所以必须是标准 base64（44 字符、带 '=' 填充、32 字节）。
// 历史缺陷：它用的是 base64.RawURLEncoding → 含 '-'/'_' → 全网络配置应用失败。
func TestGenerateNetworkSecretIsStandardBase64(t *testing.T) {
	secret, err := generateNetworkSecret()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(secret, "-_") {
		t.Fatalf("不得使用 base64url 字符（WireGuard 不接受）: %q", secret)
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("必须是标准 base64: %v（值 %q）", err, secret)
	}
	if len(raw) != 32 {
		t.Fatalf("应为 32 字节，实际 %d", len(raw))
	}
	if len(secret) != 44 || !strings.HasSuffix(secret, "=") {
		t.Fatalf("32 字节的标准 base64 应为 44 字符且带 '=' 填充，实际 %q", secret)
	}
}

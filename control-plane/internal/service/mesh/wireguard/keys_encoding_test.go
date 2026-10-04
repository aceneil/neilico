package wireguard

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"neilico/control-plane/internal/service/mesh"
)

// 渲染出的每个密钥都必须是**标准 base64 的 32 字节**——WireGuard 只接受这种形式。
//
// 历史缺陷：网络预共享密钥用 base64.RawURLEncoding 生成（含 '-'/'_'、无 '=' 填充），
// 渲染进配置后 agent 执行 `wg setconf` 直接失败：
//
//	failed to parse base64-encoded key: illegal base64 data at input byte 20
//
// 由于 PSK 对每个 peer 都写一遍，**该网络下所有节点**的配置都应用不上、mesh 起不来
// （实测：宿主 wg0 一直没有任何地址）。这条测试就是当时缺的那道闸。
func TestRenderedKeysAreStandardBase64(t *testing.T) {
	urlKey := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32))
	if !strings.ContainsAny(urlKey, "-_") {
		t.Fatalf("测试用例应当包含 base64url 字符，实际 %q", urlKey)
	}
	node := mesh.Node{
		Name:       "n",
		PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32)),
		PublicKey:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x02}, 32)),
		VirtualIP:  "100.64.0.2",
		Network: mesh.Network{
			PresharedKey: urlKey, // ← 历史坏数据形态
			Peers: []mesh.Peer{{
				PublicKey:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x03}, 32)),
				Endpoint:   "", // 对端尚未上报 endpoint
				AllowedIPs: []string{"100.64.0.3/32"},
			}},
		},
	}
	got, err := New().RenderNodeConfig(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(got)
	if strings.Contains(rendered, "Endpoint = \n") || strings.HasSuffix(strings.TrimSpace(rendered), "Endpoint =") {
		t.Fatal("对端没有 endpoint 时不应写出空的 Endpoint 行（会让解析器再报一个错）")
	}
	sawPSK := false
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(line)
		for _, field := range []string{"PrivateKey", "PublicKey", "PresharedKey"} {
			value, ok := strings.CutPrefix(line, field+" =")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			raw, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				t.Fatalf("%s 不是标准 base64（%v），长度 %d", field, err, len(value))
			}
			if len(raw) != 32 {
				t.Fatalf("%s 解码后 %d 字节，应为 32", field, len(raw))
			}
			if field == "PresharedKey" {
				sawPSK = true
				if value != base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32)) {
					t.Fatal("base64url 的 PSK 应被规整成同一密钥的标准 base64")
				}
			}
		}
	}
	if !sawPSK {
		t.Fatal("应当把网络的 PresharedKey 写进每个 peer")
	}
}

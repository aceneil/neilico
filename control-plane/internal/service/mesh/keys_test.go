package mesh

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0xfb}, 32)
	standard := base64.StdEncoding.EncodeToString(raw)
	urlForm := base64.RawURLEncoding.EncodeToString(raw)
	if !strings.ContainsAny(urlForm, "-_") {
		t.Fatalf("用例本身应含 base64url 字符: %q", urlForm)
	}
	cases := []struct{ name, in, want string }{
		{"标准 base64：原样返回", standard, standard},
		{"base64url（无填充）→ 标准", urlForm, standard},
		{"base64url（带填充）→ 标准", base64.URLEncoding.EncodeToString(raw), standard},
		{"标准无填充 → 标准带填充", base64.RawStdEncoding.EncodeToString(raw), standard},
		{"空串 → 空串", "", ""},
		{"无法识别：原样返回，不在这里吞掉问题", "not-a-key", "not-a-key"},
	}
	for _, c := range cases {
		if got := NormalizeKey(c.in); got != c.want {
			t.Errorf("%s: NormalizeKey(%q) = %q，期望 %q", c.name, c.in, got, c.want)
		}
	}
}

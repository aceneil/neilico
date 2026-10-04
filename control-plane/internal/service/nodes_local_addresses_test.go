package service

import (
	"reflect"
	"testing"
)

func TestNormalizeLocalAddresses(t *testing.T) {
	cases := []struct {
		name   string
		input  []string
		expect []string
	}{
		{"保留合规的内网 CIDR", []string{"192.168.50.10/24"}, []string{"192.168.50.10/24"}},
		{"裸 IP 原样保留", []string{"10.0.0.7"}, []string{"10.0.0.7"}},
		{"掩码规整为网段", []string{"192.168.50.10/24"}, []string{"192.168.50.10/24"}},
		{"丢弃回环与链路本地", []string{"127.0.0.1/8", "169.254.1.2/16", "192.168.1.5/24"}, []string{"192.168.1.5/24"}},
		{"丢弃垃圾数据", []string{"not-an-ip", "", "   "}, nil},
		{"去重", []string{"192.168.50.10/24", "192.168.50.10/24"}, []string{"192.168.50.10/24"}},
	}
	for _, testCase := range cases {
		got := normalizeLocalAddresses(testCase.input)
		if len(got) == 0 && len(testCase.expect) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, testCase.expect) {
			t.Errorf("%s: normalizeLocalAddresses(%v) = %v，期望 %v", testCase.name, testCase.input, got, testCase.expect)
		}
	}
	// 条数上限：防止超长请求体
	many := make([]string, 0, 20)
	for index := 0; index < 20; index++ {
		many = append(many, "10.0."+string(rune('0'+index/10))+string(rune('0'+index%10))+".1")
	}
	if got := normalizeLocalAddresses(many); len(got) > maxReportedLocalAddresses {
		t.Fatalf("最多保留 %d 条，实际 %d", maxReportedLocalAddresses, len(got))
	}
}

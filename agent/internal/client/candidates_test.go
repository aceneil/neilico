package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 心跳必须把候选地址带给控制面；老版本控制面不认这个字段也不能影响心跳。
func TestHeartbeatSendsCandidates(t *testing.T) {
	var received HeartbeatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode heartbeat body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"next_heartbeat_seconds":30}`))
	}))
	defer server.Close()
	api := New(server.URL, "token")
	candidates := []Candidate{
		{Kind: "lan", Addr: "192.168.1.5:51820"},
		{Kind: "stun", Addr: "203.0.113.9:51820"},
	}
	if _, err := api.HeartbeatWithCandidates(context.Background(), "node", "0.1.0", nil, nil, candidates); err != nil {
		t.Fatalf("HeartbeatWithCandidates() error = %v", err)
	}
	if len(received.Candidates) != 2 {
		t.Fatalf("心跳里的候选 = %+v，期望 2 条", received.Candidates)
	}
	if received.Candidates[1].Kind != "stun" || received.Candidates[1].Addr != "203.0.113.9:51820" {
		t.Fatalf("心跳里的候选内容不对：%+v", received.Candidates)
	}
}

// 没有候选时请求体里不能出现 candidates 字段（omitempty，向后兼容）。
func TestHeartbeatOmitsEmptyCandidates(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, 4096)
		n, _ := r.Body.Read(buffer)
		body = buffer[:n]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"next_heartbeat_seconds":30}`))
	}))
	defer server.Close()
	api := New(server.URL, "token")
	if _, err := api.HeartbeatWithReport(context.Background(), "node", "0.1.0", nil, nil); err != nil {
		t.Fatalf("HeartbeatWithReport() error = %v", err)
	}
	if bytesContains(body, "candidates") {
		t.Fatalf("没有候选时请求体里不应出现 candidates：%s", body)
	}
}

func bytesContains(data []byte, needle string) bool {
	return len(data) > 0 && string(data) != "" && (indexOf(string(data), needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

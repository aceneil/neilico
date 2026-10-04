package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"neilico/agent/internal/client"
)

// 回归护栏：配置哈希必须把"本地应用逻辑版本"算进去。
// 否则升级 agent 后（配置没变）会跳过应用，新增的本地动作（如对端路由）装不上。
func TestConfigHashIncludesApplicationSchemaVersion(t *testing.T) {
	delivery := client.Delivery{
		Version: 7,
		Node:    client.NodeIdentity{ID: "node-1", Name: "n", VirtualIP: "100.64.0.2"},
		Network: &client.Network{ID: "net-1", Name: "net", CIDR: "100.64.0.0/24", Peers: []client.Peer{{
			NodeID: "node-2", PublicKey: "B", Endpoint: "1.2.3.4:51820",
			AllowedIPs: []string{"100.64.0.3/32"},
		}}},
	}
	got, err := ConfigHash(delivery)
	if err != nil {
		t.Fatal(err)
	}
	// 手工按"含 schema 字段"的方式算一遍，钉住格式（将来若漏掉该字段，这个测试会红）
	comparable := delivery
	comparable.Version = 0
	encoded, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Delivery      client.Delivery `json:"delivery"`
	}{SchemaVersion: ApplicationSchemaVersion, Delivery: comparable})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("哈希必须包含 schema_version：got %s want %s", got, want)
	}
	// 若把 schema 版本换成其它值，哈希必须不同（证明它真的参与了计算）
	encodedDifferent, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Delivery      client.Delivery `json:"delivery"`
	}{SchemaVersion: ApplicationSchemaVersion + 1, Delivery: comparable})
	if err != nil {
		t.Fatal(err)
	}
	sumDifferent := sha256.Sum256(encodedDifferent)
	if hex.EncodeToString(sumDifferent[:]) == got {
		t.Fatal("schema 版本变化后哈希必须变化")
	}
}

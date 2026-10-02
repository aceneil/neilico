package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequiredMetricsAreExposed(t *testing.T) {
	m := New()
	m.Heartbeat("success")
	m.ConfigPull("not_modified")
	m.Apply("success")
	m.SetConfigVersion(4)
	m.SetPeers(2)
	m.SetDryRun(true)
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, name := range []string{
		"neilico_agent_heartbeat_total",
		"neilico_agent_config_version",
		"neilico_agent_config_pull_total",
		"neilico_agent_apply_total",
		"neilico_agent_apply_dry_run",
		"neilico_agent_peers",
	} {
		if !strings.Contains(text, name) {
			t.Fatalf("metrics omitted %s:\n%s", name, text)
		}
	}
}

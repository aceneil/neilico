package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"umpp/control-plane/internal/auth"
)

type m2bNetworkCreate struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	CIDR          string    `json:"cidr"`
	NetworkSecret string    `json:"network_secret"`
}

type m2bNodeRegistration struct {
	NodeID     uuid.UUID `json:"node_id"`
	AgentToken string    `json:"agent_token"`
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key"`
}

type m2bMember struct {
	NodeID    uuid.UUID `json:"node_id"`
	VirtualIP string    `json:"virtual_ip"`
}

type m2bPeer struct {
	NodeID     string   `json:"node_id"`
	PublicKey  string   `json:"public_key"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowed_ips"`
	VirtualIP  string   `json:"virtual_ip"`
}

type m2bAgentConfig struct {
	Version int `json:"version"`
	Node    struct {
		ID             uuid.UUID `json:"id"`
		Name           string    `json:"name"`
		VirtualIP      string    `json:"virtual_ip"`
		PublicEndpoint string    `json:"public_endpoint"`
	} `json:"node"`
	Network *struct {
		ID            string    `json:"id"`
		Name          string    `json:"name"`
		CIDR          string    `json:"cidr"`
		NetworkSecret string    `json:"network_secret"`
		Peers         []m2bPeer `json:"peers"`
	} `json:"network"`
	ProxyRules      []any  `json:"proxy_rules"`
	ACL             []any  `json:"acl"`
	Routes          []any  `json:"routes"`
	PolicyFiltered  bool   `json:"policy_filtered"`
	WireGuardConfig string `json:"wireguard_config"`
}

func TestM2BMeshConfigAndRollbackFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "home", "cidr": "100.64.10.0/24",
	})
	if status != http.StatusCreated {
		t.Fatalf("network create status = %d body = %s", status, body)
	}
	requireStatus(t, status, http.StatusCreated)
	var network m2bNetworkCreate
	decodeResponse(t, body, &network)
	if network.NetworkSecret == "" || network.CIDR != "100.64.10.0/24" {
		t.Fatalf("unexpected network creation: %#v", network)
	}

	nodeA := registerM2BNode(t, app, admin.Token, "node-a")
	nodeB := registerM2BNode(t, app, admin.Token, "node-b")
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+nodeA.NodeID.String(), nodeA.AgentToken, nil)
	requireStatus(t, status, http.StatusOK)
	var unjoinedConfig m2bAgentConfig
	decodeResponse(t, body, &unjoinedConfig)
	if unjoinedConfig.Network != nil {
		t.Fatalf("unjoined node config unexpectedly had a network: %#v", unjoinedConfig.Network)
	}
	memberA := addM2BMember(t, app, admin.Token, network.ID, nodeA.NodeID)
	memberB := addM2BMember(t, app, admin.Token, network.ID, nodeB.NodeID)
	if memberA.VirtualIP == memberB.VirtualIP {
		t.Fatalf("nodes received identical virtual IPs: %q", memberA.VirtualIP)
	}

	configA := getM2BConfig(t, app, nodeA.AgentToken, nodeA.NodeID.String(), 0)
	if configA.Network == nil || configA.Network.NetworkSecret != network.NetworkSecret {
		t.Fatalf("agent config did not carry the created network secret: %#v", configA.Network)
	}
	if !containsString(configA.Network.Peers[0].AllowedIPs, memberB.VirtualIP+"/32") {
		t.Fatalf("node A peer AllowedIPs = %#v, want node B virtual IP", configA.Network.Peers[0].AllowedIPs)
	}
	if !strings.Contains(configA.WireGuardConfig, "PrivateKey = "+nodeA.PrivateKey) ||
		!strings.Contains(configA.WireGuardConfig, "PublicKey = "+nodeB.PublicKey) {
		t.Fatalf("WireGuard config omitted local or peer keys:\n%s", configA.WireGuardConfig)
	}
	versionBeforeRoute := configA.Version

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": nodeB.NodeID, "cidr": "192.168.1.0/24", "enabled": true,
	})
	requireStatus(t, status, http.StatusCreated)
	configA = getM2BConfig(t, app, nodeA.AgentToken, nodeA.NodeID.String(), 0)
	if configA.Version != versionBeforeRoute+1 {
		t.Fatalf("route config version = %d, want %d", configA.Version, versionBeforeRoute+1)
	}
	var peerB m2bPeer
	for _, peer := range configA.Network.Peers {
		if peer.NodeID == nodeB.NodeID.String() {
			peerB = peer
		}
	}
	if !containsString(peerB.AllowedIPs, "192.168.1.0/24") {
		t.Fatalf("peer B AllowedIPs = %#v, want subnet route", peerB.AllowedIPs)
	}
	configBeforeACL := configA

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/acl", admin.Token, map[string]any{
		"src": "member:" + nodeA.NodeID.String(), "dst": "member:" + nodeB.NodeID.String(),
		"action": "deny", "protocol": "any", "ports": "any", "priority": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	configA = getM2BConfig(t, app, nodeA.AgentToken, nodeA.NodeID.String(), 0)
	for _, peer := range configA.Network.Peers {
		if peer.NodeID == nodeB.NodeID.String() {
			t.Fatalf("policy-filtered config retained denied peer: %#v", peer)
		}
	}
	if !configA.PolicyFiltered {
		t.Fatal("ACL-filtered config did not set policy_filtered")
	}

	notModified := directM2BRequest(t, app, http.MethodGet, "/api/v1/agent/config?node_id="+nodeA.NodeID.String()+"&version="+itoa(configA.Version), nodeA.AgentToken, nil)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("same-version status = %d, want 304", notModified.Code)
	}
	var modifiedResponse struct {
		NotModified bool `json:"not_modified"`
		Version     int  `json:"version"`
	}
	decodeResponse(t, notModified.Body.Bytes(), &modifiedResponse)
	if !modifiedResponse.NotModified || modifiedResponse.Version != configA.Version {
		t.Fatalf("unexpected 304 body: %s", notModified.Body.Bytes())
	}

	// A client lagging behind must be served the LATEST desired config so it
	// converges — never its own historical snapshot (that would make it apply a
	// stale config, store that version, and re-request it forever).
	lagging := getM2BConfig(t, app, nodeA.AgentToken, nodeA.NodeID.String(), configBeforeACL.Version)
	if lagging.Version != configA.Version {
		t.Fatalf("lagging version = %d, want latest %d", lagging.Version, configA.Version)
	}
	if len(lagging.Network.Peers) != len(configA.Network.Peers) || !lagging.PolicyFiltered {
		t.Fatalf("lagging config is not the latest desired state: %#v", lagging)
	}
	// Historical snapshots stay reachable via the version list + rollback APIs,
	// which the rollback assertions below exercise.

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/configs/node/"+nodeA.NodeID.String()+"/rollback", admin.Token, map[string]any{
		"version": configBeforeACL.Version,
	})
	requireStatus(t, status, http.StatusCreated)
	var rollback struct {
		Version int `json:"version"`
	}
	decodeResponse(t, body, &rollback)
	if rollback.Version != configA.Version+1 {
		t.Fatalf("rollback version = %d, want %d", rollback.Version, configA.Version+1)
	}
	rolledBack := getM2BConfig(t, app, nodeA.AgentToken, nodeA.NodeID.String(), 0)
	if !equalM2BConfig(configBeforeACL, rolledBack) {
		t.Fatalf("rollback config differs from source snapshot:\nbefore=%#v\nafter=%#v", configBeforeACL, rolledBack)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/configs?target_type=node&target_id="+nodeA.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var versions struct {
		Items []struct {
			Version int    `json:"version"`
			Reason  string `json:"reason"`
		} `json:"items"`
	}
	decodeResponse(t, body, &versions)
	if len(versions.Items) == 0 || versions.Items[0].Version != rollback.Version || !strings.Contains(versions.Items[0].Reason, "rollback") {
		t.Fatalf("unexpected config version list: %#v", versions)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+nodeA.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if bytes.Contains(body, []byte(nodeA.PrivateKey)) || bytes.Contains(body, []byte(network.NetworkSecret)) {
		t.Fatalf("node detail leaked a secret: %s", body)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/networks/"+network.ID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if bytes.Contains(body, []byte(network.NetworkSecret)) {
		t.Fatalf("network detail leaked its secret: %s", body)
	}

	// An unknown/ahead version is a client-state hint, not a resource id: the
	// server answers with the latest desired config so any client converges
	// (a 404 here would strand agents whose version is ahead, e.g. after a restore).
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+nodeA.NodeID.String()+"&version=999999", nodeA.AgentToken, nil)
	requireStatus(t, status, http.StatusOK)
	var aheadResponse struct {
		Version int `json:"version"`
	}
	decodeResponse(t, body, &aheadResponse)
	if aheadResponse.Version != rollback.Version {
		t.Fatalf("unknown-version request returned version %d, want latest %d", aheadResponse.Version, rollback.Version)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+nodeA.NodeID.String(), nodeB.AgentToken, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "invalid_agent_token")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "other-m2b", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant struct {
		ID uuid.UUID `json:"id"`
	}
	decodeResponse(t, body, &otherTenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID, "email": "other-m2b@example.test", "password": "other-m2b-password",
		"role": auth.RoleTenantAdmin, "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	otherAdmin := mustLogin(t, app, "other-m2b@example.test", "other-m2b-password")
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+nodeA.NodeID.String(), otherAdmin.Token, nil)
	requireStatus(t, status, http.StatusNotFound)
	requireErrorCode(t, body, "not_found")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/acl", admin.Token, map[string]any{
		"src": "member:" + nodeB.NodeID.String(), "dst": "member:" + nodeA.NodeID.String(),
		"action": "allow", "protocol": "any", "ports": "any", "priority": 2,
	})
	requireStatus(t, status, http.StatusCreated)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+nodeA.NodeID.String()+"/network-report", nodeA.AgentToken, map[string]any{
		"public_endpoint": "203.0.113.10:51820",
	})
	requireStatus(t, status, http.StatusOK)
	configB := getM2BConfig(t, app, nodeB.AgentToken, nodeB.NodeID.String(), 0)
	var endpointSeen bool
	for _, peer := range configB.Network.Peers {
		if peer.NodeID == nodeA.NodeID.String() && peer.Endpoint == "203.0.113.10:51820" {
			endpointSeen = true
		}
	}
	if !endpointSeen {
		t.Fatalf("network report was not reflected in peer endpoint: %#v", configB.Network.Peers)
	}

	proxyVersion := configB.Version
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/domains", admin.Token, map[string]any{
		"domain": "mesh-proxy.example.test", "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	var proxyDomain struct {
		ID uuid.UUID `json:"id"`
	}
	decodeResponse(t, body, &proxyDomain)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-rules", admin.Token, map[string]any{
		"domain_id": proxyDomain.ID, "path": "/", "target_type": "node",
		"target": nodeA.NodeID.String() + ":8080", "enabled": true,
		"access_control": map[string]any{
			"ip_whitelist": []string{}, "basic_auth": map[string]any{"enabled": false},
			"require_jwt": false,
		},
	})
	requireStatus(t, status, http.StatusCreated)
	configB = getM2BConfig(t, app, nodeB.AgentToken, nodeB.NodeID.String(), 0)
	if configB.Version != proxyVersion+1 || len(configB.ProxyRules) != 1 {
		t.Fatalf("proxy rule did not bump and appear in node config: %#v", configB)
	}

	keyVersion := configB.Version
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+nodeB.NodeID.String()+"/keys/rotate", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var rotated struct {
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}
	decodeResponse(t, body, &rotated)
	configB = getM2BConfig(t, app, nodeB.AgentToken, nodeB.NodeID.String(), 0)
	if rotated.PrivateKey == "" || configB.Version != keyVersion+1 ||
		!strings.Contains(configB.WireGuardConfig, "PrivateKey = "+rotated.PrivateKey) {
		t.Fatalf("key rotation was not returned once and versioned: %#v %#v", rotated, configB)
	}

	metricsBody := directM2BRequest(t, app, http.MethodGet, "/metrics", "", nil).Body.String()
	for _, name := range []string{"umpp_tunnel_up", "umpp_config_version", "umpp_acl_denied_total"} {
		if !strings.Contains(metricsBody, name) {
			t.Fatalf("metrics omitted %s:\n%s", name, metricsBody)
		}
	}
}

func registerM2BNode(t *testing.T, app testApp, token, name string) m2bNodeRegistration {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", token, map[string]any{
		"name": name, "os": "linux", "arch": "amd64", "version": "test", "tags": []string{},
	})
	requireStatus(t, status, http.StatusCreated)
	var node m2bNodeRegistration
	decodeResponse(t, body, &node)
	if node.NodeID == uuid.Nil || node.AgentToken == "" || node.PrivateKey == "" || node.PublicKey == "" {
		t.Fatalf("unexpected node registration: %#v", node)
	}
	return node
}

func addM2BMember(t *testing.T, app testApp, token string, networkID, nodeID uuid.UUID) m2bMember {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+networkID.String()+"/members", token, map[string]any{
		"node_id": nodeID,
	})
	requireStatus(t, status, http.StatusCreated)
	var member m2bMember
	decodeResponse(t, body, &member)
	return member
}

func getM2BConfig(t *testing.T, app testApp, token, nodeID string, version int) m2bAgentConfig {
	t.Helper()
	path := "/api/v1/agent/config?node_id=" + nodeID
	if version > 0 {
		path += "&version=" + itoa(version)
	}
	status, body := mustRequest(t, app.server, http.MethodGet, path, token, nil)
	requireStatus(t, status, http.StatusOK)
	var config m2bAgentConfig
	decodeResponse(t, body, &config)
	return config
}

func directM2BRequest(t *testing.T, app testApp, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	app.handler.ServeHTTP(recorder, request)
	return recorder
}

func equalM2BConfig(left, right m2bAgentConfig) bool {
	left.Version, right.Version = 0, 0
	left.WireGuardConfig, right.WireGuardConfig = "", ""
	if left.Network != nil {
		left.Network.NetworkSecret = ""
	}
	if right.Network != nil {
		right.Network.NetworkSecret = ""
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

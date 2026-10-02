package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/tabwriter"

	"umpp/cli/internal/api"
	"umpp/cli/internal/config"
)

type apiTokenItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	ExpiresAt   *string  `json:"expires_at"`
	LastUsedAt  *string  `json:"last_used_at"`
	RevokedAt   *string  `json:"revoked_at"`
}

type apiTokenList struct {
	Items []apiTokenItem `json:"items"`
	Total int64          `json:"total"`
}

type apiTokenCreateResponse struct {
	Token    string       `json:"token"`
	Notice   string       `json:"notice"`
	APIToken apiTokenItem `json:"api_token"`
}

type loginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		TenantID string `json:"tenant_id"`
	} `json:"user"`
}

type nodeItem struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	OS             string  `json:"os"`
	Arch           string  `json:"arch"`
	Version        string  `json:"version"`
	VirtualIP      *string `json:"virtual_ip"`
	PublicEndpoint *string `json:"public_endpoint"`
}

type nodeList struct {
	Items []nodeItem `json:"items"`
	Total int64      `json:"total"`
}

type networkItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

type networkList struct {
	Items []networkItem `json:"items"`
	Total int64         `json:"total"`
}

type registration struct {
	NodeID     string `json:"node_id"`
	AgentToken string `json:"agent_token"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

func (a *App) login(ctx context.Context, credentials *config.Credentials, path string, args []string) error {
	set := newFlagSet("login", a.Stderr)
	email := set.String("email", "", "account email")
	password := set.String("password", "", "account password (read from stdin when omitted)")
	apiToken := set.String("token", "", "API token (saved directly without password login)")
	caFile := set.String("ca-file", "", "CA certificate file for the control plane")
	clientCertFile := set.String("client-cert-file", "", "mTLS client certificate file")
	clientKeyFile := set.String("client-key-file", "", "mTLS client private key file")
	if err := set.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*apiToken) != "" {
		value := strings.TrimSpace(*apiToken)
		if !strings.HasPrefix(value, "umpp_") || len(value) < 12 {
			return fmt.Errorf("--token must be a UMPP API token")
		}
		credentials.AccessToken = value
		credentials.RefreshToken = ""
		credentials.UserEmail = ""
		credentials.CAFile = *caFile
		credentials.ClientCertFile = *clientCertFile
		credentials.ClientKeyFile = *clientKeyFile
		if err := config.Save(path, *credentials); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "API token credentials saved")
		return nil
	}
	if err := requireFlag(*email, "email"); err != nil {
		return err
	}
	value := *password
	if value == "" {
		fmt.Fprint(a.Stderr, "Password: ")
		reader := bufio.NewReader(a.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read password: %w", err)
		}
		value = strings.TrimRight(line, "\r\n")
	}
	if value == "" {
		return fmt.Errorf("password is required")
	}
	client, err := api.NewWithTLS(credentials.Server, "", api.TLSOptions{
		CAFile: *caFile, ClientCertFile: *clientCertFile, ClientKeyFile: *clientKeyFile,
	})
	if err != nil {
		return err
	}
	var response loginResponse
	if err := client.Do(ctx, "POST", "/api/v1/auth/login", map[string]string{"email": *email, "password": value}, &response); err != nil {
		return err
	}
	credentials.AccessToken = response.Token
	credentials.RefreshToken = response.RefreshToken
	credentials.UserEmail = response.User.Email
	credentials.CAFile = *caFile
	credentials.ClientCertFile = *clientCertFile
	credentials.ClientKeyFile = *clientKeyFile
	if err := config.Save(path, *credentials); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "logged in as %s\n", response.User.Email)
	return nil
}

func (a *App) token(ctx context.Context, client *api.Client, args []string) error {
	if len(args) == 0 {
		return errHelpText("token")
	}
	switch args[0] {
	case "list":
		set := newFlagSet("token list", a.Stderr)
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		var result apiTokenList
		if err := client.Do(ctx, "GET", "/api/v1/api-tokens", nil, &result); err != nil {
			return err
		}
		table := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "ID\tNAME\tTOKEN\tSCOPES\tEXPIRES\tLAST USED\tSTATUS")
		for _, item := range result.Items {
			status := "active"
			if item.RevokedAt != nil {
				status = "revoked"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, item.Name, item.TokenPrefix+"…", strings.Join(item.Scopes, ","),
				optionalTime(item.ExpiresAt), optionalTime(item.LastUsedAt), status)
		}
		return table.Flush()
	case "create":
		set := newFlagSet("token create", a.Stderr)
		name := set.String("name", "", "token name")
		scopes := set.String("scopes", "", "comma-separated scopes")
		expires := set.Int("expires-in-days", 0, "expiry in days (omit for no expiry)")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*name, "name"); err != nil {
			return err
		}
		scopeList := splitScopes(*scopes)
		if len(scopeList) == 0 {
			return fmt.Errorf("--scopes is required")
		}
		input := map[string]any{"name": *name, "scopes": scopeList}
		if *expires != 0 {
			if *expires < 1 {
				return fmt.Errorf("--expires-in-days must be positive")
			}
			input["expires_in_days"] = *expires
		}
		var response apiTokenCreateResponse
		if err := client.Do(ctx, "POST", "/api/v1/api-tokens", input, &response); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "token\t%s\n", response.Token)
		fmt.Fprintf(a.Stdout, "prefix\t%s\u2026\n", response.APIToken.TokenPrefix)
		fmt.Fprintln(a.Stdout, "notice\t此 token 只显示一次，请立即安全保存")
		return nil
	case "revoke":
		set := newFlagSet("token revoke", a.Stderr)
		id := set.String("id", "", "API token ID")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*id, "id"); err != nil {
			return err
		}
		var response struct {
			APIToken       apiTokenItem `json:"api_token"`
			AlreadyRevoked bool         `json:"already_revoked"`
		}
		if err := client.Do(ctx, "DELETE", "/api/v1/api-tokens/"+url.PathEscape(*id), nil, &response); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "revoked\t%s\t%s\u2026\n", response.APIToken.ID, response.APIToken.TokenPrefix)
		return nil
	default:
		return errHelpText("token")
	}
}

func optionalTime(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func splitScopes(value string) []string {
	result := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func (a *App) node(ctx context.Context, client *api.Client, credentials *config.Credentials, configPath string, args []string) error {
	if len(args) == 0 {
		return errHelpText("node")
	}
	switch args[0] {
	case "list":
		set := newFlagSet("node list", a.Stderr)
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		var result nodeList
		if err := client.Do(ctx, "GET", "/api/v1/nodes", nil, &result); err != nil {
			return err
		}
		table := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "ID\tNAME\tSTATUS\tOS/ARCH\tVERSION\tVIRTUAL IP")
		for _, item := range result.Items {
			virtualIP := ""
			if item.VirtualIP != nil {
				virtualIP = *item.VirtualIP
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s/%s\t%s\t%s\n", item.ID, item.Name, item.Status, item.OS, item.Arch, item.Version, virtualIP)
		}
		return table.Flush()
	case "register":
		set := newFlagSet("node register", a.Stderr)
		name := set.String("name", "", "node name")
		osName := set.String("os", runtime.GOOS, "operating system")
		arch := set.String("arch", runtime.GOARCH, "architecture")
		agentVersion := set.String("version", "dev", "agent version")
		tags := set.String("tags", "", "comma-separated tags")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*name, "name"); err != nil {
			return err
		}
		var response registration
		input := map[string]any{"name": *name, "os": *osName, "arch": *arch, "version": *agentVersion, "tags": splitTags(*tags)}
		if err := client.Do(ctx, "POST", "/api/v1/nodes/register", input, &response); err != nil {
			return err
		}
		credentials.NodeID = response.NodeID
		if err := config.Save(configPath, *credentials); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "registered node %s (%s)\nprivate_key=***\n", response.NodeID, *name)
		return nil
	case "mtls":
		set := newFlagSet("node mtls", a.Stderr)
		nodeRef := set.String("node", credentials.NodeID, "node ID or name")
		outDir := set.String("out-dir", ".", "output directory")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*nodeRef, "node"); err != nil {
			return err
		}
		nodeID, err := resolveNode(ctx, client, *nodeRef)
		if err != nil {
			return err
		}
		var issued struct {
			ClientCertPEM string `json:"client_cert_pem"`
			ClientKeyPEM  string `json:"client_key_pem"`
		}
		if err := client.Do(ctx, "POST", "/api/v1/nodes/"+url.PathEscape(nodeID)+"/mtls", nil, &issued); err != nil {
			return err
		}
		if err := os.MkdirAll(*outDir, 0o700); err != nil {
			return err
		}
		if err := writePrivateFile(filepath.Join(*outDir, "client.crt"), []byte(issued.ClientCertPEM), 0o600); err != nil {
			return err
		}
		if err := writePrivateFile(filepath.Join(*outDir, "client.key"), []byte(issued.ClientKeyPEM), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "wrote mTLS client certificate and private key to %s\n", *outDir)
		return nil
	case "trust-ca":
		set := newFlagSet("node trust-ca", a.Stderr)
		out := set.String("out", "ca.crt", "CA certificate output path")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		var result struct {
			CACertPEM string `json:"ca_cert_pem"`
		}
		if err := client.Do(ctx, "GET", "/api/v1/pki/ca", nil, &result); err != nil {
			return err
		}
		if err := writePrivateFile(*out, []byte(result.CACertPEM), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "wrote CA certificate to %s\n", *out)
		return nil
	default:
		return errHelpText("node")
	}
}

func (a *App) network(ctx context.Context, client *api.Client, credentials *config.Credentials, args []string) error {
	if len(args) == 0 {
		return errHelpText("network")
	}
	switch args[0] {
	case "create":
		set := newFlagSet("network create", a.Stderr)
		name := set.String("name", "", "network name")
		cidr := set.String("cidr", "100.64.0.0/24", "overlay CIDR")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*name, "name"); err != nil {
			return err
		}
		var created networkItem
		if err := client.Do(ctx, "POST", "/api/v1/networks", map[string]string{"name": *name, "cidr": *cidr}, &created); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "created network %s (%s)\n", created.ID, created.Name)
		return nil
	case "join":
		set := newFlagSet("network join", a.Stderr)
		networkRef := set.String("network", "", "network ID or name")
		nodeRef := set.String("node", credentials.NodeID, "node ID or name")
		role := set.String("role", "member", "network role")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if err := requireFlag(*networkRef, "network"); err != nil {
			return err
		}
		networkID, err := resolveNetwork(ctx, client, *networkRef)
		if err != nil {
			return err
		}
		nodeID, err := resolveNode(ctx, client, *nodeRef)
		if err != nil {
			return err
		}
		var member struct {
			NodeID    string `json:"node_id"`
			VirtualIP string `json:"virtual_ip"`
			Role      string `json:"role"`
		}
		path := "/api/v1/networks/" + url.PathEscape(networkID) + "/members"
		if err := client.Do(ctx, "POST", path, map[string]string{"node_id": nodeID, "role": *role}, &member); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "joined node %s to network %s as %s\n", member.NodeID, networkID, member.Role)
		return nil
	default:
		return errHelpText("network")
	}
}

func (a *App) domain(ctx context.Context, client *api.Client, args []string) error {
	if len(args) < 2 || args[0] != "add" {
		return errHelpText("domain")
	}
	set := newFlagSet("domain add", a.Stderr)
	domain := set.String("domain", "", "public domain")
	target := set.String("target", "", "target host:port")
	targetType := set.String("target-type", "", "internal_ip, virtual_ip, or node")
	path := set.String("path", "/", "proxy path")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if err := requireFlag(*domain, "domain"); err != nil {
		return err
	}
	if err := requireFlag(*target, "target"); err != nil {
		return err
	}
	kind := *targetType
	if kind == "" {
		kind = inferTargetType(*target)
	}
	var created struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	}
	if err := client.Do(ctx, "POST", "/api/v1/domains", map[string]string{"domain": *domain, "status": "pending"}, &created); err != nil {
		return err
	}
	rule := map[string]any{
		"domain_id":      created.ID,
		"path":           *path,
		"target_type":    kind,
		"target":         *target,
		"access_control": map[string]any{"ip_whitelist": []string{}, "basic_auth": false, "require_jwt": false},
		"enabled":        true,
	}
	var proxyRule struct {
		ID string `json:"id"`
	}
	if err := client.Do(ctx, "POST", "/api/v1/proxy-rules", rule, &proxyRule); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "added domain %s -> %s (%s)\n", created.Domain, *target, kind)
	return nil
}

func (a *App) status(ctx context.Context, client *api.Client) error {
	var health map[string]any
	if err := client.Do(ctx, "GET", "/healthz", nil, &health); err != nil {
		return err
	}
	var nodes nodeList
	if err := client.Do(ctx, "GET", "/api/v1/nodes", nil, &nodes); err != nil {
		return err
	}
	online := 0
	for _, item := range nodes.Items {
		if item.Status == "online" {
			online++
		}
	}
	fmt.Fprintf(a.Stdout, "status\t%s\nversion\t%v\nnodes\t%d total, %d online\n", health["status"], health["version"], nodes.Total, online)
	return nil
}

func (a *App) agent(ctx context.Context, client *api.Client, args []string) error {
	if len(args) < 2 || args[0] != "config" {
		return errHelpText("agent")
	}
	set := newFlagSet("agent config", a.Stderr)
	nodeRef := set.String("node", "", "node ID")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if err := requireFlag(*nodeRef, "node"); err != nil {
		return err
	}
	nodeID, err := resolveNode(ctx, client, *nodeRef)
	if err != nil {
		return err
	}
	var payload map[string]any
	path := "/api/v1/agent/config?node_id=" + url.QueryEscape(nodeID) + "&version=0"
	if err := client.Do(ctx, "GET", path, nil, &payload); err != nil {
		return err
	}
	redactPayload(payload)
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, string(encoded))
	return nil
}

func resolveNetwork(ctx context.Context, client *api.Client, reference string) (string, error) {
	if uuidPattern.MatchString(reference) {
		return reference, nil
	}
	var result networkList
	if err := client.Do(ctx, "GET", "/api/v1/networks", nil, &result); err != nil {
		return "", err
	}
	for _, item := range result.Items {
		if item.ID == reference || item.Name == reference {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("network %q not found", reference)
}

func resolveNode(ctx context.Context, client *api.Client, reference string) (string, error) {
	if uuidPattern.MatchString(reference) {
		return reference, nil
	}
	var result nodeList
	if err := client.Do(ctx, "GET", "/api/v1/nodes", nil, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(reference) == "" {
		if len(result.Items) == 1 {
			return result.Items[0].ID, nil
		}
		return "", fmt.Errorf("--node is required when the node is ambiguous")
	}
	for _, item := range result.Items {
		if item.ID == reference || item.Name == reference {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("node %q not found", reference)
}

func inferTargetType(target string) string {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return "internal_ip"
	}
	if _, err := uuidParse(host); err == nil {
		return "node"
	}
	return "internal_ip"
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

func uuidParse(value string) (string, error) {
	if !uuidPattern.MatchString(value) {
		return "", fmt.Errorf("not a UUID")
	}
	return value, nil
}

var privateValuePattern = regexp.MustCompile(`(?i)("?(?:private_key|network_secret|agent_token)"?\s*[:=]\s*)("?)[^"\n,}]+("?)`)

func redactPayload(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "private_key", "network_secret", "agent_token", "token", "refresh_token":
				typed[key] = "***"
			case "wireguard_config":
				if text, ok := child.(string); ok {
					typed[key] = regexp.MustCompile(`(?i)(PrivateKey\s*=\s*)\S+`).ReplaceAllString(text, `${1}***`)
				}
			default:
				redactPayload(child)
			}
		}
	case []any:
		for _, child := range typed {
			redactPayload(child)
		}
	}
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func errHelpText(name string) error {
	return fmt.Errorf("%s: missing or invalid subcommand/options; use umppctl --help", name)
}

var _ = os.ErrNotExist
var _ = filepath.Separator

func writePrivateFile(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".umppctl-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

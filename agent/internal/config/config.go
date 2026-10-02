package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err == nil {
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	}
	var seconds int64
	if err := node.Decode(&seconds); err != nil {
		return errors.New("duration must be a duration string or seconds")
	}
	*d = Duration(time.Duration(seconds) * time.Second)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

type Config struct {
	Server            string   `yaml:"server"`
	Token             string   `yaml:"token,omitempty"`
	Node              Node     `yaml:"node"`
	Mesh              Mesh     `yaml:"mesh"`
	Proxy             Proxy    `yaml:"proxy"`
	TLS               TLS      `yaml:"tls"`
	Metrics           Metrics  `yaml:"metrics"`
	Log               Log      `yaml:"log"`
	StatePath         string   `yaml:"state_path"`
	PollInterval      Duration `yaml:"poll_interval"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
}

type TLS struct {
	CAFile             string `yaml:"ca_file"`
	ClientCertFile     string `yaml:"client_cert_file"`
	ClientKeyFile      string `yaml:"client_key_file"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type Node struct {
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

type Mesh struct {
	Interface         string `yaml:"interface"`
	MTU               int    `yaml:"mtu"`
	ListenPort        int    `yaml:"listen_port"`
	PublicEndpoint    string `yaml:"public_endpoint,omitempty"`
	CleanupOnExit     bool   `yaml:"cleanup_on_exit"`
	AllowForwarding   bool   `yaml:"allow_forwarding"`
	ExternalInterface string `yaml:"external_interface,omitempty"`
}

type Proxy struct {
	Enabled   bool   `yaml:"enabled"`
	NPSServer string `yaml:"nps_server"`
}

type Metrics struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type Log struct {
	Level string `yaml:"level"`
}

func Default() Config {
	return Config{
		Server: "https://api.umpp.example.com",
		Node:   Node{Name: "umpp-node", Tags: []string{}},
		Mesh: Mesh{
			Interface: "wg0", MTU: 1420, ListenPort: 51820,
			CleanupOnExit: true, AllowForwarding: false,
		},
		Proxy:             Proxy{},
		Metrics:           Metrics{Enabled: true, Listen: "0.0.0.0:9100"},
		Log:               Log{Level: "info"},
		StatePath:         "/var/lib/umpp-agent/state.json",
		PollInterval:      Duration(30 * time.Second),
		HeartbeatInterval: Duration(30 * time.Second),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) error {
	stringsMap := map[string]*string{
		"UMPP_AGENT_SERVER":                  &cfg.Server,
		"UMPP_AGENT_TOKEN":                   &cfg.Token,
		"UMPP_AGENT_NODE_NAME":               &cfg.Node.Name,
		"UMPP_AGENT_MESH_INTERFACE":          &cfg.Mesh.Interface,
		"UMPP_AGENT_MESH_PUBLIC_ENDPOINT":    &cfg.Mesh.PublicEndpoint,
		"UMPP_AGENT_MESH_EXTERNAL_INTERFACE": &cfg.Mesh.ExternalInterface,
		"UMPP_AGENT_PROXY_NPS_SERVER":        &cfg.Proxy.NPSServer,
		"UMPP_AGENT_METRICS_LISTEN":          &cfg.Metrics.Listen,
		"UMPP_AGENT_LOG_LEVEL":               &cfg.Log.Level,
		"UMPP_AGENT_STATE":                   &cfg.StatePath,
		"UMPP_AGENT_TLS_CA_FILE":             &cfg.TLS.CAFile,
		"UMPP_AGENT_TLS_CLIENT_CERT_FILE":    &cfg.TLS.ClientCertFile,
		"UMPP_AGENT_TLS_CLIENT_KEY_FILE":     &cfg.TLS.ClientKeyFile,
		"UMPP_AGENT_TLS_SERVER_NAME":         &cfg.TLS.ServerName,
	}
	for name, target := range stringsMap {
		if value, ok := os.LookupEnv(name); ok {
			*target = value
		}
	}
	intsMap := map[string]*int{
		"UMPP_AGENT_MESH_MTU":         &cfg.Mesh.MTU,
		"UMPP_AGENT_MESH_LISTEN_PORT": &cfg.Mesh.ListenPort,
	}
	for name, target := range intsMap {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			*target = parsed
		}
	}
	boolsMap := map[string]*bool{
		"UMPP_AGENT_MESH_CLEANUP_ON_EXIT":     &cfg.Mesh.CleanupOnExit,
		"UMPP_AGENT_MESH_ALLOW_FORWARDING":    &cfg.Mesh.AllowForwarding,
		"UMPP_AGENT_PROXY_ENABLED":            &cfg.Proxy.Enabled,
		"UMPP_AGENT_METRICS_ENABLED":          &cfg.Metrics.Enabled,
		"UMPP_AGENT_TLS_INSECURE_SKIP_VERIFY": &cfg.TLS.InsecureSkipVerify,
	}
	for name, target := range boolsMap {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%s must be a boolean", name)
			}
			*target = parsed
		}
	}
	durationsMap := map[string]*Duration{
		"UMPP_AGENT_POLL_INTERVAL":      &cfg.PollInterval,
		"UMPP_AGENT_HEARTBEAT_INTERVAL": &cfg.HeartbeatInterval,
	}
	for name, target := range durationsMap {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("%s must be a duration", name)
			}
			*target = Duration(parsed)
		}
	}
	if value, ok := os.LookupEnv("UMPP_AGENT_NODE_TAGS"); ok {
		cfg.Node.Tags = splitTags(value)
	}
	return nil
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func (c Config) Validate() error {
	parsed, err := url.Parse(c.Server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("server must be an absolute URL")
	}
	if (strings.TrimSpace(c.TLS.ClientCertFile) == "") != (strings.TrimSpace(c.TLS.ClientKeyFile) == "") {
		return errors.New("tls.client_cert_file and tls.client_key_file must be set together")
	}
	if strings.TrimSpace(c.Node.Name) == "" {
		return errors.New("node.name is required")
	}
	if c.Mesh.Interface == "" || strings.ContainsAny(c.Mesh.Interface, " \t\n/") {
		return errors.New("mesh.interface is invalid")
	}
	if c.Mesh.MTU < 576 || c.Mesh.MTU > 65535 {
		return errors.New("mesh.mtu must be between 576 and 65535")
	}
	if c.Mesh.ListenPort < 1 || c.Mesh.ListenPort > 65535 {
		return errors.New("mesh.listen_port must be between 1 and 65535")
	}
	if time.Duration(c.PollInterval) < time.Second || time.Duration(c.HeartbeatInterval) < time.Second {
		return errors.New("poll_interval and heartbeat_interval must be at least 1s")
	}
	if c.StatePath == "" {
		return errors.New("state_path is required")
	}
	if c.Log.Level != "debug" && c.Log.Level != "info" && c.Log.Level != "warn" && c.Log.Level != "error" {
		return errors.New("log.level must be debug, info, warn, or error")
	}
	return nil
}

func DefaultStatePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "umpp-agent", "state.json")
	}
	return "/var/lib/umpp-agent/state.json"
}

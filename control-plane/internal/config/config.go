package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Server struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type Database struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type Auth struct {
	JWTSecret  string        `yaml:"jwt_secret"`
	AccessTTL  time.Duration `yaml:"access_ttl"`
	RefreshTTL time.Duration `yaml:"refresh_ttl"`
}

type Bootstrap struct {
	AdminEmail    string `yaml:"admin_email"`
	AdminPassword string `yaml:"admin_password"`
	DefaultTenant string `yaml:"default_tenant"`
}

type Node struct {
	HeartbeatTimeout time.Duration `yaml:"heartbeat_timeout"`
}

type ACME struct {
	Enabled         bool          `yaml:"enabled"`
	DirectoryURL    string        `yaml:"directory_url"`
	Email           string        `yaml:"email"`
	Challenge       string        `yaml:"challenge"`
	HTTPPort        int           `yaml:"http_port"`
	RenewBeforeDays int           `yaml:"renew_before_days"`
	CheckInterval   time.Duration `yaml:"check_interval"`
	KeyType         string        `yaml:"key_type"`
	AgreeTOS        bool          `yaml:"agree_tos"`
	CACertFile      string        `yaml:"ca_cert_file"`
	AutoRenew       bool          `yaml:"auto_renew"`
}

type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type Config struct {
	Server    Server    `yaml:"server"`
	Database  Database  `yaml:"database"`
	Auth      Auth      `yaml:"auth"`
	Bootstrap Bootstrap `yaml:"bootstrap"`
	Node      Node      `yaml:"node"`
	ACME      ACME      `yaml:"acme"`
	Proxy     Proxy     `yaml:"proxy"`
	Log       Log       `yaml:"log"`
}

func Default() Config {
	return Config{
		Server: Server{Host: "0.0.0.0", Port: 8080},
		Database: Database{
			Driver: "postgres",
			DSN:    "",
		},
		Auth: Auth{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 7 * 24 * time.Hour,
		},
		Bootstrap: Bootstrap{DefaultTenant: "default"},
		Node:      Node{HeartbeatTimeout: 60 * time.Second},
		ACME: ACME{
			DirectoryURL:    "https://acme-v02.api.letsencrypt.org/directory",
			Challenge:       "http-01",
			HTTPPort:        80,
			RenewBeforeDays: 30,
			CheckInterval:   6 * time.Hour,
			KeyType:         "ec256",
			AutoRenew:       true,
		},
		Proxy: Proxy{
			Enabled: true,
			Kind:    "builtin",
			Listen:  ":8081",
			TLS: ProxyTLS{
				Enabled:    false,
				Listen:     ":8443",
				MinVersion: "1.2",
			},
			NPS: ProxyNPS{
				ConfigPath:     "data/nps/config.json",
				BinaryPath:     "/usr/bin/nps",
				PIDFile:        "data/nps/nps.pid",
				ReloadStrategy: "signal",
			},
		},
		Log: Log{Level: "info", Format: "json"},
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
	if err := applyEnvironment(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnvironment(cfg *Config) error {
	stringOverrides := []struct {
		key string
		dst *string
	}{
		{"UMPP_SERVER_HOST", &cfg.Server.Host},
		{"UMPP_DATABASE_DRIVER", &cfg.Database.Driver},
		{"UMPP_DATABASE_DSN", &cfg.Database.DSN},
		{"UMPP_AUTH_JWT_SECRET", &cfg.Auth.JWTSecret},
		{"UMPP_BOOTSTRAP_ADMIN_EMAIL", &cfg.Bootstrap.AdminEmail},
		{"UMPP_BOOTSTRAP_ADMIN_PASSWORD", &cfg.Bootstrap.AdminPassword},
		{"UMPP_BOOTSTRAP_DEFAULT_TENANT", &cfg.Bootstrap.DefaultTenant},
		{"UMPP_LOG_LEVEL", &cfg.Log.Level},
		{"UMPP_LOG_FORMAT", &cfg.Log.Format},
		{"UMPP_ACME_DIRECTORY_URL", &cfg.ACME.DirectoryURL},
		{"UMPP_ACME_EMAIL", &cfg.ACME.Email},
		{"UMPP_ACME_CHALLENGE", &cfg.ACME.Challenge},
		{"UMPP_ACME_KEY_TYPE", &cfg.ACME.KeyType},
		{"UMPP_ACME_CA_CERT_FILE", &cfg.ACME.CACertFile},
		{"UMPP_PROXY_KIND", &cfg.Proxy.Kind},
		{"UMPP_PROXY_TLS_LISTEN", &cfg.Proxy.TLS.Listen},
		{"UMPP_PROXY_TLS_MIN_VERSION", &cfg.Proxy.TLS.MinVersion},
		{"UMPP_PROXY_LISTEN", &cfg.Proxy.Listen},
		{"UMPP_NPS_CONFIG_PATH", &cfg.Proxy.NPS.ConfigPath},
		{"UMPP_NPS_BINARY_PATH", &cfg.Proxy.NPS.BinaryPath},
		{"UMPP_NPS_PID_FILE", &cfg.Proxy.NPS.PIDFile},
		{"UMPP_NPS_RELOAD_STRATEGY", &cfg.Proxy.NPS.ReloadStrategy},
	}
	for _, item := range stringOverrides {
		if value, ok := os.LookupEnv(item.key); ok {
			*item.dst = value
		}
	}
	if value, ok := os.LookupEnv("UMPP_PROXY_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("UMPP_PROXY_ENABLED must be a boolean: %w", err)
		}
		cfg.Proxy.Enabled = enabled
	}
	for _, item := range []struct {
		key string
		dst *bool
	}{
		{"UMPP_ACME_ENABLED", &cfg.ACME.Enabled},
		{"UMPP_ACME_AGREE_TOS", &cfg.ACME.AgreeTOS},
		{"UMPP_ACME_AUTO_RENEW", &cfg.ACME.AutoRenew},
		{"UMPP_PROXY_TLS_ENABLED", &cfg.Proxy.TLS.Enabled},
	} {
		value, ok := os.LookupEnv(item.key)
		if !ok {
			continue
		}
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s must be a boolean: %w", item.key, err)
		}
		*item.dst = enabled
	}
	for _, item := range []struct {
		key string
		dst *int
	}{
		{"UMPP_SERVER_PORT", &cfg.Server.Port},
		{"UMPP_ACME_HTTP_PORT", &cfg.ACME.HTTPPort},
		{"UMPP_ACME_RENEW_BEFORE_DAYS", &cfg.ACME.RenewBeforeDays},
	} {
		value, ok := os.LookupEnv(item.key)
		if !ok {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", item.key, err)
		}
		*item.dst = number
	}
	durationOverrides := []struct {
		key string
		dst *time.Duration
	}{
		{"UMPP_AUTH_ACCESS_TTL", &cfg.Auth.AccessTTL},
		{"UMPP_AUTH_REFRESH_TTL", &cfg.Auth.RefreshTTL},
		{"UMPP_NODE_HEARTBEAT_TIMEOUT", &cfg.Node.HeartbeatTimeout},
		{"UMPP_ACME_CHECK_INTERVAL", &cfg.ACME.CheckInterval},
	}
	for _, item := range durationOverrides {
		value, ok := os.LookupEnv(item.key)
		if !ok {
			continue
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s must be a duration: %w", item.key, err)
		}
		*item.dst = duration
	}
	return nil
}

func (c Config) Validate() error {
	switch c.Database.Driver {
	case "postgres", "sqlite":
	default:
		return fmt.Errorf("database.driver must be postgres or sqlite")
	}
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if c.Auth.AccessTTL <= 0 || c.Auth.RefreshTTL <= 0 {
		return fmt.Errorf("auth token TTLs must be positive")
	}
	if c.Node.HeartbeatTimeout <= 0 {
		return fmt.Errorf("node.heartbeat_timeout must be positive")
	}
	if c.Log.Level != "debug" && c.Log.Level != "info" && c.Log.Level != "warn" && c.Log.Level != "error" {
		return fmt.Errorf("log.level must be debug, info, warn, or error")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return fmt.Errorf("log.format must be json or text")
	}
	if c.Proxy.Kind != "builtin" && c.Proxy.Kind != "nps" {
		return fmt.Errorf("proxy.kind must be builtin or nps")
	}
	if strings.TrimSpace(c.Proxy.Listen) == "" {
		return fmt.Errorf("proxy.listen is required")
	}
	directoryURL, err := url.Parse(c.ACME.DirectoryURL)
	if err != nil || directoryURL.Host == "" || (directoryURL.Scheme != "http" && directoryURL.Scheme != "https") {
		return fmt.Errorf("acme.directory_url must be an absolute http or https URL")
	}
	if c.ACME.Challenge != "http-01" && c.ACME.Challenge != "dns-01" {
		return fmt.Errorf("acme.challenge must be http-01 or dns-01")
	}
	if c.ACME.HTTPPort < 1 || c.ACME.HTTPPort > 65535 {
		return fmt.Errorf("acme.http_port must be between 1 and 65535")
	}
	if c.ACME.RenewBeforeDays < 0 {
		return fmt.Errorf("acme.renew_before_days must not be negative")
	}
	if c.ACME.CheckInterval <= 0 {
		return fmt.Errorf("acme.check_interval must be positive")
	}
	if c.ACME.KeyType != "ec256" && c.ACME.KeyType != "rsa2048" {
		return fmt.Errorf("acme.key_type must be ec256 or rsa2048")
	}
	if strings.TrimSpace(c.Proxy.TLS.Listen) == "" {
		return fmt.Errorf("proxy.tls.listen is required")
	}
	if c.Proxy.TLS.MinVersion != "1.2" && c.Proxy.TLS.MinVersion != "1.3" {
		return fmt.Errorf("proxy.tls.min_version must be 1.2 or 1.3")
	}
	if strings.TrimSpace(c.Proxy.NPS.ConfigPath) == "" {
		return fmt.Errorf("proxy.nps.config_path is required")
	}
	if c.Proxy.NPS.ReloadStrategy != "signal" && c.Proxy.NPS.ReloadStrategy != "file" {
		return fmt.Errorf("proxy.nps.reload_strategy must be signal or file")
	}
	return nil
}

type ProxyTLS struct {
	Enabled    bool   `yaml:"enabled"`
	Listen     string `yaml:"listen"`
	MinVersion string `yaml:"min_version"`
}

type ProxyNPS struct {
	ConfigPath     string `yaml:"config_path"`
	BinaryPath     string `yaml:"binary_path"`
	PIDFile        string `yaml:"pid_file"`
	ReloadStrategy string `yaml:"reload_strategy"`
}

type Proxy struct {
	Enabled bool     `yaml:"enabled"`
	Kind    string   `yaml:"kind"`
	Listen  string   `yaml:"listen"`
	TLS     ProxyTLS `yaml:"tls"`
	NPS     ProxyNPS `yaml:"nps"`
}

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
	Host string    `yaml:"host"`
	Port int       `yaml:"port"`
	TLS  ServerTLS `yaml:"tls"`
}

type ServerTLS struct {
	Enabled      bool   `yaml:"enabled"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	ClientCAFile string `yaml:"client_ca_file"`
	MinVersion   string `yaml:"min_version"`
	RedirectHTTP bool   `yaml:"redirect_http"`
	ClientAuth   string `yaml:"client_auth"`
	HTTPPort     int    `yaml:"http_port"`
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

type Alerts struct {
	EvaluationInterval    time.Duration `yaml:"evaluation_interval"`
	NodeOfflineAfter      time.Duration `yaml:"node_offline_after"`
	CertificateExpiringIn time.Duration `yaml:"certificate_expiring_in"`
	CertificateCriticalIn time.Duration `yaml:"certificate_critical_in"`
	P2PSuccessRateMinimum float64       `yaml:"p2p_success_rate_minimum"`
	RelaySpikeMultiplier  float64       `yaml:"relay_spike_multiplier"`
	RelayBaselineWindow   time.Duration `yaml:"relay_baseline_window"`
	ResolvedRetention     time.Duration `yaml:"resolved_retention"`
	WebhookURL            string        `yaml:"webhook_url"`
	WebhookTimeout        time.Duration `yaml:"webhook_timeout"`
	WebhookRetries        int           `yaml:"webhook_retries"`
}

type RateLimit struct {
	Enabled bool    `yaml:"enabled"`
	RPS     float64 `yaml:"rps"`
	Burst   int     `yaml:"burst"`
}

type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type PKI struct {
	Enabled         bool     `yaml:"enabled"`
	CACertName      string   `yaml:"ca_common_name"`
	ServerHosts     []string `yaml:"server_hosts"`
	ServerCertDays  int      `yaml:"server_cert_days"`
	NodeCertDays    int      `yaml:"node_cert_days"`
	RenewBeforeDays int      `yaml:"renew_before_days"`
}

type Config struct {
	Server    Server    `yaml:"server"`
	PKI       PKI       `yaml:"pki"`
	Database  Database  `yaml:"database"`
	Auth      Auth      `yaml:"auth"`
	Bootstrap Bootstrap `yaml:"bootstrap"`
	Node      Node      `yaml:"node"`
	ACME      ACME      `yaml:"acme"`
	Proxy     Proxy     `yaml:"proxy"`
	Alerts    Alerts    `yaml:"alerts"`
	RateLimit RateLimit `yaml:"ratelimit"`
	Log       Log       `yaml:"log"`
}

func Default() Config {
	return Config{
		Server: Server{
			Host: "0.0.0.0",
			Port: 8080,
			TLS: ServerTLS{
				Enabled:      false,
				MinVersion:   "1.2",
				RedirectHTTP: true,
				ClientAuth:   "none",
				HTTPPort:     80,
			},
		},
		PKI: PKI{
			Enabled:         false,
			CACertName:      "UMPP Internal CA",
			ServerCertDays:  825,
			NodeCertDays:    365,
			RenewBeforeDays: 30,
		},
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
				Enabled:      false,
				Listen:       ":8443",
				MinVersion:   "1.2",
				RedirectHTTP: true,
				HSTSMaxAge:   31536000,
			},
			NPS: ProxyNPS{
				ConfigPath:     "data/nps/config.json",
				BinaryPath:     "/usr/bin/nps",
				PIDFile:        "data/nps/nps.pid",
				ReloadStrategy: "signal",
				Crypt:          true,
				Compress:       true,
			},
		},
		Alerts: Alerts{
			EvaluationInterval:    time.Minute,
			NodeOfflineAfter:      5 * time.Minute,
			CertificateExpiringIn: 30 * 24 * time.Hour,
			CertificateCriticalIn: 7 * 24 * time.Hour,
			P2PSuccessRateMinimum: 0.60,
			RelaySpikeMultiplier:  3,
			RelayBaselineWindow:   24 * time.Hour,
			ResolvedRetention:     7 * 24 * time.Hour,
			WebhookTimeout:        5 * time.Second,
			WebhookRetries:        3,
		},
		RateLimit: RateLimit{Enabled: true, RPS: 20, Burst: 40},
		Log:       Log{Level: "info", Format: "json"},
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
		{"UMPP_SERVER_TLS_CERT_FILE", &cfg.Server.TLS.CertFile},
		{"UMPP_SERVER_TLS_KEY_FILE", &cfg.Server.TLS.KeyFile},
		{"UMPP_SERVER_TLS_CLIENT_CA_FILE", &cfg.Server.TLS.ClientCAFile},
		{"UMPP_SERVER_TLS_MIN_VERSION", &cfg.Server.TLS.MinVersion},
		{"UMPP_SERVER_TLS_CLIENT_AUTH", &cfg.Server.TLS.ClientAuth},
		{"UMPP_PKI_CA_COMMON_NAME", &cfg.PKI.CACertName},
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
		{"UMPP_ALERTS_WEBHOOK_URL", &cfg.Alerts.WebhookURL},
	}
	if value, ok := os.LookupEnv("UMPP_PKI_SERVER_HOSTS"); ok {
		cfg.PKI.ServerHosts = nil
		for _, host := range strings.Split(value, ",") {
			if host = strings.TrimSpace(host); host != "" {
				cfg.PKI.ServerHosts = append(cfg.PKI.ServerHosts, host)
			}
		}
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
		{"UMPP_RATELIMIT_ENABLED", &cfg.RateLimit.Enabled},
		{"UMPP_ACME_ENABLED", &cfg.ACME.Enabled},
		{"UMPP_ACME_AGREE_TOS", &cfg.ACME.AgreeTOS},
		{"UMPP_ACME_AUTO_RENEW", &cfg.ACME.AutoRenew},
		{"UMPP_SERVER_TLS_ENABLED", &cfg.Server.TLS.Enabled},
		{"UMPP_SERVER_TLS_REDIRECT_HTTP", &cfg.Server.TLS.RedirectHTTP},
		{"UMPP_PKI_ENABLED", &cfg.PKI.Enabled},
		{"UMPP_PROXY_TLS_ENABLED", &cfg.Proxy.TLS.Enabled},
		{"UMPP_PROXY_TLS_REDIRECT_HTTP", &cfg.Proxy.TLS.RedirectHTTP},
		{"UMPP_PROXY_NPS_CRYPT", &cfg.Proxy.NPS.Crypt},
		{"UMPP_PROXY_NPS_COMPRESS", &cfg.Proxy.NPS.Compress},
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
		{"UMPP_SERVER_TLS_HTTP_PORT", &cfg.Server.TLS.HTTPPort},
		{"UMPP_RATELIMIT_BURST", &cfg.RateLimit.Burst},
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
		{"UMPP_ALERTS_EVALUATION_INTERVAL", &cfg.Alerts.EvaluationInterval},
		{"UMPP_ALERTS_NODE_OFFLINE_AFTER", &cfg.Alerts.NodeOfflineAfter},
		{"UMPP_ALERTS_CERTIFICATE_EXPIRING_IN", &cfg.Alerts.CertificateExpiringIn},
		{"UMPP_ALERTS_CERTIFICATE_CRITICAL_IN", &cfg.Alerts.CertificateCriticalIn},
		{"UMPP_ALERTS_RELAY_BASELINE_WINDOW", &cfg.Alerts.RelayBaselineWindow},
		{"UMPP_ALERTS_RESOLVED_RETENTION", &cfg.Alerts.ResolvedRetention},
		{"UMPP_ALERTS_WEBHOOK_TIMEOUT", &cfg.Alerts.WebhookTimeout},
	}
	intOverrides := []struct {
		key string
		dst *int
	}{
		{"UMPP_ALERTS_WEBHOOK_RETRIES", &cfg.Alerts.WebhookRetries},
		{"UMPP_PKI_SERVER_CERT_DAYS", &cfg.PKI.ServerCertDays},
		{"UMPP_PKI_NODE_CERT_DAYS", &cfg.PKI.NodeCertDays},
		{"UMPP_PKI_RENEW_BEFORE_DAYS", &cfg.PKI.RenewBeforeDays},
		{"UMPP_PROXY_TLS_HSTS_MAX_AGE", &cfg.Proxy.TLS.HSTSMaxAge},
	}
	for _, item := range intOverrides {
		value, ok := os.LookupEnv(item.key)
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", item.key, err)
		}
		*item.dst = parsed
	}
	floatOverrides := []struct {
		key string
		dst *float64
	}{
		{"UMPP_RATELIMIT_RPS", &cfg.RateLimit.RPS},
		{"UMPP_ALERTS_P2P_SUCCESS_RATE_MINIMUM", &cfg.Alerts.P2PSuccessRateMinimum},
		{"UMPP_ALERTS_RELAY_SPIKE_MULTIPLIER", &cfg.Alerts.RelaySpikeMultiplier},
	}
	for _, item := range floatOverrides {
		value, ok := os.LookupEnv(item.key)
		if !ok {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number: %w", item.key, err)
		}
		*item.dst = parsed
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
	if c.Server.TLS.HTTPPort < 1 || c.Server.TLS.HTTPPort > 65535 {
		return fmt.Errorf("server.tls.http_port must be between 1 and 65535")
	}
	if c.Server.TLS.MinVersion != "1.2" && c.Server.TLS.MinVersion != "1.3" {
		return fmt.Errorf("server.tls.min_version must be 1.2 or 1.3")
	}
	if c.Server.TLS.ClientAuth != "none" && c.Server.TLS.ClientAuth != "request" && c.Server.TLS.ClientAuth != "require" {
		return fmt.Errorf("server.tls.client_auth must be none, request, or require")
	}
	if c.Server.TLS.Enabled && (strings.TrimSpace(c.Server.TLS.CertFile) == "") != (strings.TrimSpace(c.Server.TLS.KeyFile) == "") {
		return fmt.Errorf("server.tls.cert_file and server.tls.key_file must be set together")
	}
	if c.PKI.ServerCertDays < 1 || c.PKI.NodeCertDays < 1 || c.PKI.RenewBeforeDays < 0 {
		return fmt.Errorf("pki certificate day values must be positive and renew_before_days must not be negative")
	}
	if c.Auth.AccessTTL <= 0 || c.Auth.RefreshTTL <= 0 {
		return fmt.Errorf("auth token TTLs must be positive")
	}
	if c.Node.HeartbeatTimeout <= 0 {
		return fmt.Errorf("node.heartbeat_timeout must be positive")
	}
	if c.RateLimit.RPS <= 0 {
		return fmt.Errorf("ratelimit.rps must be positive")
	}
	if c.RateLimit.Burst < 1 {
		return fmt.Errorf("ratelimit.burst must be positive")
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
	if c.Proxy.TLS.HSTSMaxAge < 0 {
		return fmt.Errorf("proxy.tls.hsts_max_age must not be negative")
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
	Enabled      bool   `yaml:"enabled"`
	Listen       string `yaml:"listen"`
	MinVersion   string `yaml:"min_version"`
	RedirectHTTP bool   `yaml:"redirect_http"`
	HSTSMaxAge   int    `yaml:"hsts_max_age"`
}

type ProxyNPS struct {
	ConfigPath     string `yaml:"config_path"`
	BinaryPath     string `yaml:"binary_path"`
	PIDFile        string `yaml:"pid_file"`
	ReloadStrategy string `yaml:"reload_strategy"`
	Crypt          bool   `yaml:"crypt"`
	Compress       bool   `yaml:"compress"`
}

type Proxy struct {
	Enabled bool     `yaml:"enabled"`
	Kind    string   `yaml:"kind"`
	Listen  string   `yaml:"listen"`
	TLS     ProxyTLS `yaml:"tls"`
	NPS     ProxyNPS `yaml:"nps"`
}

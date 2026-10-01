package config

import (
	"fmt"
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
		{"UMPP_DATABASE_DRIVER", &cfg.Database.Driver},
		{"UMPP_DATABASE_DSN", &cfg.Database.DSN},
		{"UMPP_AUTH_JWT_SECRET", &cfg.Auth.JWTSecret},
		{"UMPP_BOOTSTRAP_ADMIN_EMAIL", &cfg.Bootstrap.AdminEmail},
		{"UMPP_BOOTSTRAP_ADMIN_PASSWORD", &cfg.Bootstrap.AdminPassword},
		{"UMPP_BOOTSTRAP_DEFAULT_TENANT", &cfg.Bootstrap.DefaultTenant},
		{"UMPP_LOG_LEVEL", &cfg.Log.Level},
		{"UMPP_LOG_FORMAT", &cfg.Log.Format},
	}
	for _, item := range stringOverrides {
		if value, ok := os.LookupEnv(item.key); ok {
			*item.dst = value
		}
	}
	if value, ok := os.LookupEnv("UMPP_SERVER_PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("UMPP_SERVER_PORT must be an integer: %w", err)
		}
		cfg.Server.Port = port
	}
	durationOverrides := []struct {
		key string
		dst *time.Duration
	}{
		{"UMPP_AUTH_ACCESS_TTL", &cfg.Auth.AccessTTL},
		{"UMPP_AUTH_REFRESH_TTL", &cfg.Auth.RefreshTTL},
		{"UMPP_NODE_HEARTBEAT_TIMEOUT", &cfg.Node.HeartbeatTimeout},
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
	return nil
}

package nps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	umppproxy "umpp/control-plane/internal/service/proxy"
)

type Options struct {
	ConfigPath     string
	BinaryPath     string
	PIDFile        string
	ReloadStrategy string
}

type Provider struct {
	db       *gorm.DB
	options  Options
	logger   *slog.Logger
	observer umppproxy.Observer
	mu       sync.RWMutex
	state    umppproxy.State
}

func New(db *gorm.DB, options Options, logger *slog.Logger, observer umppproxy.Observer) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if options.ConfigPath == "" {
		options.ConfigPath = "data/nps/config.json"
	}
	if options.ReloadStrategy == "" {
		options.ReloadStrategy = "file"
	}
	return &Provider{
		db:       db,
		options:  options,
		logger:   logger,
		observer: observer,
		state:    umppproxy.State{Kind: "nps", Status: "unknown", UpdatedAt: time.Now().UTC()},
	}
}

func (p *Provider) Kind() string { return "nps" }

func (p *Provider) Render(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	routes, err := loadRoutes(ctx, p.db, tenantID)
	if err != nil {
		return nil, err
	}
	return renderConfig(routes, p.options.ReloadStrategy)
}

func (p *Provider) Reload(ctx context.Context) error {
	data, err := p.Render(ctx, uuid.Nil)
	if err != nil {
		p.setStatus("degraded", err)
		return err
	}
	if err := writeAtomic(p.options.ConfigPath, data); err != nil {
		p.setStatus("degraded", err)
		return err
	}
	if _, err := os.Stat(p.options.BinaryPath); err != nil {
		err = fmt.Errorf("nps binary %q is unavailable: %w", p.options.BinaryPath, err)
		p.logger.Warn("nps reload skipped because external binary is unavailable", "error", err, "config_path", p.options.ConfigPath)
		p.setStatus("degraded", err)
		return nil
	}
	switch p.options.ReloadStrategy {
	case "signal":
		if err := p.signalReload(); err != nil {
			p.setStatus("degraded", err)
			return nil
		}
	case "file":
		// The external nps process watches config.json and reloads it without a signal.
	default:
		err := fmt.Errorf("unsupported NPS reload strategy %q", p.options.ReloadStrategy)
		p.setStatus("degraded", err)
		return nil
	}
	p.setStatus("up", nil)
	return nil
}

func (p *Provider) signalReload() error {
	raw, err := os.ReadFile(p.options.PIDFile)
	if err != nil {
		return fmt.Errorf("read nps pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid nps pid %q", strings.TrimSpace(string(raw)))
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find nps process: %w", err)
	}
	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("signal nps process: %w", err)
	}
	return nil
}

func (p *Provider) State() umppproxy.State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *Provider) setStatus(status string, err error) {
	state := umppproxy.State{Kind: "nps", Status: status, UpdatedAt: time.Now().UTC()}
	if err != nil {
		state.LastError = err.Error()
	}
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	if p.observer != nil {
		p.observer.SetProxyProviderUp("nps", status == "up")
	}
}

type npsConfig struct {
	SchemaVersion int         `json:"schema_version"`
	Server        npsServer   `json:"server"`
	Clients       []npsClient `json:"clients"`
}

type npsServer struct {
	BindPort       int    `json:"bind_port"`
	HTTPProxyPort  int    `json:"http_proxy_port"`
	ReloadStrategy string `json:"reload_strategy"`
}

type npsClient struct {
	TenantID string      `json:"tenant_id"`
	Tunnels  []npsTunnel `json:"tunnels"`
}

type npsTunnel struct {
	Mode          string               `json:"mode"`
	Port          int                  `json:"port"`
	TargetAddr    string               `json:"target_addr"`
	Host          string               `json:"host"`
	Path          string               `json:"path"`
	RuleID        string               `json:"rule_id"`
	DomainID      string               `json:"domain_id"`
	Enabled       bool                 `json:"enabled"`
	AccessControl models.AccessControl `json:"access_control"`
	Certificate   *npsCertificate      `json:"certificate,omitempty"`
}

type npsCertificate struct {
	ID        string `json:"id"`
	CertPEM   string `json:"cert_pem"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func loadRoutes(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) ([]umppproxy.Route, error) {
	set, err := umppproxy.LoadRoutes(ctx, db)
	if err != nil {
		return nil, err
	}
	if tenantID == uuid.Nil {
		return set.Routes, nil
	}
	filtered := make([]umppproxy.Route, 0, len(set.Routes))
	for _, route := range set.Routes {
		if route.TenantID == tenantID {
			filtered = append(filtered, route)
		}
	}
	return filtered, nil
}

func renderConfig(routes []umppproxy.Route, reloadStrategy string) ([]byte, error) {
	config := npsConfig{
		SchemaVersion: 1,
		Server:        npsServer{BindPort: 8024, HTTPProxyPort: 8081, ReloadStrategy: "file"},
		Clients:       []npsClient{},
	}
	clients := make(map[string]*npsClient)
	order := make([]string, 0)
	for _, route := range routes {
		key := route.TenantID.String()
		client, ok := clients[key]
		if !ok {
			client = &npsClient{TenantID: key, Tunnels: []npsTunnel{}}
			clients[key] = client
			order = append(order, key)
		}
		tunnel := npsTunnel{
			Mode:          "httpProxy",
			Port:          80,
			TargetAddr:    route.Target,
			Host:          route.Host,
			Path:          route.Path,
			RuleID:        route.RuleID.String(),
			DomainID:      route.DomainID.String(),
			Enabled:       true,
			AccessControl: route.AccessControl,
		}
		if route.Certificate != nil {
			tunnel.Port = 443
			tunnel.Certificate = &npsCertificate{ID: route.Certificate.ID, CertPEM: route.Certificate.CertPEM, ExpiresAt: route.Certificate.ExpiresAt}
		}
		client.Tunnels = append(client.Tunnels, tunnel)
	}
	for _, key := range order {
		config.Clients = append(config.Clients, *clients[key])
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create nps config directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("create nps temporary config: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o640); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write nps temporary config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("install nps config: %w", err)
	}
	return nil
}

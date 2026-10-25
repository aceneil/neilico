package relay

import (
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	// EnvEnabled 控制中继是否启用（默认关闭）。
	EnvEnabled = "NEILICO_RELAY_ENABLED"
	// EnvListen 是中继的 UDP 监听地址（默认 :51821）。
	EnvListen = "NEILICO_RELAY_LISTEN"
	// EnvPublicHost 是下发给 agent 的公网可达地址（host 或 host:port）。
	EnvPublicHost = "NEILICO_RELAY_PUBLIC_HOST"

	// DefaultListen 是中继的默认监听地址。
	DefaultListen = ":51821"
)

// Options 是中继服务端的运行参数（来自环境变量）。
type Options struct {
	// Enabled 是否启用中继。默认 false——不开时行为与上线前完全一致。
	Enabled bool
	// Listen 是 UDP 监听地址（默认 :51821）。
	Listen string
	// PublicHost 是下发给 agent 的公网可达 host（不含端口则用监听端口）。
	PublicHost string
}

// OptionsFromEnv 从环境变量读取中继配置。
func OptionsFromEnv() Options {
	return Options{
		Enabled:    envBool(EnvEnabled),
		Listen:     envOr(EnvListen, DefaultListen),
		PublicHost: strings.TrimSpace(os.Getenv(EnvPublicHost)),
	}
}

// PublicEndpoint 返回下发给 agent 的中继地址（host:port）。未配置公网 host 时返回空。
func (o Options) PublicEndpoint() string {
	host := strings.TrimSpace(o.PublicHost)
	if host == "" {
		return ""
	}
	// 允许直接写成 host:port；只写 host 时补上监听端口。
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, o.Port())
}

// Port 返回中继的监听端口（字符串形式）。
func (o Options) Port() string {
	listen := strings.TrimSpace(o.Listen)
	if listen == "" {
		listen = DefaultListen
	}
	if _, port, err := net.SplitHostPort(listen); err == nil && port != "" {
		return port
	}
	if _, err := strconv.Atoi(listen); err == nil {
		return listen
	}
	return "51821"
}

// Validate 检查中继配置是否可以启动。
func (o Options) Validate() error {
	if !o.Enabled {
		return nil
	}
	if strings.TrimSpace(o.PublicHost) == "" {
		return &ValidationError{Field: EnvPublicHost, Message: "启用中继时必须配置公网可达的地址"}
	}
	return nil
}

// ValidationError 表示中继配置不合法。
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

func envBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

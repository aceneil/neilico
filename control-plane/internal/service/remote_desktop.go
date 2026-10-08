package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

// RemoteDesktopOptions 是「远程桌面」模块的启动配置（来自 YAML / NEILICO_RD_* 环境变量）。
type RemoteDesktopOptions struct {
	Enabled       bool
	IDServer      string
	RelayServer   string
	PublicKeyFile string
	Ports         []int
	// KeyDir 是自托管 hbbs/hbbr 的密钥目录；当 PublicKeyFile 留空时，公钥路径由它派生
	// （<KeyDir>/id_ed25519.pub）。控制面**只读公钥**。
	KeyDir string
}

// RemoteDesktopConfig 是下发给客户端的服务器参数视图。
//
// 安全约定：只包含**公钥**（public_key），绝不含私钥或任何令牌。
type RemoteDesktopConfig struct {
	Enabled     bool   `json:"enabled"`
	IDServer    string `json:"id_server"`
	RelayServer string `json:"relay_server"`
	PublicKey   string `json:"public_key"`
	// Available 表示服务器参数是否就绪（公钥文件可读且非空）。
	Available bool   `json:"available"`
	Hint      string `json:"hint"`
	Ports     []int  `json:"ports"`
}

// RemoteDesktopUpdateInput 是管理员可修改的字段。
// 用指针区分「未提供该字段」与「显式置零/置空」。
type RemoteDesktopUpdateInput struct {
	Enabled     *bool   `json:"enabled"`
	IDServer    *string `json:"id_server"`
	RelayServer *string `json:"relay_server"`
}

// RemoteDesktopDevice 是「远程桌面」页面的设备卡片视图，基于现有 nodes 派生。
type RemoteDesktopDevice struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	VirtualIP        *string    `json:"virtual_ip"`
	LastSeen         *time.Time `json:"last_seen"`
	HeartbeatStale   bool       `json:"heartbeat_stale"`
	Platform         string     `json:"platform"`
	OS               string     `json:"os"`
	Arch             string     `json:"arch"`
	RustdeskID       string     `json:"rustdesk_id"`
	RustdeskHint     string     `json:"rustdesk_hint"`
	ConnectURL       string     `json:"connect_url"`
	ConnectionParams string     `json:"connection_params"`
}

type RemoteDesktopDeviceList struct {
	Items []RemoteDesktopDevice `json:"items"`
	Total int64                 `json:"total"`
}

// RemoteDesktopPortStatus 是单个端口的可达性探测结果。
type RemoteDesktopPortStatus struct {
	Port      int    `json:"port"`
	Target    string `json:"target"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// RemoteDesktopStatus 是「服务器参数」探活结果（纯 TCP 拨号，1s 超时，总预算 3s）。
type RemoteDesktopStatus struct {
	IDServerHost    string                    `json:"id_server_host"`
	RelayServerHost string                    `json:"relay_server_host"`
	Ports           []RemoteDesktopPortStatus `json:"ports"`
	Reachable       bool                      `json:"reachable"`
	CheckedAt       time.Time                 `json:"checked_at"`
}

// RustDeskHintPrefix 是节点用 tag 形式上报自身 RustDesk ID 的约定前缀
// （例如 tag "rustdesk:123456789"）。在客户端原生上报该字段之前，这是唯一的数据来源。
const RustDeskHintPrefix = "rustdesk:"

// probePorts 是需要探活的标准端口：21115/21116 属 hbbs（ID 服务器），21117 属 hbbr（中继）。
var probePorts = []int{21115, 21116, 21117}

// RemoteDesktopService 保存远程桌面服务器参数，并允许管理员在进程内覆盖。
//
// 覆盖值只保存在内存中（控制面单进程）：进程重启后回落到 YAML / 环境变量提供的值。
// 这是刻意的取舍——避免为少量可调参数引入新表与迁移；长期参数请用 NEILICO_RD_* 固化。
type RemoteDesktopService struct {
	mu            sync.RWMutex
	enabled       bool
	idServer      string
	relayServer   string
	publicKeyFile string
	keyDir        string
	ports         []int
}

func NewRemoteDesktopService(opts RemoteDesktopOptions) *RemoteDesktopService {
	service := &RemoteDesktopService{}
	service.reset(opts)
	return service
}

func (s *RemoteDesktopService) reset(opts RemoteDesktopOptions) {
	idServer := strings.TrimSpace(opts.IDServer)
	relayServer := strings.TrimSpace(opts.RelayServer)
	if relayServer == "" {
		relayServer = idServer
	}
	s.enabled = opts.Enabled
	s.idServer = idServer
	s.relayServer = relayServer
	s.publicKeyFile = strings.TrimSpace(opts.PublicKeyFile)
	s.keyDir = strings.TrimSpace(opts.KeyDir)
	s.ports = normalizeRemoteDesktopPorts(opts.Ports)
}

// publicKeyFilePath 返回实际读取公钥的路径：显式配置优先，否则由自托管密钥目录派生。
func (s *RemoteDesktopService) publicKeyFilePath() string {
	if s.publicKeyFile != "" {
		return s.publicKeyFile
	}
	if s.keyDir != "" {
		return filepath.Join(s.keyDir, "id_ed25519.pub")
	}
	return ""
}

func normalizeRemoteDesktopPorts(ports []int) []int {
	if len(ports) == 0 {
		return append([]int(nil), defaultRemoteDesktopPorts...)
	}
	result := make([]int, 0, len(ports))
	for _, port := range ports {
		if port >= 1 && port <= 65535 {
			result = append(result, port)
		}
	}
	if len(result) == 0 {
		return append([]int(nil), defaultRemoteDesktopPorts...)
	}
	return result
}

// defaultRemoteDesktopPorts 与 config 层保持一致（避免跨包依赖，这里独立声明一份）。
var defaultRemoteDesktopPorts = []int{21115, 21116, 21117, 21118, 21119}

// Config 解析当前生效的服务器参数。公钥文件缺失/为空时返回 available=false + hint，不报错。
func (s *RemoteDesktopService) Config(ctx context.Context) RemoteDesktopConfig {
	_ = ctx
	s.mu.RLock()
	enabled := s.enabled
	idServer := s.idServer
	relayServer := s.relayServer
	publicKeyFile := s.publicKeyFilePath()
	ports := append([]int(nil), s.ports...)
	s.mu.RUnlock()

	publicKey, err := readPublicKeyFile(publicKeyFile)
	keyReady := err == nil && publicKey != ""
	serverReady := strings.TrimSpace(idServer) != ""

	cfg := RemoteDesktopConfig{
		Enabled:     enabled,
		IDServer:    idServer,
		RelayServer: relayServer,
		PublicKey:   publicKey,
		Available:   keyReady && serverReady,
		Ports:       ports,
	}
	switch {
	case !serverReady:
		// 未配置服务器地址：不报错，只如实给出原因。
		cfg.PublicKey = ""
		cfg.Hint = "服务器未就绪：未配置服务器地址。请用环境变量 NEILICO_RD_ID_SERVER / NEILICO_RD_RELAY_SERVER（或 YAML remote_desktop.id_server / relay_server）配置自建 rustdesk-server 的地址。"
	case !keyReady:
		// 公钥未就绪（P1 尚未完成部署）：不报错，只如实给出原因。
		cfg.PublicKey = ""
		cfg.Hint = fmt.Sprintf("服务器未就绪：未能读取公钥文件（%s）。请先完成 rustdesk-server 部署，或用环境变量 NEILICO_RD_PUBLIC_KEY_FILE 指向正确的公钥。", publicKeyFile)
	default:
		cfg.Hint = fmt.Sprintf("服务器已就绪：在 RustDesk 客户端「ID/中继服务器」填入 %s，并把上方公钥填入「Key」。", idServer)
	}
	return cfg
}

// Update 应用管理员提交的参数改动（幂等）。返回的校验错误为 ErrInvalidInput。
func (s *RemoteDesktopService) Update(ctx context.Context, input RemoteDesktopUpdateInput) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.IDServer != nil {
		value := strings.TrimSpace(*input.IDServer)
		if err := validateRemoteDesktopServer(value); err != nil {
			return fmt.Errorf("%w: id_server %v", ErrInvalidInput, err)
		}
		s.idServer = value
	}
	if input.RelayServer != nil {
		value := strings.TrimSpace(*input.RelayServer)
		if err := validateRemoteDesktopServer(value); err != nil {
			return fmt.Errorf("%w: relay_server %v", ErrInvalidInput, err)
		}
		s.relayServer = value
	}
	if input.Enabled != nil {
		s.enabled = *input.Enabled
	}
	return nil
}

func validateRemoteDesktopServer(value string) error {
	if value == "" {
		return errors.New("must not be empty")
	}
	if len(value) > 255 {
		return errors.New("must not exceed 255 characters")
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return errors.New("must not contain whitespace")
	}
	return nil
}

// readPublicKeyFile 只读取公钥文件本身（id_ed25519.pub），绝不读取同名私钥。
func readPublicKeyFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("public key file is not configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// BuildRemoteDesktopDevices 由现有节点派生设备列表（复用 effective_mesh/last_seen/heartbeat_stale）。
func BuildRemoteDesktopDevices(nodes []models.Node, cfg RemoteDesktopConfig) RemoteDesktopDeviceList {
	items := make([]RemoteDesktopDevice, 0, len(nodes))
	for _, node := range nodes {
		rustdeskID := rustdeskIDFromTags(node.Tags)
		device := RemoteDesktopDevice{
			ID:             node.ID,
			Name:           node.Name,
			Status:         node.Status,
			VirtualIP:      node.VirtualIP,
			LastSeen:       node.LastSeen,
			HeartbeatStale: node.HeartbeatStale,
			OS:             node.OS,
			Arch:           node.Arch,
			Platform:       platformLabel(node.OS, node.Arch),
			RustdeskID:     rustdeskID,
			ConnectURL:     connectURL(rustdeskID),
			RustdeskHint:   rustdeskID,
		}
		device.ConnectionParams = connectionParams(device, cfg)
		items = append(items, device)
	}
	return RemoteDesktopDeviceList{Items: items, Total: int64(len(items))}
}

func rustdeskIDFromTags(tags []string) string {
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if len(trimmed) <= len(RustDeskHintPrefix) {
			continue
		}
		if strings.EqualFold(trimmed[:len(RustDeskHintPrefix)], RustDeskHintPrefix) {
			return strings.TrimSpace(trimmed[len(RustDeskHintPrefix):])
		}
	}
	return ""
}

func connectURL(rustdeskID string) string {
	if rustdeskID == "" {
		return ""
	}
	return "rustdesk://" + rustdeskID
}

func platformLabel(osName, arch string) string {
	osName = strings.TrimSpace(osName)
	arch = strings.TrimSpace(arch)
	switch {
	case osName == "" && arch == "":
		return "未知"
	case arch == "":
		return osName
	case osName == "":
		return arch
	default:
		return osName + "/" + arch
	}
}

func connectionParams(device RemoteDesktopDevice, cfg RemoteDesktopConfig) string {
	virtualIP := "（未分配）"
	if device.VirtualIP != nil && strings.TrimSpace(*device.VirtualIP) != "" {
		virtualIP = strings.TrimSpace(*device.VirtualIP)
	}
	rustdeskID := device.RustdeskID
	if rustdeskID == "" {
		rustdeskID = "（未上报，需该设备安装 RustDesk 并告知 ID）"
	}
	publicKey := cfg.PublicKey
	if publicKey == "" {
		publicKey = "（密钥未就绪）"
	}
	lines := []string{
		"设备: " + device.Name,
		"虚拟 IP: " + virtualIP,
		"RustDesk ID: " + rustdeskID,
		"ID 服务器: " + cfg.IDServer,
		"中继服务器: " + cfg.RelayServer,
		"Key: " + publicKey,
	}
	return strings.Join(lines, "\n")
}

// Probe 对 ID/中继服务器的标准端口做 TCP 拨号探活。
// 单个端口超时 1s；各端口并发拨号，整体不会阻塞超过 ~1s（硬上限 3s）。
func (s *RemoteDesktopService) Probe(ctx context.Context) RemoteDesktopStatus {
	s.mu.RLock()
	idServer := s.idServer
	relayServer := s.relayServer
	s.mu.RUnlock()

	idHost := hostOnly(idServer)
	relayHost := hostOnly(relayServer)

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	targets := make([]struct {
		port int
		host string
	}, len(probePorts))
	for index, port := range probePorts {
		host := idHost
		if port == 21117 {
			host = relayHost
		}
		targets[index] = struct {
			port int
			host string
		}{port: port, host: host}
	}

	results := make([]RemoteDesktopPortStatus, len(targets))
	var wait sync.WaitGroup
	for index := range targets {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index] = probeTCP(probeCtx, targets[index].host, targets[index].port)
		}(index)
	}
	wait.Wait()

	reachable := false
	for _, result := range results {
		if result.Reachable {
			reachable = true
		}
	}
	return RemoteDesktopStatus{
		IDServerHost:    idHost,
		RelayServerHost: relayHost,
		Ports:           results,
		Reachable:       reachable,
		CheckedAt:       time.Now().UTC(),
	}
}

func probeTCP(ctx context.Context, host string, port int) RemoteDesktopPortStatus {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	status := RemoteDesktopPortStatus{Port: port, Target: target}
	if strings.TrimSpace(host) == "" {
		status.Error = "服务器地址未配置"
		return status
	}
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	_ = connection.Close()
	status.Reachable = true
	return status
}

// hostOnly 从 host 或 host:port 中取出主机名。
func hostOnly(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

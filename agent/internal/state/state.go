package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type State struct {
	NodeID             string    `json:"node_id"`
	AgentToken         string    `json:"agent_token"`
	PrivateKey         string    `json:"private_key"`
	PublicKey          string    `json:"public_key,omitempty"`
	VirtualIP          string    `json:"virtual_ip,omitempty"`
	NetworkID          string    `json:"network_id,omitempty"`
	Server             string    `json:"server,omitempty"`
	AppliedVersion     int       `json:"applied_version"`
	AppliedConfigHash  string    `json:"applied_config_hash,omitempty"`
	NetworkSecret      string    `json:"network_secret,omitempty"`
	LastEndpoint       string    `json:"last_endpoint,omitempty"`
	LastEndpointReport time.Time `json:"last_endpoint_report,omitempty"`
	// LastLocalAddresses 是上次上报的内网地址列表：地址一变就立刻重报，
	// 不必等 5 分钟节流窗口（换网络/换网段后能马上恢复同内网直连）。
	LastLocalAddresses []string `json:"last_local_addresses,omitempty"`
	// ApplicationSchema 是上次成功应用时 agent 的"本地应用逻辑版本"。
	// agent 升级后该值会与二进制里的常量不一致 → 必须重新拉取并应用配置，
	// 否则升级带来的本地动作（例如新增对端路由）永远装不上（真机踩到）。
	ApplicationSchema int `json:"application_schema,omitempty"`
	// AppliedPeers 是上次成功应用时各对端的期望配置（公钥 + AllowedIPs，仅公开信息）。
	//
	// 为什么留在 state 里：容器被 recreate 后 netns 会被清空（wg0 与对端路由凭空消失），
	// 而 applied_version / applied_config_hash 都原封不动。要让 agent 在**不联网**的情况下
	// 也能自证"本地实物还在不在"，就必须把"期望的本地状态"落盘，供启动时的漂移校对比对
	// （见 mesh.SystemProbe / Reconciler.LocalDrift）。
	AppliedPeers []AppliedPeer `json:"applied_peers,omitempty"`
}

// AppliedPeer 是一次成功应用时记录的单个对端期望配置，仅含公开字段，
// 不含任何密钥（公钥本身是公开的）。
type AppliedPeer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

func Load(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("read state: %w", err)
	}
	var stored State
	if err := json.Unmarshal(data, &stored); err != nil {
		return State{}, false, fmt.Errorf("decode state: %w", err)
	}
	if stored.NodeID == "" || stored.AgentToken == "" {
		return State{}, false, errors.New("state is missing node_id or agent_token")
	}
	return stored, true, nil
}

func Save(path string, stored State) error {
	if stored.NodeID == "" || stored.AgentToken == "" {
		return errors.New("state requires node_id and agent_token")
	}
	directory := filepath.Dir(path)
	_, directoryErr := os.Stat(directory)
	directoryCreated := errors.Is(directoryErr, os.ErrNotExist)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if directoryCreated || filepath.Base(directory) == "neilico-agent" {
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure state directory: %w", err)
		}
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempName := file.Name()
	defer os.Remove(tempName)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("secure temporary state: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func TokenPrefix(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return token + "***"
	}
	return token[:4] + "***"
}

func RedactSecret(value string) string {
	if value == "" {
		return ""
	}
	return "***"
}

func Sanitize(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, RedactSecret(secret))
		}
	}
	return text
}

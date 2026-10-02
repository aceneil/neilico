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
	PublicKey          string    `json:"public_key"`
	AppliedVersion     int       `json:"applied_version"`
	AppliedConfigHash  string    `json:"applied_config_hash,omitempty"`
	NetworkSecret      string    `json:"network_secret,omitempty"`
	LastEndpoint       string    `json:"last_endpoint,omitempty"`
	LastEndpointReport time.Time `json:"last_endpoint_report,omitempty"`
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

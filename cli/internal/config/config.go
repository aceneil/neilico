package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Credentials struct {
	Server         string `yaml:"server"`
	AccessToken    string `yaml:"access_token,omitempty"`
	RefreshToken   string `yaml:"refresh_token,omitempty"`
	UserEmail      string `yaml:"user_email,omitempty"`
	NodeID         string `yaml:"node_id,omitempty"`
	CAFile         string `yaml:"ca_file,omitempty"`
	ClientCertFile string `yaml:"client_cert_file,omitempty"`
	ClientKeyFile  string `yaml:"client_key_file,omitempty"`
}

func DefaultPath() (string, error) {
	if value := os.Getenv("UMPPCTL_CONFIG"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".umppctl", "config.yaml"), nil
}

func Load(path, serverOverride string) (Credentials, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Credentials{}, err
		}
	}
	var credentials Credentials
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, &credentials); err != nil {
			return Credentials{}, fmt.Errorf("decode umppctl config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Credentials{}, fmt.Errorf("read umppctl config: %w", err)
	}
	if serverOverride != "" {
		credentials.Server = serverOverride
	} else if value := os.Getenv("UMPP_SERVER"); value != "" {
		credentials.Server = value
	}
	return credentials, nil
}

func Save(path string, credentials Credentials) error {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(credentials.Server) == "" {
		return errors.New("server is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := yaml.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("encode umppctl config: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace umppctl config: %w", err)
	}
	return os.Chmod(path, 0o600)
}

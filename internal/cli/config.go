package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// AuthTypePAT is a sipgate personal access token (HTTP Basic).
	AuthTypePAT = "pat"
	// AuthTypeOAuth is an OAuth 2.0 access token (HTTP Bearer).
	AuthTypeOAuth = "oauth2"
)

// Config is the persisted CLI configuration.
type Config struct {
	BaseURL string      `yaml:"base_url,omitempty"`
	Auth    AuthConfig  `yaml:"auth,omitempty"`
	OAuth   OAuthConfig `yaml:"oauth,omitempty"`
}

// AuthConfig stores the local authentication state.
type AuthConfig struct {
	Type         string     `yaml:"type,omitempty"`
	TokenID      string     `yaml:"token_id,omitempty"`
	Token        string     `yaml:"token,omitempty"`
	AccessToken  string     `yaml:"access_token,omitempty"`
	RefreshToken string     `yaml:"refresh_token,omitempty"`
	IDToken      string     `yaml:"id_token,omitempty"`
	Expiry       *time.Time `yaml:"expiry,omitempty"`
}

// OAuthConfig stores the OAuth 2.0 client registration.
type OAuthConfig struct {
	ClientID     string   `yaml:"client_id,omitempty"`
	ClientSecret string   `yaml:"client_secret,omitempty"`
	AuthURL      string   `yaml:"auth_url,omitempty"`
	TokenURL     string   `yaml:"token_url,omitempty"`
	RedirectURL  string   `yaml:"redirect_uri,omitempty"`
	Scopes       []string `yaml:"scopes,omitempty"`
}

// LoadConfig reads a configuration file. A missing file is not an error and
// yields an empty configuration.
func LoadConfig(path string) (*Config, error) {
	path = expandHome(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// SaveConfig writes the configuration with mode 0600. Parent directories are
// created with mode 0700.
func SaveConfig(path string, cfg *Config) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path must not be empty")
	}
	path = expandHome(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	content := append([]byte("# sipgate CLI configuration. Contains secrets, keep it private.\n"), data...)

	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	return nil
}

// DefaultConfigPath returns the per-user configuration path.
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determine user config directory: %w", err)
	}
	return filepath.Join(dir, "sipgate", "config.yaml"), nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

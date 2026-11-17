package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	expiry := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC)
	cfg := &Config{
		BaseURL: "https://api.example.test/v2",
		Auth: AuthConfig{
			Type:         AuthTypeOAuth,
			AccessToken:  "access",
			RefreshToken: "refresh",
			IDToken:      "id",
			Expiry:       &expiry,
		},
		OAuth: OAuthConfig{
			ClientID:     "client",
			ClientSecret: "secret",
			AuthURL:      "https://auth.example/authorize",
			TokenURL:     "https://auth.example/token",
			RedirectURL:  "http://127.0.0.1:8080/callback",
			Scopes:       []string{"account:read", "sms:write"},
		},
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config permissions = %o, want 600", got)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.BaseURL != cfg.BaseURL || loaded.Auth.AccessToken != "access" || loaded.Auth.RefreshToken != "refresh" {
		t.Errorf("loaded config = %+v", loaded)
	}
	if loaded.Auth.Expiry == nil || !loaded.Auth.Expiry.Equal(expiry) {
		t.Errorf("expiry = %v, want %v", loaded.Auth.Expiry, expiry)
	}
	if loaded.OAuth.ClientID != "client" || loaded.OAuth.ClientSecret != "secret" {
		t.Errorf("oauth = %+v", loaded.OAuth)
	}
	if len(loaded.OAuth.Scopes) != 2 || loaded.OAuth.Scopes[1] != "sms:write" {
		t.Errorf("scopes = %v", loaded.OAuth.Scopes)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.Auth.AccessToken != "" || cfg.BaseURL != "" {
		t.Errorf("config = %+v, want zero value", cfg)
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("auth: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig() accepted invalid YAML")
	}
}

func TestSaveConfigRejectsEmptyPath(t *testing.T) {
	if err := SaveConfig("", &Config{}); err == nil {
		t.Fatal("SaveConfig() accepted an empty path")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "sipgate", "config.yaml"); path != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", path, want)
	}
}

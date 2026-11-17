package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T, configPath string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	app := &App{
		Out:         out,
		Err:         errOut,
		ConfigPath:  configPath,
		Output:      "json",
		Retries:     2,
		openBrowser: func(string) error { return nil },
		now:         time.Now,
	}
	return app, out, errOut
}

func writeConfig(t *testing.T, path string, cfg *Config) {
	t.Helper()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestAccountGetWithAccessTokenFlag(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/account" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Love sipgate","email":"test@sipgate.de"}`)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	if err := app.Run(context.Background(), "account", "get", "--base-url", srv.URL, "--access-token", "oauth-token"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotAuth != "Bearer oauth-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if body["company"] != "Love sipgate" {
		t.Errorf("company = %v", body["company"])
	}
}

func TestAccountGetWithPATFromConfig(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Config sipgate"}`)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{
		BaseURL: srv.URL,
		Auth:    AuthConfig{Type: AuthTypePAT, TokenID: "token-id", Token: "secret"},
	})

	app, out, _ := testApp(t, path)
	if err := app.Run(context.Background(), "account", "get"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("token-id:secret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if !strings.Contains(out.String(), "Config sipgate") {
		t.Errorf("output = %s", out.String())
	}
}

func TestAccountGetWithPATFromConfigWithoutType(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Legacy sipgate"}`)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{
		Auth: AuthConfig{TokenID: "token-id", Token: "secret"},
	})

	app, out, _ := testApp(t, path)
	if err := app.Run(context.Background(), "account", "get", "--base-url", srv.URL); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("token-id:secret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if !strings.Contains(out.String(), "Legacy sipgate") {
		t.Errorf("output = %s", out.String())
	}
}

func TestSMSCommandArgumentPrecedence(t *testing.T) {
	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(), "sms", "send")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %v, want required flag error", err)
	}
}

func TestSMSSend(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/sms" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"sms", "send",
		"--base-url", srv.URL,
		"--access-token", "token",
		"--sms-id", "s0",
		"--to", "+4915799912345",
		"--message", "hello",
		"--send-at", "2026-11-17T12:30:00Z",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if body["smsId"] != "s0" || body["recipient"] != "+4915799912345" || body["message"] != "hello" {
		t.Errorf("body = %v", body)
	}
	wantSendAt := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC).Unix()
	if body["sendAt"] != float64(wantSendAt) {
		t.Errorf("sendAt = %v, want %d", body["sendAt"], wantSendAt)
	}
	if !strings.Contains(out.String(), `"status": "sent"`) {
		t.Errorf("output = %s", out.String())
	}
}

func TestCallsStartAndHangup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/calls":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"sessionId":"session-1"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/calls/session-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"calls", "start",
		"--base-url", srv.URL,
		"--access-token", "token",
		"--caller", "e0",
		"--callee", "+4915799912345",
	)
	if err != nil {
		t.Fatalf("calls start error = %v", err)
	}
	if !strings.Contains(out.String(), "session-1") {
		t.Errorf("output = %s", out.String())
	}

	out.Reset()
	err = app.Run(context.Background(),
		"calls", "hangup", "session-1",
		"--base-url", srv.URL,
		"--access-token", "token",
	)
	if err != nil {
		t.Fatalf("calls hangup error = %v", err)
	}
	if !strings.Contains(out.String(), `"status": "hangup"`) {
		t.Errorf("output = %s", out.String())
	}
}

func TestHistoryListQuery(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[],"totalCount":0}`)
	}))
	defer srv.Close()

	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"history", "list",
		"--base-url", srv.URL,
		"--access-token", "token",
		"--limit", "5",
		"--type", "SMS",
		"--direction", "INCOMING",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if query.Get("limit") != "5" || query.Get("types") != "SMS" || query.Get("directions") != "INCOMING" {
		t.Errorf("query = %v", query)
	}
}

func TestContactsList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contacts" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"id":"c1","name":"Ada Lovelace"}],"totalCount":1}`)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	if err := app.Run(context.Background(), "contacts", "list", "--base-url", srv.URL, "--access-token", "token"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "Ada Lovelace") {
		t.Errorf("output = %s", out.String())
	}
}

func TestRawAPICommand(t *testing.T) {
	var query url.Values
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"api", "GET", "/custom",
		"--base-url", srv.URL,
		"--access-token", "token",
		"--query", "limit=5",
		"--header", "X-Test: yes",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if query.Get("limit") != "5" {
		t.Errorf("query = %v", query)
	}
	if auth != "Bearer token" {
		t.Errorf("Authorization = %q", auth)
	}
	if !strings.Contains(out.String(), `"ok": true`) {
		t.Errorf("output = %s", out.String())
	}
}

func TestRawAPIPOSTBody(t *testing.T) {
	var gotBody string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"api", "POST", "/custom",
		"--base-url", srv.URL,
		"--access-token", "token",
		"--data", `{"hello":"world"}`,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotBody != `{"hello":"world"}` || gotContentType != "application/json" {
		t.Errorf("body = %q, content type = %q", gotBody, gotContentType)
	}
}

func TestYAMLOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Love sipgate"}`)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(), "account", "get", "--base-url", srv.URL, "--access-token", "token", "-o", "yaml")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "company: Love sipgate") {
		t.Errorf("output = %s", out.String())
	}
}

func TestMissingAuthentication(t *testing.T) {
	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(), "account", "get")
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error = %v, want not authenticated", err)
	}
}

func TestAuthStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	expiry := time.Now().Add(time.Hour).UTC()
	writeConfig(t, path, &Config{
		BaseURL: "https://api.example.test/v2",
		Auth: AuthConfig{
			Type:        AuthTypeOAuth,
			AccessToken: "access",
			Expiry:      &expiry,
		},
		OAuth: OAuthConfig{Scopes: []string{"account:read"}},
	})

	app, out, _ := testApp(t, path)
	if err := app.Run(context.Background(), "auth", "status"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != true || status["type"] != AuthTypeOAuth {
		t.Errorf("status = %v", status)
	}
	if status["base_url"] != "https://api.example.test/v2" {
		t.Errorf("base_url = %v", status["base_url"])
	}
}

func TestAuthStatusFromFlags(t *testing.T) {
	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	if err := app.Run(context.Background(), "auth", "status", "--token-id", "id", "--token", "secret"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != true || status["type"] != AuthTypePAT {
		t.Errorf("status = %v", status)
	}
}

func TestAuthLogout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{
		BaseURL: "https://api.example.test/v2",
		Auth:    AuthConfig{Type: AuthTypeOAuth, AccessToken: "access", RefreshToken: "refresh"},
	})

	app, _, _ := testApp(t, path)
	if err := app.Run(context.Background(), "auth", "logout"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AccessToken != "" || cfg.Auth.RefreshToken != "" || cfg.Auth.Type != "" {
		t.Errorf("auth not cleared: %+v", cfg.Auth)
	}
	if cfg.BaseURL != "https://api.example.test/v2" {
		t.Errorf("base URL lost: %q", cfg.BaseURL)
	}
}

func TestExpiredAccessTokenIsRefreshedAndPersisted(t *testing.T) {
	var refreshForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		refreshForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh","refresh_token":"fresh-refresh","token_type":"Bearer","expires_in":300}`)
	}))
	defer tokenSrv.Close()

	var apiAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Refreshed"}`)
	}))
	defer apiSrv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	past := time.Now().Add(-time.Hour)
	writeConfig(t, path, &Config{
		BaseURL: apiSrv.URL,
		Auth: AuthConfig{
			Type:         AuthTypeOAuth,
			AccessToken:  "stale",
			RefreshToken: "refresh",
			Expiry:       &past,
		},
		OAuth: OAuthConfig{ClientID: "client", TokenURL: tokenSrv.URL},
	})

	app, _, _ := testApp(t, path)
	if err := app.Run(context.Background(), "account", "get"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if refreshForm.Get("grant_type") != "refresh_token" || refreshForm.Get("refresh_token") != "refresh" {
		t.Errorf("refresh form = %v", refreshForm)
	}
	if apiAuth != "Bearer fresh" {
		t.Errorf("API Authorization = %q", apiAuth)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AccessToken != "fresh" || cfg.Auth.RefreshToken != "fresh-refresh" {
		t.Errorf("saved auth = %+v", cfg.Auth)
	}
	if cfg.Auth.Expiry == nil || !cfg.Auth.Expiry.After(time.Now()) {
		t.Errorf("saved expiry = %v", cfg.Auth.Expiry)
	}
}

func TestAuthLoginFlow(t *testing.T) {
	var tokenForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		tokenForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"login-access","refresh_token":"login-refresh","token_type":"Bearer","expires_in":300,"id_token":"id-token"}`)
	}))
	defer tokenSrv.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + listener.Addr().String() + "/callback"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	app, out, _ := testApp(t, path)
	read, write := io.Pipe()
	app.Err = write

	runDone := make(chan error, 1)
	go func() {
		runDone <- app.Run(context.Background(),
			"auth", "login",
			"--client-id", "client-id",
			"--token-url", tokenSrv.URL,
			"--redirect-uri", redirect,
			"--scope", "account:read",
			"--no-browser",
			"--timeout", "10s",
		)
		_ = write.Close()
	}()

	type urlResult struct {
		url string
		err error
	}
	urlCh := make(chan urlResult, 1)
	go func() {
		value, err := readAuthorizeURL(read)
		urlCh <- urlResult{url: value, err: err}
	}()

	var authorizeURL string
	select {
	case result := <-urlCh:
		if result.err != nil {
			t.Fatalf("read authorization URL: %v", result.err)
		}
		authorizeURL = result.url
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the authorization URL")
	}
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("authorization URL has no state")
	}
	callback := redirect + "?code=test-code&state=" + url.QueryEscape(state)
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("auth login error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("auth login did not finish")
	}

	if tokenForm.Get("code") != "test-code" || tokenForm.Get("code_verifier") == "" {
		t.Errorf("token form = %v", tokenForm)
	}
	if !strings.Contains(out.String(), `"status": "authenticated"`) {
		t.Errorf("output = %s", out.String())
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AccessToken != "login-access" || cfg.Auth.RefreshToken != "login-refresh" || cfg.Auth.IDToken != "id-token" {
		t.Errorf("saved auth = %+v", cfg.Auth)
	}
	if cfg.OAuth.ClientID != "client-id" || cfg.OAuth.RedirectURL != redirect {
		t.Errorf("saved oauth = %+v", cfg.OAuth)
	}
	if cfg.Auth.Expiry == nil {
		t.Error("expiry not saved")
	}
}

func TestAuthLoginRejectsNonLoopbackRedirect(t *testing.T) {
	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"auth", "login",
		"--client-id", "id",
		"--redirect-uri", "https://example.com/callback",
		"--no-browser",
	)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error = %v, want loopback error", err)
	}
}

func TestNewAppUsesEnvironment(t *testing.T) {
	t.Setenv(envBaseURL, "https://api.example.test/v2")
	t.Setenv(envTokenID, "env-id")
	t.Setenv(envToken, "env-token")
	t.Setenv(envOutput, "yaml")
	t.Setenv(envConfig, "/tmp/env-config.yaml")

	app := NewApp()
	if app.BaseURL != "https://api.example.test/v2" || app.TokenID != "env-id" || app.Token != "env-token" {
		t.Errorf("app = %+v", app)
	}
	if app.Output != "yaml" || app.ConfigPath != "/tmp/env-config.yaml" {
		t.Errorf("output/config = %q/%q", app.Output, app.ConfigPath)
	}
}

func TestFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv(envBaseURL, "https://env.example.test")
	t.Setenv(envAccessToken, "env-token")

	app := NewApp()
	out := &bytes.Buffer{}
	app.Out = out
	app.Err = &bytes.Buffer{}
	app.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")

	if err := app.Run(context.Background(), "auth", "status", "--base-url", "https://flag.example.test"); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["base_url"] != "https://flag.example.test" {
		t.Errorf("base_url = %v, want flag value", status["base_url"])
	}
	if status["authenticated"] != true {
		t.Errorf("authenticated = %v, want env access token", status["authenticated"])
	}
}

func readAuthorizeURL(r io.Reader) (string, error) {
	buf := make([]byte, 4096)
	var output strings.Builder
	for i := 0; i < 100; i++ {
		n, err := r.Read(buf)
		if n > 0 {
			output.Write(buf[:n])
			for _, line := range strings.Split(output.String(), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
					return line, nil
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("authorization URL not printed, got: %s", output.String())
		}
	}
	return "", fmt.Errorf("authorization URL not printed, got: %s", output.String())
}

func TestOAuthTokenWithoutExpiryIsUsed(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"No expiry"}`)
	}))
	defer apiSrv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{
		BaseURL: apiSrv.URL,
		Auth: AuthConfig{
			Type:         AuthTypeOAuth,
			AccessToken:  "valid",
			RefreshToken: "refresh",
		},
		OAuth: OAuthConfig{ClientID: "client", TokenURL: "://invalid"},
	})

	app, out, _ := testApp(t, path)
	if err := app.Run(context.Background(), "account", "get"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "No expiry") {
		t.Errorf("output = %s", out.String())
	}
}

func TestRawAPIYAMLOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"api", "GET", "/anything",
		"--base-url", srv.URL,
		"--access-token", "token",
		"-o", "yaml",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok: true") {
		t.Errorf("output = %s", out.String())
	}
}

func TestConfigFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	writeConfig(t, path, &Config{BaseURL: "https://example.test"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o", dirInfo.Mode().Perm())
	}
}

func TestAPIErrorIsShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errors":[{"title":"You shall not pass","detail":"invalid token","status":401}]}`)
	}))
	defer srv.Close()

	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(), "account", "get", "--base-url", srv.URL, "--access-token", "bad")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "You shall not pass") {
		t.Fatalf("error = %v", err)
	}
}

func TestRawAPIRejectsAbsoluteURL(t *testing.T) {
	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"api", "GET", "https://evil.example/steal",
		"--access-token", "token",
	)
	if err == nil || !strings.Contains(err.Error(), "relative to the API base URL") {
		t.Fatalf("error = %v, want relative URL error", err)
	}
}

func TestHelpDoesNotLeakSecretFlagDefaults(t *testing.T) {
	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	app.TokenID = "token-id-secret"
	app.Token = "token-secret"
	app.AccessToken = "access-secret"
	if err := app.Run(context.Background(), "--help"); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"token-id-secret", "token-secret", "access-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("help output leaked %q:\n%s", secret, out.String())
		}
	}

	t.Setenv(envClientSecret, "client-secret-value")
	app, out, _ = testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	if err := app.Run(context.Background(), "auth", "login", "--help"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "client-secret-value") {
		t.Errorf("auth login help leaked the client secret:\n%s", out.String())
	}
}

func TestLoopbackRedirectValidation(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080/callback",
		"http://localhost:8081/callback",
		"http://[::1]:8082/callback",
	} {
		if _, err := loopbackRedirect(raw); err != nil {
			t.Errorf("loopbackRedirect(%q) error = %v", raw, err)
		}
	}
	for _, raw := range []string{
		"https://127.0.0.1:8080/callback",
		"http://example.com:8080/callback",
		"http://127.0.0.1/callback",
		"http://127.0.0.1:0/callback",
		"http://127.0.0.1:8080",
		"http://127.0.0.1:8080/callback?x=1",
		"http://user@127.0.0.1:8080/callback",
	} {
		if _, err := loopbackRedirect(raw); err == nil {
			t.Errorf("loopbackRedirect(%q) accepted an invalid URI", raw)
		}
	}
}

func TestCredentialFlagPrecedence(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"company":"Flags"}`)
	}))
	defer srv.Close()

	patAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("id:secret"))
	tests := []struct {
		name     string
		setup    func(*App)
		args     []string
		wantAuth string
		wantErr  string
	}{
		{
			name:     "explicit access token beats environment PAT",
			setup:    func(app *App) { app.TokenID, app.Token = "env-id", "env-secret" },
			args:     []string{"--access-token", "oauth"},
			wantAuth: "Bearer oauth",
		},
		{
			name:     "explicit PAT beats environment access token",
			setup:    func(app *App) { app.AccessToken = "env-access" },
			args:     []string{"--token-id", "id", "--token", "secret"},
			wantAuth: patAuth,
		},
		{
			name:    "explicit credentials are mutually exclusive",
			setup:   func(*App) {},
			args:    []string{"--access-token", "oauth", "--token-id", "id", "--token", "secret"},
			wantErr: "not both",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAuth = ""
			app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
			tt.setup(app)
			args := append([]string{"account", "get", "--base-url", srv.URL}, tt.args...)
			err := app.Run(context.Background(), args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if gotAuth != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, tt.wantAuth)
			}
		})
	}
}

func TestExpiredTokenWithoutRefreshFailsClearly(t *testing.T) {
	expiry := time.Now().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{Auth: AuthConfig{
		Type:        AuthTypeOAuth,
		AccessToken: "stale",
		Expiry:      &expiry,
	}})

	app, _, _ := testApp(t, path)
	err := app.Run(context.Background(), "account", "get")
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("error = %v, want an expired-token hint", err)
	}
}

func TestUserScopedCommandExplainsHowToPassUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"title":"Forbidden"}`)
	}))
	defer srv.Close()

	app, _, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"devices", "list",
		"--base-url", srv.URL,
		"--access-token", "token",
	)
	if err == nil || !strings.Contains(err.Error(), "use --user") {
		t.Fatalf("error = %v, want a --user hint", err)
	}
}

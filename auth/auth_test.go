package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestGenerateVerifier(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		verifier, err := GenerateVerifier()
		if err != nil {
			t.Fatalf("GenerateVerifier() error = %v", err)
		}
		if len(verifier) < 43 || len(verifier) > 128 {
			t.Errorf("verifier length = %d, want 43..128", len(verifier))
		}
		if strings.ContainsAny(verifier, "+/=") {
			t.Errorf("verifier %q is not base64url encoded without padding", verifier)
		}
		if seen[verifier] {
			t.Errorf("GenerateVerifier() returned a duplicate: %q", verifier)
		}
		seen[verifier] = true
	}
}

func TestCodeChallengeRFC7636Vector(t *testing.T) {
	// Test vector from RFC 7636, appendix B.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := CodeChallenge(verifier); got != want {
		t.Errorf("CodeChallenge() = %q, want %q", got, want)
	}
}

func TestAuthorizeURL(t *testing.T) {
	cfg := Config{
		ClientID:    "client-id",
		RedirectURL: "http://127.0.0.1:8080/callback",
		Scopes:      []string{"account:read", "sms:write"},
	}
	verifier, err := GenerateVerifier()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := cfg.AuthorizeURL("state-123", verifier)
	if err != nil {
		t.Fatalf("AuthorizeURL() error = %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "login.sipgate.com" {
		t.Errorf("authorize endpoint = %s://%s", u.Scheme, u.Host)
	}
	if !strings.HasSuffix(u.Path, "/protocol/openid-connect/auth") {
		t.Errorf("path = %q", u.Path)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             cfg.ClientID,
		"redirect_uri":          cfg.RedirectURL,
		"response_type":         "code",
		"scope":                 "account:read sms:write",
		"state":                 "state-123",
		"code_challenge":        CodeChallenge(verifier),
		"code_challenge_method": "S256",
	}
	for key, want := range checks {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestAuthorizeURLRequiresConfiguration(t *testing.T) {
	tests := []Config{
		{RedirectURL: "http://localhost/cb"},
		{ClientID: "id"},
	}
	for _, cfg := range tests {
		if _, err := cfg.AuthorizeURL("state", ""); err == nil {
			t.Errorf("AuthorizeURL(%+v) returned no error", cfg)
		}
	}
}

func TestCustomEndpoints(t *testing.T) {
	cfg := Config{
		ClientID:    "id",
		RedirectURL: "http://localhost/cb",
		AuthURL:     "https://auth.example/authorize",
		TokenURL:    "https://auth.example/token",
		LogoutURL:   "https://auth.example/logout",
	}
	raw, err := cfg.AuthorizeURL("state", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "https://auth.example/authorize?") {
		t.Errorf("authorize URL = %q", raw)
	}
	if got := cfg.LogoutURLFor("id-token", "https://app.example/bye"); !strings.HasPrefix(got, "https://auth.example/logout?") {
		t.Errorf("logout URL = %q", got)
	}
}

func TestExchange(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "client-id" || pass != "client-secret" {
			t.Errorf("basic auth = %q:%q ok=%v", user, pass, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"token_type":    "Bearer",
			"expires_in":    300,
		})
	}))
	defer srv.Close()

	cfg := Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "http://127.0.0.1:8080/callback",
		TokenURL:     srv.URL,
	}
	token, err := cfg.Exchange(context.Background(), "auth-code", "verifier")
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" {
		t.Errorf("token = %+v", token)
	}
	checks := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "auth-code",
		"code_verifier": "verifier",
		"redirect_uri":  cfg.RedirectURL,
	}
	for key, want := range checks {
		if got := form.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestExchangeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer srv.Close()

	cfg := Config{ClientID: "id", RedirectURL: "http://localhost/cb", TokenURL: srv.URL}
	_, err := cfg.Exchange(context.Background(), "expired", "")
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error = %v, want invalid_grant", err)
	}
}

func TestTokenSourceRefreshesExpiredToken(t *testing.T) {
	var grants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grants = append(grants, r.PostForm.Get("grant_type"))
		if got := r.PostForm.Get("refresh_token"); got != "refresh-token" {
			t.Errorf("refresh_token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "fresh-access",
			"refresh_token": "fresh-refresh",
			"token_type":    "Bearer",
			"expires_in":    300,
		})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "id", TokenURL: srv.URL}
	expired := &oauth2.Token{
		AccessToken:  "stale",
		RefreshToken: "refresh-token",
		Expiry:       time.Now().Add(-time.Minute),
	}
	token, err := cfg.TokenSource(context.Background(), expired).Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token.AccessToken != "fresh-access" {
		t.Errorf("access token = %q", token.AccessToken)
	}
	if len(grants) != 1 || grants[0] != "refresh_token" {
		t.Errorf("grants = %v", grants)
	}
}

func TestLogoutURL(t *testing.T) {
	raw := Config{ClientID: "client id"}.LogoutURLFor("id token", "https://app.example/bye")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "login.sipgate.com" {
		t.Errorf("host = %q", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != "client id" || q.Get("id_token_hint") != "id token" || q.Get("post_logout_redirect_uri") != "https://app.example/bye" {
		t.Errorf("query = %v", q)
	}
}

func TestPublicClientExchangeSendsClientIDInForm(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("public client sent basic auth")
		}
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "public-id", RedirectURL: "http://localhost/cb", TokenURL: srv.URL}
	token, err := cfg.Exchange(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" {
		t.Errorf("token = %+v", token)
	}
	if form.Get("client_id") != "public-id" {
		t.Errorf("client_id = %q", form.Get("client_id"))
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "missing client id", cfg: Config{RedirectURL: "http://localhost/cb"}},
		{name: "invalid auth URL", cfg: Config{ClientID: "id", RedirectURL: "http://localhost/cb", AuthURL: "://bad"}},
		{name: "invalid redirect URL", cfg: Config{ClientID: "id", RedirectURL: "://bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.cfg.AuthorizeURL("state", ""); err == nil {
				t.Error("AuthorizeURL() returned no error")
			}
		})
	}
}

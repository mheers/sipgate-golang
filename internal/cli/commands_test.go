package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedRequest struct {
	method string
	path   string
	body   []byte
}

type fakeSipgate struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeSipgate(t *testing.T) *fakeSipgate {
	t.Helper()
	fake := &fakeSipgate{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.mu.Lock()
		fake.requests = append(fake.requests, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
		fake.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/account":
			_, _ = io.WriteString(w, `{"company":"Fake sipgate"}`)
		case r.URL.Path == "/balance":
			_, _ = io.WriteString(w, `{"amount":100,"currency":"EUR"}`)
		case r.URL.Path == "/authorization/userinfo":
			_, _ = io.WriteString(w, `{"sub":"w0","domain":"sipgate.de"}`)
		case r.URL.Path == "/sessions/sms":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/sessions/calls":
			_, _ = io.WriteString(w, `{"sessionId":"session-1"}`)
		case r.URL.Path == "/sessions/fax":
			_, _ = io.WriteString(w, `{"sessionId":"fax-1"}`)
		case r.URL.Path == "/calls":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case strings.HasPrefix(r.URL.Path, "/calls/"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/history":
			_, _ = io.WriteString(w, `{"items":[],"totalCount":0}`)
		case strings.HasPrefix(r.URL.Path, "/history/"):
			_, _ = io.WriteString(w, `{"id":"e1","type":"SMS"}`)
		case r.URL.Path == "/contacts":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = io.WriteString(w, `{"items":[{"id":"c1","name":"Ada"}],"totalCount":1}`)
		case strings.HasPrefix(r.URL.Path, "/contacts/"):
			if r.Method == http.MethodDelete {
				_, _ = io.WriteString(w, `{"success":true}`)
				return
			}
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = io.WriteString(w, `{"id":"c1","name":"Ada"}`)
		case r.URL.Path == "/addresses":
			_, _ = io.WriteString(w, `{"items":[{"addressId":"1"}]}`)
		case strings.HasPrefix(r.URL.Path, "/addresses/"):
			_, _ = io.WriteString(w, `{"addressId":"1","city":"Berlin"}`)
		case r.URL.Path == "/w0/devices":
			_, _ = io.WriteString(w, `{"items":[{"id":"d1"}]}`)
		case strings.HasPrefix(r.URL.Path, "/devices/"):
			_, _ = io.WriteString(w, `{"id":"d1"}`)
		case r.URL.Path == "/w0/sms":
			_, _ = io.WriteString(w, `{"items":[{"id":"s0","alias":"SMS"}]}`)
		case r.URL.Path == "/numbers":
			_, _ = io.WriteString(w, `{"items":[{"number":"+49111","addressId":"2428129"}]}`)
		case r.URL.Path == "/w0/numbers":
			_, _ = io.WriteString(w, `{"items":[{"number":"+49222","addressId":"2428129"}]}`)
		case r.URL.Path == "/phonelines":
			_, _ = io.WriteString(w, `{"items":[{"id":"p1"}]}`)
		case r.URL.Path == "/w0/phonelines":
			_, _ = io.WriteString(w, `{"items":[{"id":"p1"}]}`)
		case r.URL.Path == "/w0/phonelines/p1":
			_, _ = io.WriteString(w, `{"id":"p1","alias":"Office"}`)
		case r.URL.Path == "/users":
			_, _ = io.WriteString(w, `{"items":[{"id":"w0"}]}`)
		case strings.HasPrefix(r.URL.Path, "/users/"):
			_, _ = io.WriteString(w, `{"id":"w0","firstname":"Ada"}`)
		case r.URL.Path == "/voicemails":
			_, _ = io.WriteString(w, `{"items":[{"id":"v1"}]}`)
		case strings.HasPrefix(r.URL.Path, "/voicemails/"):
			_, _ = io.WriteString(w, `{"id":"v1"}`)
		case r.URL.Path == "/app/events":
			_, _ = io.WriteString(w, `{"items":[{"eventId":"1"}]}`)
		case strings.HasPrefix(r.URL.Path, "/app/events/"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/settings/sipgateio":
			_, _ = io.WriteString(w, `{"incomingUrl":"https://example.test/in","outgoingUrl":"https://example.test/out"}`)
		case r.URL.Path == "/log/webhooks":
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeSipgate) last() recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return recordedRequest{}
	}
	return f.requests[len(f.requests)-1]
}

func TestCLICommandWiring(t *testing.T) {
	faxFile := filepath.Join(t.TempDir(), "fax.pdf")
	if err := os.WriteFile(faxFile, []byte("%PDF-1.4 test"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		method   string
		path     string
		bodyPart string
	}{
		{name: "account balance", args: []string{"account", "balance"}, method: http.MethodGet, path: "/balance"},
		{name: "whoami", args: []string{"whoami"}, method: http.MethodGet, path: "/authorization/userinfo"},
		{name: "sms list", args: []string{"sms", "list", "--user", "w0"}, method: http.MethodGet, path: "/w0/sms"},
		{name: "sms list current user", args: []string{"sms", "list"}, method: http.MethodGet, path: "/w0/sms"},
		{name: "calls list", args: []string{"calls", "list"}, method: http.MethodGet, path: "/calls"},
		{name: "calls dtmf", args: []string{"calls", "dtmf", "call-1", "--sequence", "123#"}, method: http.MethodPost, path: "/calls/call-1/dtmf", bodyPart: `"sequence":"123#"`},
		{name: "history get", args: []string{"history", "get", "e1"}, method: http.MethodGet, path: "/history/e1"},
		{name: "contacts get", args: []string{"contacts", "get", "c1"}, method: http.MethodGet, path: "/contacts/c1"},
		{name: "contacts create", args: []string{"contacts", "create", "--name", "Ada", "--email", "ada@example.com"}, method: http.MethodPost, path: "/contacts", bodyPart: `"name":"Ada"`},
		{name: "contacts update", args: []string{"contacts", "update", "c1", "--note", "updated"}, method: http.MethodPut, path: "/contacts/c1", bodyPart: `"note":"updated"`},
		{name: "contacts delete", args: []string{"contacts", "delete", "c1"}, method: http.MethodDelete, path: "/contacts/c1"},
		{name: "addresses list", args: []string{"addresses", "list"}, method: http.MethodGet, path: "/addresses"},
		{name: "addresses get", args: []string{"addresses", "get", "1"}, method: http.MethodGet, path: "/addresses/1"},
		{name: "devices list", args: []string{"devices", "list", "--user", "w0"}, method: http.MethodGet, path: "/w0/devices"},
		{name: "devices list current user", args: []string{"devices", "list"}, method: http.MethodGet, path: "/w0/devices"},
		{name: "devices get", args: []string{"devices", "get", "d1"}, method: http.MethodGet, path: "/devices/d1"},
		{name: "numbers list", args: []string{"numbers", "list"}, method: http.MethodGet, path: "/numbers"},
		{name: "numbers list user", args: []string{"numbers", "list", "--user", "w0"}, method: http.MethodGet, path: "/w0/numbers"},
		{name: "phonelines list", args: []string{"phonelines", "list"}, method: http.MethodGet, path: "/phonelines"},
		{name: "phonelines list user", args: []string{"phonelines", "list", "--user", "w0"}, method: http.MethodGet, path: "/w0/phonelines"},
		{name: "phonelines get", args: []string{"phonelines", "get", "p1", "--user", "w0"}, method: http.MethodGet, path: "/w0/phonelines/p1"},
		{name: "phonelines get current user", args: []string{"phonelines", "get", "p1"}, method: http.MethodGet, path: "/w0/phonelines/p1"},
		{name: "users list", args: []string{"users", "list"}, method: http.MethodGet, path: "/users"},
		{name: "users get", args: []string{"users", "get", "w0"}, method: http.MethodGet, path: "/users/w0"},
		{name: "voicemails list", args: []string{"voicemails", "list"}, method: http.MethodGet, path: "/voicemails"},
		{name: "voicemails get", args: []string{"voicemails", "get", "v1"}, method: http.MethodGet, path: "/voicemails/v1"},
		{name: "fax send", args: []string{"fax", "send", "--faxline", "f0", "--to", "+49111", "--file", faxFile}, method: http.MethodPost, path: "/sessions/fax", bodyPart: `"faxlineId":"f0"`},
		{name: "events list", args: []string{"events", "list"}, method: http.MethodGet, path: "/app/events"},
		{name: "events delete", args: []string{"events", "delete", "1"}, method: http.MethodDelete, path: "/app/events/1"},
		{name: "webhooks get", args: []string{"webhooks", "get"}, method: http.MethodGet, path: "/settings/sipgateio"},
		{name: "webhooks set", args: []string{"webhooks", "set", "--incoming-url", "https://example.test/in", "--outgoing-url", "https://example.test/out"}, method: http.MethodPut, path: "/settings/sipgateio", bodyPart: `"incomingUrl":"https://example.test/in"`},
		{name: "webhooks log", args: []string{"webhooks", "log"}, method: http.MethodGet, path: "/log/webhooks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeSipgate(t)
			app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
			args := append([]string{}, tt.args...)
			args = append(args, "--base-url", fake.server.URL, "--access-token", "token")
			if err := app.Run(context.Background(), args...); err != nil {
				t.Fatalf("Run(%v) error = %v", tt.args, err)
			}
			last := fake.last()
			if last.method != tt.method || last.path != tt.path {
				t.Errorf("request = %s %s, want %s %s", last.method, last.path, tt.method, tt.path)
			}
			if tt.bodyPart != "" && !strings.Contains(string(last.body), tt.bodyPart) {
				t.Errorf("body = %s, want substring %s", last.body, tt.bodyPart)
			}
			if out.Len() == 0 && tt.method != http.MethodDelete {
				t.Error("command produced no output")
			}
		})
	}
}

func TestAuthStatusExplicitAccessTokenBeatsEnvironmentPAT(t *testing.T) {
	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	app.TokenID, app.Token = "env-id", "env-secret"
	if err := app.Run(context.Background(), "auth", "status", "--access-token", "oauth"); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != true || status["type"] != AuthTypeOAuth {
		t.Errorf("status = %v", status)
	}
}

func TestAuthStatusRejectsAmbiguousExplicitCredentials(t *testing.T) {
	app, out, _ := testApp(t, filepath.Join(t.TempDir(), "config.yaml"))
	err := app.Run(context.Background(),
		"auth", "status",
		"--access-token", "oauth",
		"--token-id", "id",
		"--token", "secret",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != false || status["error"] == nil {
		t.Errorf("status = %v, want an unauthenticated error state", status)
	}
}

func TestAuthStatusExpiredToken(t *testing.T) {
	expiry := time.Now().Add(-time.Minute)
	for _, tt := range []struct {
		name         string
		refreshToken string
		wantAuth     bool
	}{
		{name: "without refresh token", wantAuth: false},
		{name: "with refresh token", refreshToken: "refresh", wantAuth: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeConfig(t, path, &Config{Auth: AuthConfig{
				Type:         AuthTypeOAuth,
				AccessToken:  "stale",
				RefreshToken: tt.refreshToken,
				Expiry:       &expiry,
			}})

			app, out, _ := testApp(t, path)
			if err := app.Run(context.Background(), "auth", "status"); err != nil {
				t.Fatal(err)
			}
			var status map[string]any
			if err := json.Unmarshal(out.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status["authenticated"] != tt.wantAuth {
				t.Errorf("authenticated = %v, want %v", status["authenticated"], tt.wantAuth)
			}
			if status["expired"] != true {
				t.Errorf("expired = %v, want true", status["expired"])
			}
		})
	}
}

func TestAuthStatusInfersOAuthTypeWithoutExplicitType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, &Config{Auth: AuthConfig{AccessToken: "access"}})

	app, out, _ := testApp(t, path)
	if err := app.Run(context.Background(), "auth", "status"); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != true || status["type"] != AuthTypeOAuth {
		t.Errorf("status = %v", status)
	}
}

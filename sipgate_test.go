package sipgate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func newClient(t *testing.T, handler http.Handler, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	base := []Option{
		WithToken("test-access-token"),
		WithBaseURL(srv.URL + "/"),
		WithRetryBackoff(time.Millisecond, 2*time.Millisecond),
	}
	c, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

type countingTokenSource struct {
	calls int
	token string
	err   error
}

func (s *countingTokenSource) Token() (*oauth2.Token, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &oauth2.Token{AccessToken: s.token}, nil
}

func TestPersonalAccessTokenUsesBasicAuth(t *testing.T) {
	const tokenID, token = "token-FQ1V12", "e68ead46-a7db-46cd-8a1a-44aed1e4e372"
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(tokenID+":"+token))

	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}), WithPersonalAccessToken(tokenID, token))

	if _, err := c.Account(context.Background()); err != nil {
		t.Fatalf("Account() error = %v", err)
	}
}

func TestBearerTokenIsSent(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-access-token" {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}))
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatalf("Account() error = %v", err)
	}
}

func TestTokenSourceIsConsultedForEveryRequest(t *testing.T) {
	src := &countingTokenSource{token: "rotating-token"}
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer rotating-token" {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}), WithTokenSource(src))

	for i := 0; i < 2; i++ {
		if _, err := c.Account(context.Background()); err != nil {
			t.Fatalf("Account() error = %v", err)
		}
	}
	if src.calls != 2 {
		t.Errorf("token source called %d times, want 2", src.calls)
	}
}

func TestTokenSourceErrorIsReturned(t *testing.T) {
	src := &countingTokenSource{err: errors.New("token endpoint down")}
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the API without a token")
	}), WithTokenSource(src))

	_, err := c.Account(context.Background())
	if err == nil || !strings.Contains(err.Error(), "token endpoint down") {
		t.Fatalf("error = %v, want token source error", err)
	}
}

func TestCredentialsAreTrimmed(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer trimmed" {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}), WithToken("  trimmed  "))
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatal(err)
	}

	c = newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("id:secret"))
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}), WithPersonalAccessToken("  id  ", "  secret  "))
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "no credentials"},
		{name: "empty bearer token", opts: []Option{WithToken("")}},
		{name: "empty token id", opts: []Option{WithPersonalAccessToken("", "secret")}},
		{name: "empty token", opts: []Option{WithPersonalAccessToken("token-id", "")}},
		{name: "nil token source", opts: []Option{WithTokenSource(nil)}},
		{name: "invalid base URL", opts: []Option{WithToken("t"), WithBaseURL("://nope")}},
		{name: "base URL with query", opts: []Option{WithToken("t"), WithBaseURL("https://api.example/v2?x=1")}},
		{name: "base URL with userinfo", opts: []Option{WithToken("t"), WithBaseURL("https://user:pass@api.example/v2")}},
		{name: "negative retries", opts: []Option{WithToken("t"), WithRetries(-1)}},
		{name: "nil HTTP client", opts: []Option{WithToken("t"), WithHTTPClient(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts...); err == nil {
				t.Fatal("New() returned no error")
			}
		})
	}
}

func TestVersionFromBuildInfo(t *testing.T) {
	const modulePath = "github.com/mheers/sipgate-golang"
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "main module",
			info: &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}},
			want: "v1.2.3",
		},
		{
			name: "dependency",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"},
				Deps: []*debug.Module{{Path: modulePath, Version: "v1.0.0"}},
			},
			want: "v1.0.0",
		},
		{
			name: "replaced dependency",
			info: &debug.BuildInfo{
				Deps: []*debug.Module{{
					Path:    modulePath,
					Version: "v1.0.0",
					Replace: &debug.Module{Path: "../sipgate-golang", Version: "v2.0.0"},
				}},
			},
			want: "v2.0.0",
		},
		{
			name: "devel",
			info: &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}},
		},
		{
			name: "unrelated module",
			info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"}},
		},
		{
			name: "nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionFromBuildInfo(tt.info); got != tt.want {
				t.Errorf("versionFromBuildInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultsAndBaseURLNormalization(t *testing.T) {
	var path string
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "sipgate-golang/") {
			t.Errorf("User-Agent = %q, want sipgate-golang prefix", ua)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}))
	if got := c.BaseURL(); !strings.HasSuffix(got, "/") {
		t.Errorf("BaseURL() = %q, want trailing slash", got)
	}
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
	if path != "/account" {
		t.Errorf("path = %q, want /account", path)
	}
}

func TestUserAgentCanBeOverridden(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "my-app/1.2.3" {
			t.Errorf("User-Agent = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{})
	}), WithUserAgent("my-app/1.2.3"))
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAccount(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/account" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"company":   "Love sipgate",
			"email":     "test@sipgate.de",
			"firstName": "Ada",
			"lastName":  "Lovelace",
		})
	}))

	account, err := c.Account(context.Background())
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.GetCompany() != "Love sipgate" {
		t.Errorf("company = %q", account.GetCompany())
	}
	if account.GetEmail() != "test@sipgate.de" {
		t.Errorf("email = %q", account.GetEmail())
	}
}

func TestUserinfo(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authorization/userinfo" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"sub": "w0", "domain": "sipgate.de"})
	}))

	info, err := c.Userinfo(context.Background())
	if err != nil {
		t.Fatalf("Userinfo() error = %v", err)
	}
	if info.GetSub() != "w0" {
		t.Errorf("sub = %q", info.GetSub())
	}
}

func TestSendSMS(t *testing.T) {
	var got map[string]any
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sessions/sms" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	err := c.SendSMS(context.Background(), SMS{
		SMSID:     "s0",
		Recipient: "+4915799912345",
		Message:   "hello",
		SendAt:    time.Unix(1546850272, 0),
	})
	if err != nil {
		t.Fatalf("SendSMS() error = %v", err)
	}
	if got["smsId"] != "s0" || got["recipient"] != "+4915799912345" || got["message"] != "hello" {
		t.Errorf("body = %v", got)
	}
	if got["sendAt"] != float64(1546850272) {
		t.Errorf("sendAt = %v", got["sendAt"])
	}
}

func TestSendSMSOmitsZeroSendAt(t *testing.T) {
	var got map[string]any
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	if err := c.SendSMS(context.Background(), SMS{SMSID: "s0", Recipient: "+49", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["sendAt"]; ok {
		t.Error("sendAt should be omitted when zero")
	}
}

func TestSendSMSValidation(t *testing.T) {
	c, err := New(WithToken("t"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []SMS{
		{Recipient: "+49", Message: "hi"},
		{SMSID: "s0", Message: "hi"},
		{SMSID: "s0", Recipient: "+49"},
	}
	for _, sms := range tests {
		if err := c.SendSMS(context.Background(), sms); err == nil {
			t.Errorf("SendSMS(%+v) returned no error", sms)
		}
	}
}

func TestCreateCall(t *testing.T) {
	var got map[string]any
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sessions/calls" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		writeJSON(t, w, http.StatusOK, map[string]any{"sessionId": "session-1"})
	}))

	sessionID, err := c.CreateCall(context.Background(), CallRequest{
		Caller:   "e0",
		Callee:   "+4915799912345",
		CallerID: "+492111234567",
	})
	if err != nil {
		t.Fatalf("CreateCall() error = %v", err)
	}
	if sessionID != "session-1" {
		t.Errorf("sessionID = %q", sessionID)
	}
	if got["caller"] != "e0" || got["callee"] != "+4915799912345" || got["callerId"] != "+492111234567" {
		t.Errorf("body = %v", got)
	}
}

func TestCreateCallValidation(t *testing.T) {
	c, err := New(WithToken("t"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateCall(context.Background(), CallRequest{Callee: "+49"}); err == nil {
		t.Error("CreateCall without caller returned no error")
	}
	if _, err := c.CreateCall(context.Background(), CallRequest{Caller: "e0"}); err == nil {
		t.Error("CreateCall without callee returned no error")
	}
}

func TestCallsAndHangup(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calls":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"data": []any{map[string]any{"callId": "call-1", "hold": false, "muted": false, "recording": false, "participants": []any{}}},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/calls/call-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	calls, err := c.Calls(context.Background())
	if err != nil {
		t.Fatalf("Calls() error = %v", err)
	}
	if len(calls) != 1 || calls[0].GetCallId() != "call-1" {
		t.Fatalf("calls = %+v", calls)
	}
	if err := c.HangupCall(context.Background(), "call-1"); err != nil {
		t.Fatalf("HangupCall() error = %v", err)
	}
}

func TestDetailedErrorResponse(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"errors": []any{map[string]any{
				"status": 404,
				"title":  "Account not found",
				"detail": "The account does not exist",
			}},
		})
	}))

	_, err := c.Account(context.Background())
	if err == nil {
		t.Fatal("Account() returned no error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error %T is not an *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Title != "Account not found" || apiErr.Detail != "The account does not exist" {
		t.Errorf("APIError = %+v", apiErr)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound() = false")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestPlainTextErrorResponse(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "Unauthorized\n")
	}))

	_, err := c.Account(context.Background())
	if err == nil {
		t.Fatal("Account() returned no error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error %T is not an *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Detail != "Unauthorized" {
		t.Errorf("Detail = %q", apiErr.Detail)
	}
	if !IsUnauthorized(err) {
		t.Error("IsUnauthorized() = false")
	}
}

func TestSingleErrorResponse(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"title":  "Bad request",
			"detail": "smsId is missing",
			"status": 400,
		})
	}))
	_, err := c.Account(context.Background())
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error = %v", err)
	}
	if apiErr.Title != "Bad request" || apiErr.Detail != "smsId is missing" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestRetryIdempotentRequestOnServerError(t *testing.T) {
	var attempts atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"company": "retried"})
	}), WithRetries(2))

	account, err := c.Account(context.Background())
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.GetCompany() != "retried" {
		t.Errorf("company = %q", account.GetCompany())
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestNoRetryForPostOnServerError(t *testing.T) {
	var attempts atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}), WithRetries(3))

	err := c.SendSMS(context.Background(), SMS{SMSID: "s0", Recipient: "+49", Message: "hi"})
	if err == nil {
		t.Fatal("SendSMS() returned no error")
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (POST must not be retried on 5xx)", attempts.Load())
	}
}

func TestRetryPostOnTooManyRequests(t *testing.T) {
	var attempts atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), WithRetries(1))

	if err := c.SendSMS(context.Background(), SMS{SMSID: "s0", Recipient: "+49", Message: "hi"}); err != nil {
		t.Fatalf("SendSMS() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

func TestRetriesCanBeDisabled(t *testing.T) {
	var attempts atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}), WithRetries(0))

	if _, err := c.Account(context.Background()); err == nil {
		t.Fatal("Account() returned no error")
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1", attempts.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{value: "0", want: 0, ok: true},
		{value: "2", want: 2 * time.Second, ok: true},
		{value: now.Add(3 * time.Second).Format(http.TimeFormat), want: 3 * time.Second, ok: true},
		{value: now.Add(-time.Minute).Format(http.TimeFormat), want: 0, ok: true},
		{value: "later", ok: false},
		{value: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(tt.value, now)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseRetryAfter(%q) = (%v, %v), want (%v, %v)", tt.value, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCustomHTTPClient(t *testing.T) {
	seen := false
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"company":"custom"}`)),
			Request:    r,
		}, nil
	})
	c, err := New(WithToken("t"), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	account, err := c.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !seen || account.GetCompany() != "custom" {
		t.Errorf("custom transport not used (seen=%v, company=%q)", seen, account.GetCompany())
	}
}

func TestRawRequest(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/raw" || r.URL.Query().Get("x") != "1" {
			t.Errorf("request = %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"ok": true})
	}))

	req, err := c.NewRequest(context.Background(), http.MethodGet, "/raw?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Errorf("body = %v", body)
	}
}

func TestNewRequestRejectsInvalidPath(t *testing.T) {
	c, err := New(WithToken("t"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"http://evil.example",
		"HTTPS://evil.example",
		"//evil.example",
	} {
		if _, err := c.NewRequest(context.Background(), http.MethodGet, path, nil); err == nil {
			t.Errorf("absolute URL %q accepted", path)
		}
	}
}

func TestDoReturnsAPIErrorForFailure(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{"title": "Forbidden", "detail": "scope missing"})
	}))
	req, err := c.NewRequest(context.Background(), http.MethodGet, "/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(req)
	if !IsForbidden(err) {
		t.Fatalf("error = %v, want forbidden API error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestURLJoin(t *testing.T) {
	tests := []struct {
		base, path, want string
	}{
		{base: "https://api.sipgate.com/v2", path: "/account", want: "https://api.sipgate.com/v2/account"},
		{base: "https://api.sipgate.com/v2/", path: "account", want: "https://api.sipgate.com/v2/account"},
	}
	for _, tt := range tests {
		if got := joinURL(tt.base, tt.path); got != tt.want {
			t.Errorf("joinURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
		}
	}
}

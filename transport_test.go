package sipgate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthTransportOnlyAuthenticatesAPIOrigin(t *testing.T) {
	var headers []string
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		headers = append(headers, r.Header.Get("Authorization"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    r,
		}, nil
	})
	transport := &authTransport{
		base:        base,
		credentials: tokenCredentials{source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "secret"})},
		userAgent:   "test",
		scheme:      "https",
		host:        "api.example",
	}

	for _, raw := range []string{
		"https://api.example/v2/account",
		"https://api.example:443/v2/account",
		"https://evil.example/account",
		"https://api.example:8443/v2/account",
		"http://api.example/v2/account",
	} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip(%s) error = %v", raw, err)
		}
		_ = resp.Body.Close()
	}

	// A redirect may copy an Authorization header before this transport sees
	// the follow-up request. It must be removed for a different origin.
	req, err := http.NewRequest(http.MethodGet, "https://evil.example/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer copied")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(headers) != 6 {
		t.Fatalf("requests = %d, want 6", len(headers))
	}
	if headers[0] != "Bearer secret" || headers[1] != "Bearer secret" {
		t.Errorf("API origin Authorization = %q, want bearer token for both default and explicit port", headers[:2])
	}
	if headers[2] != "" || headers[3] != "" || headers[4] != "" || headers[5] != "" {
		t.Errorf("credentials leaked to another origin: %q", headers)
	}
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestRetryTransportClosesOriginalBodyAndReplays(t *testing.T) {
	var bodies []string
	attempts := 0
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		bodies = append(bodies, string(data))
		// A real transport closes the request body.
		_ = r.Body.Close()

		status := http.StatusInternalServerError
		if attempts == 3 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})
	transport := &retryTransport{
		base:     base,
		attempts: 3,
		baseWait: time.Millisecond,
		maxWait:  time.Millisecond,
		sleep:    func(context.Context, time.Duration) error { return nil },
	}

	original := &trackingBody{Reader: strings.NewReader("payload")}
	req := &http.Request{
		Method: http.MethodPut,
		URL:    &url.URL{Scheme: "https", Host: "api.example", Path: "/v2/resource"},
		Header: make(http.Header),
		Body:   original,
		GetBody: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("payload")), nil
		},
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	for i, body := range bodies {
		if body != "payload" {
			t.Errorf("attempt %d body = %q, want payload", i, body)
		}
	}
	if !original.closed {
		t.Error("original request body was not closed")
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	attempts := 0
	var waited time.Duration
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		status := http.StatusTooManyRequests
		if attempts == 2 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Retry-After": []string{"3600"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})
	transport := &retryTransport{
		base:     base,
		attempts: 2,
		baseWait: time.Millisecond,
		maxWait:  50 * time.Millisecond,
		sleep: func(_ context.Context, d time.Duration) error {
			waited = d
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.example/v2/calls", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	_ = resp.Body.Close()
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if waited != 50*time.Millisecond {
		t.Errorf("wait = %v, want the 50ms cap", waited)
	}
}

func TestRetryTransportTreatsNoBodyAsReplayable(t *testing.T) {
	attempts := 0
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		status := http.StatusInternalServerError
		if attempts == 2 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})
	transport := &retryTransport{
		base:     base,
		attempts: 2,
		baseWait: time.Millisecond,
		maxWait:  time.Millisecond,
		sleep:    func(context.Context, time.Duration) error { return nil },
	}

	req := httptest.NewRequest(http.MethodGet, "https://api.example/v2/calls", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	_ = resp.Body.Close()
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 for http.NoBody", attempts)
	}
}

func TestServiceUnavailableRetrySafety(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"0"}},
	}

	post := httptest.NewRequest(http.MethodPost, "/sessions/sms", nil)
	if retry, _ := shouldRetry(post, resp, nil); retry {
		t.Error("POST with 503 and Retry-After must not be retried")
	}

	get := httptest.NewRequest(http.MethodGet, "/calls", nil)
	if retry, _ := shouldRetry(get, resp, nil); !retry {
		t.Error("GET with 503 and Retry-After must be retried")
	}
}

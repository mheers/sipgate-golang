// Package sipgate provides a Go SDK for the sipgate REST API v2.
//
// The SDK is a thin, opinionated layer on top of the generated OpenAPI client
// in github.com/mheers/sipgate-golang/openapi. It handles authentication,
// retries, error decoding and exposes the complete generated API surface.
//
// # Authentication
//
// sipgate supports personal access tokens (HTTP Basic) and OAuth 2.0 (Bearer):
//
//	client, err := sipgate.New(
//		sipgate.WithPersonalAccessToken(tokenID, token),
//	)
//
//	client, err = sipgate.New(
//		sipgate.WithTokenSource(tokenSource),
//	)
//
// # API access
//
// The generated API services are embedded in Client, so every endpoint is
// available:
//
//	account, resp, err := client.AccountAPI.GetAccount(ctx).Execute()
//
// Frequently used calls have convenience methods:
//
//	account, err := client.Account(ctx)
//	err = client.SendSMS(ctx, sipgate.SMS{SMSID: "s0", Recipient: "+49...", Message: "hi"})
package sipgate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mheers/sipgate-golang/openapi"
)

const (
	// DefaultBaseURL is the base URL of the sipgate REST API v2.
	DefaultBaseURL = "https://api.sipgate.com/v2"

	defaultTimeout   = 60 * time.Second
	defaultRetries   = 2
	defaultRetryBase = 200 * time.Millisecond
	defaultRetryMax  = 5 * time.Second
)

// Version is the SDK version. It can be overridden at build time:
//
//	go build -ldflags "-X github.com/mheers/sipgate-golang.Version=v1.2.3"
var Version = "dev"

// Client is a sipgate API client. It embeds the generated OpenAPI client, so
// all API services (AccountAPI, SessionsAPI, SmsAPI, ...) are accessible
// directly.
type Client struct {
	*openapi.APIClient

	baseURL    string
	httpClient *http.Client
	userAgent  string
	retries    int
}

// New creates a sipgate API client. At least one authentication option is
// required: WithToken, WithTokenSource, WithPersonalAccessToken or
// WithBasicAuth.
func New(opts ...Option) (*Client, error) {
	o := options{
		baseURL:   DefaultBaseURL,
		userAgent: "sipgate-golang/" + Version,
		retries:   defaultRetries,
		retryBase: defaultRetryBase,
		retryMax:  defaultRetryMax,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&o); err != nil {
			return nil, err
		}
	}

	if o.credentials == nil {
		return nil, errors.New("sipgate: no credentials configured (use WithToken, WithTokenSource, WithPersonalAccessToken or WithBasicAuth)")
	}

	if err := validateBaseURL(o.baseURL); err != nil {
		return nil, err
	}
	if o.retries < 0 {
		return nil, errors.New("sipgate: retries must not be negative")
	}
	if o.retryBase <= 0 || o.retryMax < o.retryBase {
		return nil, errors.New("sipgate: invalid retry backoff configuration")
	}

	httpClient := &http.Client{Timeout: defaultTimeout}
	if o.httpClient != nil {
		clone := *o.httpClient
		httpClient = &clone
		if o.timeoutSet {
			httpClient.Timeout = o.timeout
		}
	} else if o.timeoutSet {
		httpClient.Timeout = o.timeout
	}

	baseTransport := httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}

	httpClient.Transport = &retryTransport{
		base:     &authTransport{base: baseTransport, credentials: o.credentials, userAgent: o.userAgent},
		attempts: o.retries + 1,
		baseWait: o.retryBase,
		maxWait:  o.retryMax,
		sleep:    sleepContext,
	}

	cfg := openapi.NewConfiguration()
	cfg.Servers = openapi.ServerConfigurations{{URL: strings.TrimRight(o.baseURL, "/")}}
	cfg.HTTPClient = httpClient
	cfg.UserAgent = o.userAgent

	return &Client{
		APIClient:  openapi.NewAPIClient(cfg),
		baseURL:    strings.TrimRight(o.baseURL, "/") + "/",
		httpClient: httpClient,
		userAgent:  o.userAgent,
		retries:    o.retries,
	}, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("sipgate: invalid base URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("sipgate: base URL %q must use http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("sipgate: base URL %q has no host", raw)
	}
	return nil
}

// BaseURL returns the API base URL, including a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// UserAgent returns the User-Agent header sent with every request.
func (c *Client) UserAgent() string { return c.userAgent }

// HTTPClient returns the HTTP client used for API requests.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// Retries returns the number of retries configured for idempotent requests.
func (c *Client) Retries() int { return c.retries }

// NewRequest creates an authenticated request relative to the API base URL.
// path must not be an absolute URL. Use it together with Do for endpoints that
// are not (yet) part of the generated client.
func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("sipgate: path %q must be relative to the API base URL", path)
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(c.baseURL, path), body)
	if err != nil {
		return nil, fmt.Errorf("sipgate: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}

// Do sends a request created with NewRequest. Non-2xx responses are decoded
// into an *APIError. The response body of a successful response is left open
// for the caller.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, errorFromResponse(resp)
	}
	return resp, nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

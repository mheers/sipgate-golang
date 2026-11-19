package sipgate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Option configures a Client.
type Option func(*options) error

type options struct {
	baseURL     string
	httpClient  *http.Client
	userAgent   string
	retries     int
	retryBase   time.Duration
	retryMax    time.Duration
	timeout     time.Duration
	timeoutSet  bool
	credentials credentials
}

// WithToken authenticates with an OAuth 2.0 Bearer token. Surrounding
// whitespace is removed.
func WithToken(token string) Option {
	return func(o *options) error {
		token = strings.TrimSpace(token)
		if token == "" {
			return errors.New("sipgate: token must not be empty")
		}
		o.credentials = tokenCredentials{source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})}
		return nil
	}
}

// WithTokenSource authenticates with tokens produced by an oauth2.TokenSource.
// The source is consulted for every request, so short-lived OAuth 2.0 access
// tokens are refreshed transparently.
func WithTokenSource(source oauth2.TokenSource) Option {
	return func(o *options) error {
		if source == nil {
			return errors.New("sipgate: token source must not be nil")
		}
		o.credentials = tokenCredentials{source: source}
		return nil
	}
}

// WithPersonalAccessToken authenticates with a sipgate personal access token.
// sipgate expects these tokens as HTTP Basic credentials, where the username is
// the token ID and the password is the token itself. Surrounding whitespace is
// removed from both values.
func WithPersonalAccessToken(tokenID, token string) Option {
	return WithBasicAuth(strings.TrimSpace(tokenID), strings.TrimSpace(token))
}

// WithBasicAuth authenticates with HTTP Basic credentials. Surrounding
// whitespace is removed from the username.
func WithBasicAuth(username, password string) Option {
	return func(o *options) error {
		username = strings.TrimSpace(username)
		if username == "" {
			return errors.New("sipgate: basic auth username must not be empty")
		}
		if password == "" {
			return errors.New("sipgate: basic auth password must not be empty")
		}
		o.credentials = basicCredentials{username: username, password: password}
		return nil
	}
}

// WithBaseURL overrides the API base URL. It is useful for tests and proxies.
// The URL must use http or https and must not contain userinfo, a query or a
// fragment.
func WithBaseURL(baseURL string) Option {
	return func(o *options) error {
		o.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/"
		return nil
	}
}

// WithHTTPClient uses c for API requests. Its transport and redirect policy are
// preserved and wrapped with authentication and retry handling. If no
// WithTimeout is given, the client's own timeout is respected.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("sipgate: HTTP client must not be nil")
		}
		o.httpClient = c
		return nil
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(userAgent string) Option {
	return func(o *options) error {
		userAgent = strings.TrimSpace(userAgent)
		if userAgent == "" {
			return errors.New("sipgate: user agent must not be empty")
		}
		o.userAgent = userAgent
		return nil
	}
}

// WithRetries configures how often idempotent requests are retried after a
// network error or a 5xx response. POST requests are only retried on 429 Too
// Many Requests. The default is 2 retries.
func WithRetries(retries int) Option {
	return func(o *options) error {
		if retries < 0 {
			return errors.New("sipgate: retries must not be negative")
		}
		o.retries = retries
		return nil
	}
}

// WithRetryBackoff configures the exponential backoff between retries. The wait
// starts at base and doubles per attempt, capped at max. The cap also applies to
// server-provided Retry-After delays.
func WithRetryBackoff(base, max time.Duration) Option {
	return func(o *options) error {
		if base <= 0 {
			return errors.New("sipgate: retry base duration must be positive")
		}
		if max < base {
			return errors.New("sipgate: retry max duration must not be shorter than the base duration")
		}
		o.retryBase, o.retryMax = base, max
		return nil
	}
}

// WithTimeout sets the request timeout. It overrides the timeout of a client
// passed with WithHTTPClient.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) error {
		if timeout <= 0 {
			return errors.New("sipgate: timeout must be positive")
		}
		o.timeout, o.timeoutSet = timeout, true
		return nil
	}
}

type credentials interface {
	authorization(ctx context.Context) (string, error)
}

type basicCredentials struct {
	username string
	password string
}

func (b basicCredentials) authorization(context.Context) (string, error) {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(b.username+":"+b.password)), nil
}

type tokenCredentials struct {
	source oauth2.TokenSource
}

func (t tokenCredentials) authorization(context.Context) (string, error) {
	token, err := t.source.Token()
	if err != nil {
		return "", fmt.Errorf("sipgate: obtain access token: %w", err)
	}
	if token == nil || token.AccessToken == "" {
		return "", errors.New("sipgate: token source returned an empty access token")
	}
	return "Bearer " + token.AccessToken, nil
}

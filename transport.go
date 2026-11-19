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
)

type authTransport struct {
	base        http.RoundTripper
	credentials credentials
	userAgent   string
	scheme      string
	host        string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if sameOrigin(req.URL, t.scheme, t.host) {
		if clone.Header.Get("Authorization") == "" {
			header, err := t.credentials.authorization(req.Context())
			if err != nil {
				return nil, err
			}
			clone.Header.Set("Authorization", header)
		}
	} else {
		// http.Client may follow a redirect to another host. Go drops the
		// Authorization header for most cross-host redirects, but this
		// transport must not re-add it and should also remove a header that the
		// redirect policy copied. Never send API credentials to another origin.
		clone.Header.Del("Authorization")
	}
	if clone.Header.Get("User-Agent") == "" {
		clone.Header.Set("User-Agent", t.userAgent)
	}
	return t.base.RoundTrip(clone)
}

func sameOrigin(u *url.URL, scheme, host string) bool {
	if u == nil {
		return false
	}
	return canonicalOrigin(u) == canonicalOrigin(&url.URL{Scheme: scheme, Host: host})
}

func canonicalOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

type retryTransport struct {
	base     http.RoundTripper
	attempts int
	baseWait time.Duration
	maxWait  time.Duration
	sleep    func(context.Context, time.Duration) error
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A body that cannot be replayed (no GetBody) makes retries unsafe.
	// http.NoBody is an explicit empty body and is safe to reuse.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return t.base.RoundTrip(req)
	}

	var lastErr error
	for attempt := 0; attempt < t.attempts; attempt++ {
		// The first attempt sends the original body, so the underlying
		// transport owns and closes it. Retries get a fresh copy from GetBody.
		attemptReq := req
		if attempt > 0 {
			attemptReq = req.Clone(req.Context())
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("sipgate: replay request body: %w", err)
				}
				attemptReq.Body = body
			}
		}

		resp, err := t.base.RoundTrip(attemptReq)
		lastErr = err

		retry, wait := shouldRetry(req, resp, err)
		if !retry || attempt == t.attempts-1 {
			return resp, err
		}
		if resp != nil {
			drainAndClose(resp.Body)
		}
		if wait <= 0 {
			wait = t.backoff(attempt)
		} else if wait > t.maxWait {
			// A malicious or misconfigured server must not be able to park a
			// request for an arbitrary amount of time.
			wait = t.maxWait
		}
		if sleepErr := t.sleep(req.Context(), wait); sleepErr != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, sleepErr
		}
	}
	return nil, lastErr
}

func (t *retryTransport) backoff(attempt int) time.Duration {
	wait := t.baseWait
	for i := 0; i < attempt; i++ {
		if wait >= t.maxWait/2 {
			return t.maxWait
		}
		wait *= 2
	}
	if wait > t.maxWait {
		return t.maxWait
	}
	return wait
}

func shouldRetry(req *http.Request, resp *http.Response, err error) (bool, time.Duration) {
	if req.Context().Err() != nil {
		return false, 0
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, 0
		}
		return isIdempotent(req.Method), 0
	}
	if resp == nil {
		return false, 0
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		wait, _ := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return true, wait
	case http.StatusServiceUnavailable:
		// 503 does not guarantee that the request was not processed. Retry it
		// only for methods that are safe to repeat, even when Retry-After is
		// present. This keeps POST (SMS, fax, calls) single-shot.
		if !isIdempotent(req.Method) {
			return false, 0
		}
		wait, _ := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return true, wait
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return isIdempotent(req.Method), 0
	default:
		return false, 0
	}
}

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

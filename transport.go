package sipgate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type authTransport struct {
	base        http.RoundTripper
	credentials credentials
	userAgent   string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if clone.Header.Get("Authorization") == "" {
		header, err := t.credentials.authorization(req.Context())
		if err != nil {
			return nil, err
		}
		clone.Header.Set("Authorization", header)
	}
	if clone.Header.Get("User-Agent") == "" {
		clone.Header.Set("User-Agent", t.userAgent)
	}
	return t.base.RoundTrip(clone)
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
	if req.Body != nil && req.GetBody == nil {
		return t.base.RoundTrip(req)
	}

	var lastErr error
	for attempt := 0; attempt < t.attempts; attempt++ {
		attemptReq := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("sipgate: replay request body: %w", err)
			}
			attemptReq.Body = body
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
		if wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return true, wait
		}
		return isIdempotent(req.Method), 0
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

package sipgate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mheers/sipgate-golang/openapi"
)

// APIError is returned for non-2xx responses of the sipgate API. It contains
// the HTTP status and, when present, the RFC 7807 style error fields used by
// sipgate.
type APIError struct {
	// StatusCode is the HTTP status code of the response.
	StatusCode int
	// Title is the short error summary returned by sipgate.
	Title string
	// Detail is the human readable explanation returned by sipgate.
	Detail string
	// Body is the raw response body.
	Body []byte
}

// Error implements the error interface.
func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("sipgate: ")
	if e.StatusCode == 0 {
		b.WriteString("request failed")
	} else {
		b.WriteString(strconv.Itoa(e.StatusCode))
		if text := http.StatusText(e.StatusCode); text != "" {
			b.WriteString(" ")
			b.WriteString(text)
		}
	}

	switch {
	case e.Title != "" && e.Detail != "" && e.Detail != e.Title:
		fmt.Fprintf(&b, ": %s (%s)", e.Title, e.Detail)
	case e.Title != "":
		b.WriteString(": ")
		b.WriteString(e.Title)
	case e.Detail != "":
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// AsAPIError extracts an *APIError from err.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsUnauthorized reports whether err is an API error with status 401.
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether err is an API error with status 403.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

// IsNotFound reports whether err is an API error with status 404.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is an API error with status 409.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsRateLimited reports whether err is an API error with status 429.
func IsRateLimited(err error) bool { return hasStatus(err, http.StatusTooManyRequests) }

// IsServerError reports whether err is an API error with a 5xx status.
func IsServerError(err error) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.StatusCode >= 500
}

func hasStatus(err error, status int) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.StatusCode == status
}

type errorPayload struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

type errorEnvelope struct {
	Errors []errorPayload `json:"errors"`
}

func errorFromResponse(resp *http.Response) error {
	if resp == nil {
		return errors.New("sipgate: request failed without a response")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		return &APIError{StatusCode: resp.StatusCode, Detail: "could not read error response: " + readErr.Error()}
	}
	return newAPIError(resp.StatusCode, body)
}

func newAPIError(statusCode int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: statusCode, Body: body}

	var envelope errorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Errors) > 0 {
		payload := envelope.Errors[0]
		apiErr.Title = payload.Title
		apiErr.Detail = payload.Detail
		if apiErr.StatusCode == 0 && payload.Status != 0 {
			apiErr.StatusCode = payload.Status
		}
		return apiErr
	}

	var single errorPayload
	if err := json.Unmarshal(body, &single); err == nil && (single.Title != "" || single.Detail != "") {
		apiErr.Title = single.Title
		apiErr.Detail = single.Detail
		if apiErr.StatusCode == 0 && single.Status != 0 {
			apiErr.StatusCode = single.Status
		}
		return apiErr
	}

	if detail := strings.TrimSpace(string(body)); detail != "" {
		const maxDetail = 4096
		if len(detail) > maxDetail {
			detail = detail[:maxDetail] + "..."
		}
		apiErr.Detail = detail
	}
	return apiErr
}

// wrapError converts the error returned by a generated API call into an
// *APIError and leaves other errors untouched.
func wrapError(resp *http.Response, err error) error {
	if err == nil {
		if resp != nil && resp.StatusCode >= http.StatusBadRequest {
			return errorFromResponse(resp)
		}
		return nil
	}

	if resp != nil && resp.StatusCode >= http.StatusBadRequest {
		return errorFromResponse(resp)
	}

	var generic *openapi.GenericOpenAPIError
	if errors.As(err, &generic) {
		return newAPIError(0, generic.Body())
	}
	return err
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		wait := when.Sub(now)
		if wait < 0 {
			wait = 0
		}
		return wait, true
	}
	return 0, false
}

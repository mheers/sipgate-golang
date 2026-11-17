// Package webhook parses and verifies sipgate Push API requests.
//
// sipgate sends webhooks as application/x-www-form-urlencoded POST requests.
// The request body is signed with RSA-SHA256 in the X-Sipgate-Signature header:
//
//	func handle(w http.ResponseWriter, r *http.Request) {
//		event, body, err := webhook.ParseRequest(r)
//		if err != nil {
//			http.Error(w, err.Error(), http.StatusBadRequest)
//			return
//		}
//		if !webhook.VerifySignature(r.Header.Get("X-Sipgate-Signature"), body) {
//			http.Error(w, "invalid signature", http.StatusUnauthorized)
//			return
//		}
//		// React to event.Type, event.CallID, ...
//	}
//
// The signature protects the payload but not against replay. When running
// behind a reverse proxy that overwrites X-Forwarded-For, IsSipgateOrigin can
// additionally check that the request came from one of sipgate's Push API
// addresses. It is spoofable otherwise.
package webhook

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// MaxBodySize is the maximum accepted webhook body size (1 MiB).
const MaxBodySize = 1 << 20

// EventType is the type of a Push API event.
type EventType string

const (
	// EventNewCall signals an incoming or outgoing call that is ringing.
	EventNewCall EventType = "newCall"
	// EventAnswer signals that a call was answered.
	EventAnswer EventType = "answer"
	// EventHangup signals that a call ended.
	EventHangup EventType = "hangup"
	// EventDTMF signals DTMF digits entered by a caller.
	EventDTMF EventType = "dtmf"
)

// Direction is the direction of a call.
type Direction string

const (
	// DirectionIn is an incoming call.
	DirectionIn Direction = "in"
	// DirectionOut is an outgoing call.
	DirectionOut Direction = "out"
)

// HangupCause describes why a call ended.
type HangupCause string

const (
	CauseNormalClearing HangupCause = "normalClearing"
	CauseBusy           HangupCause = "busy"
	CauseCancel         HangupCause = "cancel"
	CauseNoAnswer       HangupCause = "noAnswer"
	CauseCongestion     HangupCause = "congestion"
	CauseNotFound       HangupCause = "notFound"
	CauseForwarded      HangupCause = "forwarded"
)

// Event is a parsed sipgate Push API request.
type Event struct {
	// Type is the event name.
	Type EventType
	// CallID identifies the call.
	CallID string
	// OriginalCallID identifies a call across transfers.
	OriginalCallID string
	// XCID is an additional correlation identifier.
	XCID string
	// From is the calling number or "anonymous".
	From string
	// To is the called number.
	To string
	// Direction is the call direction.
	Direction Direction
	// Cause is set for hangup events.
	Cause HangupCause
	// DTMF contains the digits entered by the caller.
	DTMF string
	// Users are the names of the involved sipgate users.
	Users []string
	// UserIDs are the IDs of the involved users, for example "w0".
	UserIDs []string
	// FullUserIDs are the full IDs of the involved users.
	FullUserIDs []string
	// AnsweringNumber is the number that answered the call.
	AnsweringNumber string
	// Diversion is set when the call was diverted before reaching sipgate.
	Diversion string
	// ChannelID is set by Push API version 2 for sipgate Neo accounts.
	ChannelID string
	// Raw contains all form values for fields that are not modeled yet.
	Raw url.Values
}

// ParseRequest parses a Push API HTTP request and returns the event together
// with the raw body. The raw body is needed for VerifySignature.
func ParseRequest(r *http.Request) (*Event, []byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, nil, fmt.Errorf("webhook: unexpected content type %q", r.Header.Get("Content-Type"))
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("webhook: read request body: %w", err)
	}
	if len(body) > MaxBodySize {
		return nil, nil, fmt.Errorf("webhook: request body exceeds %d bytes", MaxBodySize)
	}

	event, err := ParseForm(body)
	if err != nil {
		return nil, nil, err
	}
	return event, body, nil
}

// ParseForm parses a raw form encoded Push API body.
func ParseForm(body []byte) (*Event, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("webhook: parse form body: %w", err)
	}
	return ParseValues(values)
}

// ParseValues converts already parsed form values into an Event.
func ParseValues(values url.Values) (*Event, error) {
	eventName := strings.TrimSpace(values.Get("event"))
	if eventName == "" {
		return nil, errors.New("webhook: missing event parameter")
	}

	return &Event{
		Type:            EventType(eventName),
		CallID:          values.Get("callId"),
		OriginalCallID:  firstValue(values, "originalCallId", "origCallId"),
		XCID:            values.Get("xcid"),
		From:            values.Get("from"),
		To:              values.Get("to"),
		Direction:       Direction(values.Get("direction")),
		Cause:           HangupCause(values.Get("cause")),
		DTMF:            values.Get("dtmf"),
		Users:           collectValues(values, "user"),
		UserIDs:         collectValues(values, "userId"),
		FullUserIDs:     collectValues(values, "fullUserId"),
		AnsweringNumber: values.Get("answeringNumber"),
		Diversion:       values.Get("diversion"),
		ChannelID:       values.Get("channelId"),
		Raw:             values,
	}, nil
}

func firstValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			return value
		}
	}
	return ""
}

// collectValues collects a field that may be sent as user=, user[]=,
// user[0]= or user[][0]=. The iteration order is deterministic.
func collectValues(values url.Values, base string) []string {
	keys := make([]string, 0, 1)
	for key := range values {
		if formKeyBase(key) == base {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var collected []string
	for _, key := range keys {
		collected = append(collected, values[key]...)
	}
	return collected
}

func formKeyBase(key string) string {
	if i := strings.IndexByte(key, '['); i >= 0 {
		return key[:i]
	}
	return key
}

// VerifySignature checks the X-Sipgate-Signature header against the raw
// request body using sipgate's public key.
func VerifySignature(signature string, body []byte) bool {
	if signature == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(body)
	return rsa.VerifyPKCS1v15(sipgatePublicKey, crypto.SHA256, digest[:], decoded) == nil
}

// IsSipgateOrigin reports whether the request was forwarded by one of
// sipgate's Push API addresses, based on X-Forwarded-For.
//
// X-Forwarded-For is client-controlled unless the reverse proxy overwrites it.
// Treat this as a defense-in-depth signal, not as authentication; always verify
// the signature.
func IsSipgateOrigin(r *http.Request) bool {
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, raw := range strings.Split(header, ",") {
			ip := strings.TrimSpace(raw)
			for _, allowed := range sipgatePushIPs {
				if ip == allowed {
					return true
				}
			}
		}
	}
	return false
}

var sipgatePushIPs = []string{
	"217.116.118.254",
	"212.9.46.32",
}

// sipgatePublicKeyPEM is the Push API signing key published by sipgate in the
// official sipgateio-node library. If sipgate rotates the key, update it here
// and replace the webhook test vector.
const sipgatePublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAvFdlnqRfO5TmqzjtsFVC
qPz+oW+poi3lXQFvMJJMEAtlU4MbX+yuIkgnC+Qt3AQWIK+oWSEQiybg+p39wEMl
16PZxEzf9gONQvTg1XhyHnwqYUFQj/AoxPKGYC3jmdjZ224SrESiTN3CdxoOc3vk
cXiQstYE7SxnksPtzfk0aIOkicim5/tAP9izNQ18/zmX3ChjK2k72gf5dDlq+qZ8
pRiHAPZ64TySH6cLQDhaoh6Nbq8FEG3usXXPClbUMh2DZaTMPafpd+LNuTngJ/y+
KoZasVuFfXAeBqGA6l5d4Uedfr99Z6qjwDrJ8sU4z2Do9bJrbuqP+mOXEebhSI1K
vi8trEfrIt8hnDBGsBaB9NEO3kYxvDT2/PhC9fNYFocjEpZNd7/mUqTJDYlpa4UC
NDQaU+ATdXG8/P5sGAegks6MQTu+qe9XTO0CV48eswDkz6g3UbLOwmrJK59SjD3J
yAZ52sluwyDKRWw/PPTt8BTQLjvkdbiLUlc4ehJ4b1X7D7diYWHDPeFL47cAvaXh
dXVrZDeBJYibvD1jRx4ES0KlfhwzNXBYUoM/Movg9zMM2PzyHHKS//xIjwUC/FJ6
/6eykno0Hy2B1ev6s3hdY6VkN2zd7Wf+EK4DxRliwsg1U99szFE3ewLfexF+Uag5
M+TfPtOUumI/NHMTvsRW9+ECAwEAAQ==
-----END PUBLIC KEY-----`

var sipgatePublicKey = mustParsePublicKey(sipgatePublicKeyPEM)

func mustParsePublicKey(pemData string) *rsa.PublicKey {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		panic("webhook: invalid embedded sipgate public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("webhook: parse embedded sipgate public key: " + err.Error())
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		panic("webhook: embedded sipgate public key is not RSA")
	}
	return rsaKey
}

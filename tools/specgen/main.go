// Command specgen downloads the sipgate API specification and normalizes it so
// that it can be consumed by OpenAPI Generator without validation errors.
//
// The upstream document is Swagger 2.0 and contains a handful of problems that
// are harmless in Swagger UI but break code generation:
//
//   - two operationIds are used twice,
//   - parameters carry a "deprecated" field, which Swagger 2.0 does not allow.
//
// Normalize fixes those issues deterministically. The downloaded document is
// checked in under spec/ so that code generation does not depend on the live
// sipgate API being reachable.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// SourceURL is the canonical Swagger 2.0 document of the sipgate API.
	SourceURL = "https://api.sipgate.com/v2/swagger.json"

	userAgent = "sipgate-golang-specgen/1.0"
)

// operationRenames resolves the duplicate operationIds in the upstream
// document with descriptive, stable names.
var operationRenames = map[string]string{
	"GET /{userId}/phonelines/{phonelineId}/voicemails":                          "getPhonelineVoicemails",
	"POST /{userId}/phonelines/{phonelineId}/voicemails/{voicemailId}/greetings": "addPhonelineVoicemailGreeting",
}

// Normalize turns the raw upstream Swagger document into a generator-friendly
// document. The result is deterministic: normalizing the same input twice
// yields byte-identical output.
func Normalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode swagger document: %w", err)
	}

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("swagger document has no paths object")
	}

	pathNames := make([]string, 0, len(paths))
	for name := range paths {
		pathNames = append(pathNames, name)
	}
	sort.Strings(pathNames)

	seen := make(map[string]struct{}, len(pathNames))
	for _, path := range pathNames {
		item, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		stripDeprecated(item["parameters"])

		methods := make([]string, 0, len(item))
		for method, rawOperation := range item {
			method = strings.ToLower(method)
			if _, ok := rawOperation.(map[string]any); !ok {
				continue
			}
			if !isHTTPMethod(method) {
				continue
			}
			methods = append(methods, method)
		}
		sort.Strings(methods)

		for _, method := range methods {
			op := item[method].(map[string]any)
			stripDeprecated(op["parameters"])

			id, _ := op["operationId"].(string)
			if id == "" {
				continue
			}

			key := strings.ToUpper(method) + " " + path
			if renamed, ok := operationRenames[key]; ok {
				id = renamed
			}

			if _, duplicate := seen[id]; duplicate {
				id = uniqueOperationID(id, tagOf(op), path, seen)
			}
			seen[id] = struct{}{}
			op["operationId"] = id
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode normalized document: %w", err)
	}
	return buf.Bytes(), nil
}

func isHTTPMethod(method string) bool {
	switch method {
	case "get", "put", "post", "delete", "options", "head", "patch":
		return true
	default:
		return false
	}
}

func stripDeprecated(raw any) {
	params, ok := raw.([]any)
	if !ok {
		return
	}
	for _, rawParam := range params {
		if param, ok := rawParam.(map[string]any); ok {
			delete(param, "deprecated")
		}
	}
}

func tagOf(op map[string]any) string {
	tags, ok := op["tags"].([]any)
	if !ok || len(tags) == 0 {
		return ""
	}
	tag, _ := tags[0].(string)
	return tag
}

// uniqueOperationID derives a readable, deterministic operationId for a
// duplicate: the first tag is used as a prefix, followed by the path. If that
// is still taken (for example for two operations with the same tag and a
// similar path), a numeric suffix is appended.
func uniqueOperationID(id, tag, path string, seen map[string]struct{}) string {
	base := id
	if tag != "" {
		base = lowerFirst(tag) + upperFirst(id)
	}
	candidate := base
	if _, exists := seen[candidate]; !exists {
		return candidate
	}

	suffix := pathSuffix(path)
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s%s%d", base, suffix, i)
		if _, exists := seen[candidate]; !exists {
			return candidate
		}
	}
}

// pathSuffix converts the static path segments into an upper camel case
// suffix, e.g. "/calls/{callId}/dtmf" becomes "CallsDtmf".
func pathSuffix(path string) string {
	parts := strings.Split(path, "/")
	var b strings.Builder
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "{") {
			continue
		}
		b.WriteString(upperFirst(part))
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func main() {
	var (
		source  = flag.String("source", SourceURL, "URL of the upstream Swagger document")
		out     = flag.String("out", "spec/sipgate-v2.json", "output file for the normalized document")
		timeout = flag.Duration("timeout", 60*time.Second, "HTTP timeout")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *source, nil)
	if err != nil {
		log.Fatalf("specgen: build request: %v", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("specgen: download %s: %v", *source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("specgen: download %s: unexpected status %s", *source, resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		log.Fatalf("specgen: read response: %v", err)
	}

	normalized, err := Normalize(raw)
	if err != nil {
		log.Fatalf("specgen: normalize: %v", err)
	}

	if dir := filepath.Dir(*out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("specgen: create output directory: %v", err)
		}
	}
	tmp := *out + ".tmp"
	if err := os.WriteFile(tmp, normalized, 0o644); err != nil {
		log.Fatalf("specgen: write %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, *out); err != nil {
		log.Fatalf("specgen: replace %s: %v", *out, err)
	}

	log.Printf("specgen: wrote %s (%d bytes)", *out, len(normalized))
}

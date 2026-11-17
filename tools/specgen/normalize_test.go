package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func minimalSpec(paths map[string]any) []byte {
	doc := map[string]any{
		"swagger": "2.0",
		"info":    map[string]any{"title": "test", "version": "1.0.0"},
		"paths":   paths,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

func mustNormalize(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return doc
}

func operationID(t *testing.T, doc map[string]any, path, method string) string {
	t.Helper()
	paths := doc["paths"].(map[string]any)
	item := paths[path].(map[string]any)
	op := item[method].(map[string]any)
	id, _ := op["operationId"].(string)
	return id
}

func TestNormalizeRenamesKnownDuplicateOperationIDs(t *testing.T) {
	raw := minimalSpec(map[string]any{
		"/voicemails": map[string]any{
			"get": map[string]any{"operationId": "getVoicemails"},
		},
		"/{userId}/phonelines/{phonelineId}/voicemails": map[string]any{
			"get": map[string]any{"operationId": "getVoicemails"},
		},
		"/autorecordings/greetings": map[string]any{
			"post": map[string]any{"operationId": "addGreeting"},
		},
		"/{userId}/phonelines/{phonelineId}/voicemails/{voicemailId}/greetings": map[string]any{
			"post": map[string]any{"operationId": "addGreeting"},
		},
	})

	doc := mustNormalize(t, raw)
	if got := operationID(t, doc, "/voicemails", "get"); got != "getVoicemails" {
		t.Errorf("first getVoicemails renamed to %q, want unchanged", got)
	}
	if got := operationID(t, doc, "/{userId}/phonelines/{phonelineId}/voicemails", "get"); got != "getPhonelineVoicemails" {
		t.Errorf("phoneline voicemails operationId = %q, want %q", got, "getPhonelineVoicemails")
	}
	if got := operationID(t, doc, "/{userId}/phonelines/{phonelineId}/voicemails/{voicemailId}/greetings", "post"); got != "addPhonelineVoicemailGreeting" {
		t.Errorf("phoneline greeting operationId = %q, want %q", got, "addPhonelineVoicemailGreeting")
	}
}

func TestNormalizeMakesRemainingDuplicatesUnique(t *testing.T) {
	raw := minimalSpec(map[string]any{
		"/a": map[string]any{"get": map[string]any{"operationId": "thing", "tags": []any{"alpha"}}},
		"/b": map[string]any{"get": map[string]any{"operationId": "thing", "tags": []any{"beta"}}},
		"/c": map[string]any{"get": map[string]any{"operationId": "thing", "tags": []any{"beta"}}},
	})

	doc := mustNormalize(t, raw)
	ids := []string{
		operationID(t, doc, "/a", "get"),
		operationID(t, doc, "/b", "get"),
		operationID(t, doc, "/c", "get"),
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("operationId became empty")
		}
		if seen[id] {
			t.Fatalf("duplicate operationId survived normalization: %v", ids)
		}
		seen[id] = true
	}
	if ids[0] != "thing" {
		t.Errorf("first occurrence = %q, want unchanged %q", ids[0], "thing")
	}
	if !strings.HasPrefix(ids[1], "beta") {
		t.Errorf("tag-qualified duplicate = %q, want beta prefix", ids[1])
	}
	if !strings.Contains(ids[2], "C") {
		t.Errorf("second duplicate = %q, want a path-derived suffix", ids[2])
	}
}

func TestNormalizeDropsUnsupportedParameterDeprecatedField(t *testing.T) {
	raw := minimalSpec(map[string]any{
		"/contacts": map[string]any{
			"get": map[string]any{
				"operationId": "getContacts",
				"parameters": []any{
					map[string]any{"name": "offset", "in": "query", "deprecated": true, "type": "integer"},
				},
			},
			"parameters": []any{
				map[string]any{"name": "limit", "in": "query", "deprecated": true, "type": "integer"},
			},
		},
	})

	doc := mustNormalize(t, raw)
	item := doc["paths"].(map[string]any)["/contacts"].(map[string]any)

	for _, key := range []string{"parameters"} {
		for _, rawParam := range item[key].([]any) {
			if _, ok := rawParam.(map[string]any)["deprecated"]; ok {
				t.Errorf("path-level parameter still has deprecated field")
			}
		}
	}
	op := item["get"].(map[string]any)
	for _, rawParam := range op["parameters"].([]any) {
		if _, ok := rawParam.(map[string]any)["deprecated"]; ok {
			t.Errorf("operation-level parameter still has deprecated field")
		}
	}
}

func TestNormalizeIsDeterministic(t *testing.T) {
	raw := minimalSpec(map[string]any{
		"/a": map[string]any{"get": map[string]any{"operationId": "thing"}},
		"/b": map[string]any{"get": map[string]any{"operationId": "thing"}},
	})

	first, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("Normalize is not deterministic")
	}
}

func TestNormalizeRejectsInvalidJSON(t *testing.T) {
	if _, err := Normalize([]byte("{")); err == nil {
		t.Fatal("Normalize accepted invalid JSON")
	}
}

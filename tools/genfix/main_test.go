package main

import (
	"strings"
	"testing"
)

const collisionSource = `type ApiUrlsResponse struct {
	CallRestrictionsUrl              *string ` + "`json:\"callRestrictionsUrl,omitempty\"`" + `
	GetCallRestrictionsUrl           *string ` + "`json:\"getCallRestrictionsUrl,omitempty\"`" + `
}
`

func TestFixFieldCollision(t *testing.T) {
	fixed := fixFieldCollision(collisionSource)

	if strings.Contains(fixed, "GetCallRestrictionsUrl           *string") {
		t.Error("colliding field was not renamed")
	}
	if !strings.Contains(fixed, "CallRestrictionsLookupUrl") {
		t.Error("renamed field missing")
	}
	if !strings.Contains(fixed, `json:"getCallRestrictionsUrl,omitempty"`) {
		t.Error("JSON name changed")
	}
	if !strings.Contains(fixed, `json:"callRestrictionsUrl,omitempty"`) {
		t.Error("sibling JSON name changed")
	}
}

func TestFixFieldCollisionIsIdempotent(t *testing.T) {
	once := fixFieldCollision(collisionSource)
	twice := fixFieldCollision(once)
	if once != twice {
		t.Error("fixFieldCollision is not idempotent")
	}
}

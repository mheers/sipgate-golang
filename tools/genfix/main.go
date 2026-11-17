package main

import (
	"os"
	"regexp"
	"strings"
)

// fieldDeclaration matches the struct field generated for the
// "getCallRestrictionsUrl" property of ApiUrlsResponse. The property name is
// unfortunate: its PascalCase form collides with the getter that OpenAPI
// Generator produces for the sibling property "callRestrictionsUrl".
var fieldDeclaration = regexp.MustCompile(`(?m)^(\s*)GetCallRestrictionsUrl(\s+)\*string`)

// fixFieldCollision renames the colliding struct field while keeping the JSON
// name intact. All generated receiver accesses use the "o." prefix, so they can
// be renamed safely; method names are left untouched.
func fixFieldCollision(src string) string {
	src = fieldDeclaration.ReplaceAllString(src, "${1}CallRestrictionsLookupUrl${2}*string")
	return strings.ReplaceAll(src, "o.GetCallRestrictionsUrl", "o.CallRestrictionsLookupUrl")
}

func main() {
	const path = "openapi/model_api_urls_response.go"
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fixed := fixFieldCollision(string(raw))
	if fixed == string(raw) {
		return
	}
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		panic(err)
	}
}

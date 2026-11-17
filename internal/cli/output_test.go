package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintJSON(t *testing.T) {
	var out bytes.Buffer
	app := &App{Out: &out, Output: "json"}
	if err := app.print(map[string]any{"company": "Love sipgate", "count": 2}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `"company": "Love sipgate"`) || !strings.HasPrefix(got, "{\n") {
		t.Errorf("output = %q", got)
	}
}

func TestPrintYAML(t *testing.T) {
	var out bytes.Buffer
	app := &App{Out: &out, Output: "yaml"}
	if err := app.print(map[string]any{"company": "Love sipgate"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "company: Love sipgate") {
		t.Errorf("output = %q", got)
	}
}

func TestPrintRejectsUnknownFormat(t *testing.T) {
	app := &App{Out: &bytes.Buffer{}, Output: "xml"}
	if err := app.print(map[string]any{}); err == nil {
		t.Fatal("print() accepted xml")
	}
}

func TestPrintString(t *testing.T) {
	var out bytes.Buffer
	app := &App{Out: &out, Output: "json"}
	app.printString("session-1")
	if got := out.String(); got != "session-1\n" {
		t.Errorf("output = %q", got)
	}
}

//go:build integration

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveReadOnly exercises the CLI against the real sipgate API. It only
// performs read-only requests and is skipped unless SIPGATE_TOKEN_ID and
// SIPGATE_TOKEN (or SIPGATE_ACCESS_TOKEN) are set.
//
// Run it with: make e2e
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv(envTokenID) == "" && os.Getenv(envAccessToken) == "" {
		t.Skip("no sipgate credentials in the environment")
	}

	app := NewApp()
	app.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	out := &bytes.Buffer{}
	app.Out = out
	app.Err = &bytes.Buffer{}

	commands := [][]string{
		{"account", "get"},
		{"account", "balance"},
		{"whoami"},
		{"numbers", "list"},
		{"users", "list"},
		{"contacts", "list", "--limit", "1"},
		{"addresses", "list"},
		{"devices", "list", "--user", "w0"},
		{"sms", "list", "--user", "w0"},
		{"history", "list", "--limit", "1"},
		{"calls", "list"},
		{"voicemails", "list"},
		{"webhooks", "get"},
		{"webhooks", "log"},
	}

	for _, args := range commands {
		out.Reset()
		if err := app.Run(context.Background(), args...); err != nil {
			t.Errorf("%s failed: %v (output: %s)", strings.Join(args, " "), err, out.String())
			continue
		}
		if out.Len() == 0 {
			t.Errorf("%s returned no output", strings.Join(args, " "))
		}
	}
}

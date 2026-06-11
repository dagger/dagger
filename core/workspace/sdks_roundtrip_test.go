package workspace

import (
	"strings"
	"testing"
)

func TestSDKsRoundTripFresh(t *testing.T) {
	cfg := &Config{
		Modules: map[string]ModuleEntry{
			"mymod": {Source: ".dagger/modules/mymod"},
		},
		SDKs: map[string]SDKEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				Pin:    "abcdef",
				Modules: []SDKManagedModule{
					{Path: ".dagger/modules/mymod"},
					{Path: "libs/shared"},
				},
				Clients: []SDKManagedClient{
					{Path: "./lib/cli", Module: ".dagger/modules/cli"},
				},
				Settings: map[string]any{"strict-build": true},
			},
		},
	}

	raw := SerializeConfig(cfg)
	t.Logf("=== SerializeConfig ===\n%s", raw)

	parsed, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := parsed.SDKs["go-sdk"]; got.Source != "github.com/dagger/go-sdk" {
		t.Errorf("Source: got %q", got.Source)
	}
	if got := parsed.SDKs["go-sdk"]; got.Pin != "abcdef" {
		t.Errorf("Pin: got %q", got.Pin)
	}
	if got := len(parsed.SDKs["go-sdk"].Modules); got != 2 {
		t.Errorf("Modules count: got %d", got)
	}
	if got := len(parsed.SDKs["go-sdk"].Clients); got != 1 {
		t.Errorf("Clients count: got %d", got)
	}

	updated, err := UpdateConfigBytes(raw, parsed)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== UpdateConfigBytes round-trip ===\n%s", updated)

	if !strings.Contains(string(updated), "[sdks.go-sdk]") {
		t.Errorf("missing [sdks.go-sdk] in round-trip output")
	}
	if !strings.Contains(string(updated), "[[sdks.go-sdk.modules]]") {
		t.Errorf("missing [[sdks.go-sdk.modules]] in round-trip output")
	}
}

// TestSDKsPreservesCommentsOutsideSDKs verifies that comments and formatting
// in non-SDK sections survive a write that touches SDKs.
func TestSDKsPreservesCommentsOutsideSDKs(t *testing.T) {
	original := []byte(`# Top-level comment
ignore = ["*.bak"]

# Modules section
[modules.mymod]
source = ".dagger/modules/mymod"  # inline comment

[sdks.stale-sdk]
source = "github.com/old/sdk"
`)

	cfg, err := ParseConfig(original)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// Replace SDKs entirely.
	cfg.SDKs = map[string]SDKEntry{
		"new-sdk": {
			Source: "github.com/new/sdk",
			Modules: []SDKManagedModule{
				{Path: ".dagger/modules/mymod"},
			},
		},
	}

	out, err := UpdateConfigBytes(original, cfg)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== Updated ===\n%s", out)

	s := string(out)
	if !strings.Contains(s, "# Top-level comment") {
		t.Errorf("lost top-level comment")
	}
	if !strings.Contains(s, "# Modules section") {
		t.Errorf("lost module section comment")
	}
	if !strings.Contains(s, "# inline comment") {
		t.Errorf("lost inline comment")
	}
	if strings.Contains(s, "stale-sdk") {
		t.Errorf("stale SDK survived removal")
	}
	if !strings.Contains(s, "[sdks.new-sdk]") {
		t.Errorf("new SDK missing from output")
	}
	if !strings.Contains(s, "[[sdks.new-sdk.modules]]") {
		t.Errorf("new SDK modules array missing")
	}
}

// TestSDKsRemovedWhenEmpty checks that an emptied SDKs map clears the section.
func TestSDKsRemovedWhenEmpty(t *testing.T) {
	original := []byte(`[modules.mymod]
source = ".dagger/modules/mymod"

[sdks.go-sdk]
source = "github.com/dagger/go-sdk"

[[sdks.go-sdk.modules]]
path = ".dagger/modules/mymod"
`)

	cfg, err := ParseConfig(original)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.SDKs = nil

	out, err := UpdateConfigBytes(original, cfg)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== Updated (sdks removed) ===\n%s", out)

	if strings.Contains(string(out), "sdks") {
		t.Errorf("sdks section should be gone, got:\n%s", out)
	}
	if !strings.Contains(string(out), "[modules.mymod]") {
		t.Errorf("modules section should still be present")
	}
}

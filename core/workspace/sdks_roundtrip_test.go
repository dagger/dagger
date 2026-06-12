package workspace

import (
	"strings"
	"testing"
)

// TestModuleAsSDKRoundTripFresh covers a freshly-serialized config that
// declares an SDK install via the as-sdk sub-table.
func TestModuleAsSDKRoundTripFresh(t *testing.T) {
	cfg := &Config{
		Modules: map[string]ModuleEntry{
			"mymod": {Source: ".dagger/modules/mymod"},
			"go-sdk": {
				Source:   "github.com/dagger/go-sdk",
				Pin:      "abcdef",
				Settings: map[string]any{"strict-build": true},
				AsSDK: &ModuleAsSDK{
					Modules: []SDKManagedModule{
						{Path: ".dagger/modules/mymod"},
						{Path: "libs/shared"},
					},
					Clients: []SDKManagedClient{
						{
							Path:   "./lib/cli",
							Module: ".dagger/modules/cli",
							Pin:    "123456",
							Options: map[string]string{
								"package-name": "@my-app/dagger-cli-client",
							},
						},
					},
				},
			},
		},
	}

	raw := SerializeConfig(cfg)
	t.Logf("=== SerializeConfig ===\n%s", raw)

	parsed, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	goSDK, ok := parsed.Modules["go-sdk"]
	if !ok {
		t.Fatalf("go-sdk module missing from parsed config")
	}
	if goSDK.Source != "github.com/dagger/go-sdk" {
		t.Errorf("Source: got %q", goSDK.Source)
	}
	if goSDK.Pin != "abcdef" {
		t.Errorf("Pin: got %q", goSDK.Pin)
	}
	if goSDK.AsSDK == nil {
		t.Fatalf("AsSDK is nil after round-trip")
	}
	if got := len(goSDK.AsSDK.Modules); got != 2 {
		t.Errorf("Modules count: got %d", got)
	}
	if got := len(goSDK.AsSDK.Clients); got != 1 {
		t.Errorf("Clients count: got %d", got)
	}
	if got := goSDK.AsSDK.Clients[0].Pin; got != "123456" {
		t.Errorf("Client pin: got %q", got)
	}
	if got := goSDK.AsSDK.Clients[0].Options["package-name"]; got != "@my-app/dagger-cli-client" {
		t.Errorf("Client package-name option: got %q", got)
	}

	updated, err := UpdateConfigBytes(raw, parsed)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== UpdateConfigBytes round-trip ===\n%s", updated)

	for _, want := range []string{
		"[modules.go-sdk]",
		"[modules.go-sdk.settings]",
		"[[modules.go-sdk.as-sdk.modules]]",
		"[[modules.go-sdk.as-sdk.clients]]",
		`pin = "123456"`,
		`package-name = "@my-app/dagger-cli-client"`,
	} {
		if !strings.Contains(string(updated), want) {
			t.Errorf("missing %q in round-trip output", want)
		}
	}
}

// TestModuleAsSDKPreservesCommentsOutside verifies that comments and
// formatting in non-as-sdk regions survive a write that touches as-sdk.
func TestModuleAsSDKPreservesCommentsOutside(t *testing.T) {
	original := []byte(`# Top-level comment
ignore = ["*.bak"]

# Modules section
[modules.mymod]
source = ".dagger/modules/mymod"  # inline comment

[modules.go-sdk]
source = "github.com/old/go-sdk"

[[modules.go-sdk.as-sdk.modules]]
path = ".dagger/modules/stale"
`)

	cfg, err := ParseConfig(original)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// Replace the SDK's as-sdk content entirely.
	entry := cfg.Modules["go-sdk"]
	entry.AsSDK = &ModuleAsSDK{
		Modules: []SDKManagedModule{
			{Path: ".dagger/modules/mymod"},
		},
	}
	cfg.Modules["go-sdk"] = entry

	out, err := UpdateConfigBytes(original, cfg)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== Updated ===\n%s", out)

	s := string(out)
	for _, want := range []string{
		"# Top-level comment",
		"# Modules section",
		"# inline comment",
		"[modules.go-sdk]",
		`source = "github.com/old/go-sdk"`,
		`path = ".dagger/modules/mymod"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "stale") {
		t.Errorf("stale as-sdk entry survived")
	}
}

// TestModuleAsSDKRemovedWhenEmpty checks that clearing AsSDK from a module
// entry drops the corresponding as-sdk array-of-tables on next write.
func TestModuleAsSDKRemovedWhenEmpty(t *testing.T) {
	original := []byte(`[modules.mymod]
source = ".dagger/modules/mymod"

[modules.go-sdk]
source = "github.com/dagger/go-sdk"

[[modules.go-sdk.as-sdk.modules]]
path = ".dagger/modules/mymod"
`)

	cfg, err := ParseConfig(original)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	entry := cfg.Modules["go-sdk"]
	entry.AsSDK = nil
	cfg.Modules["go-sdk"] = entry

	out, err := UpdateConfigBytes(original, cfg)
	if err != nil {
		t.Fatalf("UpdateConfigBytes: %v", err)
	}
	t.Logf("=== Updated (as-sdk removed) ===\n%s", out)

	if strings.Contains(string(out), "as-sdk") {
		t.Errorf("as-sdk should be gone, got:\n%s", out)
	}
	if !strings.Contains(string(out), "[modules.go-sdk]") {
		t.Errorf("the go-sdk module install should still be present")
	}
	if !strings.Contains(string(out), "[modules.mymod]") {
		t.Errorf("the mymod module should still be present")
	}
}

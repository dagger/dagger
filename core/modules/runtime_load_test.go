package modules

import (
	"strings"
	"testing"
)

func TestRuntimeFieldRoundTrip(t *testing.T) {
	// New TOML format: scalar source under [runtime] table, no Config/Debug/Experimental.
	cfg := &ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "api",
			SDK:  &SDK{Source: "go", Pin: "sha256:abc"},
		},
	}
	data, err := MarshalModuleConfigForFormat(cfg, ConfigFormatCurrent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)
	t.Logf("=== marshaled ===\n%s", out)

	for _, want := range []string{
		`[runtime]`,
		`source = "go"`,
		`pin = "sha256:abc"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, dropped := range []string{`config`, `debug`, `experimental`} {
		if strings.Contains(out, dropped) {
			t.Errorf("unexpectedly present: %q in:\n%s", dropped, out)
		}
	}

	parsed, err := ParseModuleConfigForFormat(data, ConfigFormatCurrent)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.SDK == nil || parsed.SDK.Source != "go" {
		t.Errorf("Source: %+v", parsed.SDK)
	}
	if parsed.SDK.Pin != "sha256:abc" {
		t.Errorf("Pin: got %q", parsed.SDK.Pin)
	}
}

func TestRuntimeFieldExternalRef(t *testing.T) {
	cfg := &ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "api",
			SDK:  &SDK{Source: "github.com/dagger/go-sdk", Pin: "sha256:def"},
		},
	}
	data, err := MarshalModuleConfigForFormat(cfg, ConfigFormatCurrent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := ParseModuleConfigForFormat(data, ConfigFormatCurrent)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.SDK.Source != "github.com/dagger/go-sdk" {
		t.Errorf("Source: got %q", parsed.SDK.Source)
	}
	if parsed.SDK.Pin != "sha256:def" {
		t.Errorf("Pin: got %q", parsed.SDK.Pin)
	}
}

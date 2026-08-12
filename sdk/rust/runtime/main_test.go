package main

import (
	"os"
	"strings"
	"testing"

	"rust-sdk/internal/metadata"
)

func TestGenerationRequestKeepsAuthoringSemanticsInRust(t *testing.T) {
	descriptor := metadata.EngineSource{
		FormatVersion:    1,
		Repository:       "https://github.com/dagger/dagger",
		DaggerRevision:   strings.Repeat("a", 40),
		EngineVersion:    "1.0.0-beta.10",
		RustSDKVersion:   "1.0.0-beta.10",
		RustToolchain:    "1.97.1",
		CoreSchemaDigest: "sha256:" + strings.Repeat("b", 64),
		SDKDependency: metadata.PublishedSDKDependency{
			Source:       "registry",
			Registry:     "crates-io",
			Package:      "dagger-sdk",
			ExactVersion: "1.0.0-beta.10",
		},
	}
	request := generationRequestDocument(
		descriptor,
		"generate-module",
		scopedModuleIdentity{
			Name:          "fixture",
			OriginalName:  "Fixture",
			SourceSubpath: "workspace/module",
			SourceDigest:  metadata.DigestModuleSource("fixture"),
			ConfigFormat:  "current",
		},
		[]byte(`{"__schema":{"types":[]}}`),
		"workspace/module",
	)
	body := request["request"].(map[string]any)
	for _, forbidden := range []string{
		"entrypoint_type_defs",
		"source_snapshot",
		"descriptor",
		"registration",
		"codec",
		"dispatch",
		"defaults",
		"state",
		"metadata_policy",
		"evidence",
	} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("Go request unexpectedly owns Rust semantic field %q", forbidden)
		}
	}
	if body["operation"] != "generate-module" || body["output_root"] != "workspace/module" {
		t.Fatalf("closed operation selectors were not forwarded exactly: %#v", body)
	}
}

func TestAdapterSourceContainsNoRustBehaviouralModel(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read adapter source: %v", err)
	}
	for _, forbidden := range []string{
		"ModuleDescriptor",
		"RegistrationProjection",
		"CallEnvelope",
		"DispatchRegistry",
		"ModuleInputCodec",
		"ModuleOutputCodec",
		"dagger(function",
		"dagger(object",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("Go adapter contains Rust behavioural model token %q", forbidden)
		}
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
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

func TestProperty19ModernLegacyGenerationSemanticallyConverge(t *testing.T) {
	descriptor := metadata.EngineSource{
		FormatVersion: 1, Repository: "https://github.com/dagger/dagger",
		DaggerRevision: strings.Repeat("a", 40), EngineVersion: "1.0.0-beta.10",
		RustSDKVersion: "1.0.0-beta.10", RustToolchain: "1.97.1",
		CoreSchemaDigest: "sha256:" + strings.Repeat("b", 64),
		SDKDependency: metadata.PublishedSDKDependency{
			Source: "registry", Registry: "crates-io", Package: "dagger-sdk", ExactVersion: "1.0.0-beta.10",
		},
	}
	for seed := 0; seed < 256; seed++ {
		pin := ""
		if seed%2 == 0 {
			pin = strings.Repeat(fmt.Sprintf("%x", seed%16), 40)
		}
		identity := scopedModuleIdentity{
			Name: fmt.Sprintf("module-%d", seed), OriginalName: fmt.Sprintf("Module %d", seed),
			SourceSubpath: fmt.Sprintf("workspace/module-%d", seed),
			SourceDigest:  metadata.DigestModuleSource(fmt.Sprintf("source-%d", seed)),
			ConfigFormat:  "current", ResolvedPin: pin,
		}
		schema := []byte(fmt.Sprintf(`{"fixture":%d}`, seed))
		modern := generationRequestDocument(descriptor, "generate-client", identity, schema, "workspace/client")
		identity.ConfigFormat = "legacy"
		legacy := generationRequestDocument(descriptor, "generate-client", identity, schema, "workspace/client")
		if reflect.DeepEqual(modern, legacy) {
			t.Fatalf("adapter variants did not exercise their control-path distinction for seed %d", seed)
		}
		if !reflect.DeepEqual(clientGenerationSemantics(modern), clientGenerationSemantics(legacy)) {
			t.Fatalf("equivalent adapters diverged for seed %d", seed)
		}
	}
}

func clientGenerationSemantics(document map[string]any) map[string]any {
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		panic(err)
	}
	request := normalized["request"].(map[string]any)
	module := request["module"].(map[string]any)
	delete(module, "config_format")
	return normalized
}

func TestClientPackageNameIsDeterministicAndConfined(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"clients/example", "example"},
		{"clients/my_client", "my-client"},
		{".", "dagger-client"},
	} {
		got, err := clientPackageName(test.path)
		if err != nil || got != test.want {
			t.Fatalf("clientPackageName(%q) = %q, %v; want %q", test.path, got, err, test.want)
		}
	}
	for _, invalid := range []string{"../escape", "/absolute", "bad path"} {
		if _, err := clientPackageName(invalid); err == nil {
			t.Fatalf("clientPackageName(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestInitClientRequestDoesNotForwardModuleReference(t *testing.T) {
	descriptor := metadata.EngineSource{
		FormatVersion: 1, Repository: "https://github.com/dagger/dagger",
		DaggerRevision: strings.Repeat("a", 40), EngineVersion: "1.0.0-beta.10",
		RustSDKVersion: "1.0.0-beta.10", RustToolchain: "1.97.1",
		CoreSchemaDigest: "sha256:" + strings.Repeat("b", 64),
		SDKDependency: metadata.PublishedSDKDependency{
			Source: "registry", Registry: "crates-io", Package: "dagger-sdk", ExactVersion: "1.0.0-beta.10",
		},
	}
	request := clientInitializationRequestDocument(descriptor, "workspace/clients/example", "example")
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode initialization request: %v", err)
	}
	for _, forbidden := range []string{"module_ref", "moduleRef", "github.com/private", "Authorization"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("initialization request forwarded forbidden input %q", forbidden)
		}
	}
	body := request["request"].(map[string]any)
	if body["client_root"] != "workspace/clients/example" || body["package_name"] != "example" {
		t.Fatalf("initialization identity was not forwarded exactly: %#v", body)
	}
}

func TestCheckedRuntimeAPIExposesInitClientExactlyOnce(t *testing.T) {
	entrypoint, err := os.ReadFile("dagger.gen.go")
	if err != nil {
		t.Fatalf("read checked module entrypoint: %v", err)
	}
	client, err := os.ReadFile("internal/dagger/rust-sdk.gen.go")
	if err != nil {
		t.Fatalf("read checked module API client: %v", err)
	}
	for token, source := range map[string][]byte{
		`case "InitClient":`:                               entrypoint,
		`dag.Function("InitClient",`:                       entrypoint,
		`func (r *RustSDK) InitClient(ws *Workspace, path`: client,
	} {
		if count := strings.Count(string(source), token); count != 1 {
			t.Fatalf("checked runtime API contains %q %d times; want exactly once", token, count)
		}
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

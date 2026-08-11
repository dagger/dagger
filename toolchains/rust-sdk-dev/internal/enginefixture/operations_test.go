package enginefixture

import (
	"reflect"
	"testing"
)

const installedWorkspace = `[modules.dagger-rust-sdk]
source = "rust"

[modules.dagger-rust-sdk.as-sdk]
name = "rust"

[[modules.dagger-rust-sdk.as-sdk.modules]]
path = ".dagger/modules/operations"
`

func TestOperationsPlanSeparatesCheckedGenerationFromSchemaLoading(t *testing.T) {
	t.Parallel()

	plan, err := NewOperationsPlan(installedWorkspace, "v1.0.0-beta.10")
	if err != nil {
		t.Fatalf("construct operations plan: %v", err)
	}
	wantGenerate := []string{
		"dagger", "api", "call", "-M",
		"module-source", "--ref-string", operationsSchemaModule, "--require-kind", "LOCAL_SOURCE",
		"generated-context-changeset", "export", "--path", operationsSchemaModule,
	}
	if !reflect.DeepEqual(plan.GenerateClientArgs, wantGenerate) {
		t.Fatalf("unexpected client generator command: %v", plan.GenerateClientArgs)
	}
	if len(plan.Files) != 2 || plan.Files[0].Path != operationsSchemaConfig || plan.Files[1].Path != operationsSchemaSource {
		t.Fatalf("unexpected schema fixture files: %+v", plan.Files)
	}
	if !contains(plan.RequiredPaths, operationsModuleManifest) ||
		!contains(plan.RequiredPaths, operationsClientRoot+"/Cargo.toml") ||
		!contains(plan.RequiredPaths, operationsClientRoot+"/.dagger/rust/operation-manifest.json") {
		t.Fatalf("required products omit module or client ownership: %v", plan.RequiredPaths)
	}
}

func TestOperationsPlanRejectsInvalidPreconditions(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		config  string
		version string
	}{
		"empty version": {config: installedWorkspace},
		"missing SDK":   {config: "[modules.other]\nsource = \"go\"\n", version: "v1.0.0-beta.10"},
		"malformed TOML": {
			config:  "[modules.dagger-rust-sdk\n",
			version: "v1.0.0-beta.10",
		},
	} {
		fixture := fixture
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewOperationsPlan(fixture.config, fixture.version); err == nil {
				t.Fatalf("invalid operations fixture unexpectedly succeeded")
			}
		})
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

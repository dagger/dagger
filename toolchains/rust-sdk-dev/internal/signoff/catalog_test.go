package signoff

import (
	"os"
	"strings"
	"testing"
)

func TestFixedRegistryMatchesTheRustCatalogExactly(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-cases.json")
	if err != nil {
		t.Fatalf("read canonical case catalog: %v", err)
	}
	programs, err := DecodeFixedPrograms(data)
	if err != nil {
		t.Fatalf("decode canonical fixed programs: %v", err)
	}
	registry := FixedProgramRegistry()
	if len(programs) != 60 || len(registry) != 60 {
		t.Fatalf("fixed program count: catalog=%d registry=%d, want 60", len(programs), len(registry))
	}
	counts := map[ProgramKind]int{}
	for _, program := range programs {
		counts[program.Kind]++
		spec, ok := registry[program.Key()]
		if !ok || spec.Program != program {
			t.Fatalf("catalog program %q has no exact production route", program.Key())
		}
		if program.Value == "init-module-renders-root-type" {
			t.Fatalf("the common harness self-check entered the subject inventory")
		}
	}
	want := map[ProgramKind]int{
		ProgramCommonHarness:     17,
		ProgramStableConnector:   1,
		ProgramCoreShape:         9,
		ProgramEngineIntegration: 10,
		ProgramModuleAuthoring:   9,
		ProgramStandaloneClient:  5,
		ProgramDefinitiveGo:      9,
	}
	for kind, expected := range want {
		if counts[kind] != expected {
			t.Fatalf("program family %q count: got %d, want %d", kind, counts[kind], expected)
		}
	}
}

func TestProgramDecoderRejectsUnknownAndAmbiguousSelectors(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"program":"shell","case":"anything"}`,
		`{"program":"core-shape","shape":"scalar","case":"extra"}`,
		`{"program":"stable-connector","case":"extra"}`,
		`{"program":"engine-integration"}`,
		`{"program":"core-shape","shape":"scalar","command":"cargo run"}`,
	} {
		if _, err := decodeProgram([]byte(input)); err == nil {
			t.Fatalf("accepted forbidden fixed program %s", input)
		}
	}
}

func TestExternalWorkspacePolicyIsLimitedToPackagedConsumers(t *testing.T) {
	t.Parallel()
	for _, spec := range FixedProgramRegistry() {
		external := spec.Workspace == WorkspaceExternalPackage
		wantExternal := spec.Program.Kind == ProgramStandaloneClient ||
			(spec.Program.Kind == ProgramModuleAuthoring && spec.Program.Value == "packaged-self-consumer")
		if external != wantExternal {
			t.Fatalf("program %q external workspace policy: got %v, want %v", spec.Program.Key(), external, wantExternal)
		}
	}
}

func TestCoreAndDefinitiveClientRoutesUsePublicRustFixtureOperations(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../testdata/core_conformance.rs")
	if err != nil {
		t.Fatalf("read public Rust conformance fixture: %v", err)
	}
	source := string(data)
	for program, anchor := range map[string]string{
		"scalar":                    "query.version().await?",
		"enum":                      "with_sharing(CacheSharingMode::Private)",
		"input":                     "DirectoryDockerBuildOpts::default()",
		"object":                    "query.container().from(\"alpine:3.22\")",
		"interface":                 "interface_id(&container).await?",
		"nullable":                  ".docker_healthcheck()",
		"list-object":               ".env_variables()",
		"expected-type":             "query.node(id).await?",
		"void":                      "query.engine().local_cache().prune().await?",
		"directory":                 ".docker_build_opts(&opts)",
		"git":                       "QueryGitOpts::default().with_keep_git_dir(false)",
		"container":                 "\"object\" | \"container\"",
		"container-mutation":        ".with_env_variable(\"RUST_CONFORMANCE_MUTATION\", \"retained\")",
		"list":                      ".env_variables()",
		"typed-exec-error":          "QueryError::Exec { error, .. }",
		"exec-error-output-fields":  "error.stdout(), Some(\"rust-stdout\")",
		"exec-error-empty-output":   "error.stdout(), Some(\"\")",
		"non-exec-error-separation": "QueryError::GraphQl { .. }",
	} {
		if !strings.Contains(source, anchor) {
			t.Fatalf("public Rust fixture route %q lacks anchor %q", program, anchor)
		}
	}
}

func TestConcreteExecutorRegistryIsClosedAndFullySpecified(t *testing.T) {
	t.Parallel()
	counts := map[ExecutorKind]int{}
	concrete := 0
	for _, spec := range FixedProgramRegistry() {
		if spec.Executor == nil {
			continue
		}
		concrete++
		executor := spec.Executor
		if executor.Selector != spec.Program.Value || executor.Expected.Category == "" || executor.Expected.Operation == "" {
			t.Fatalf("program %q has an incomplete executor: %#v", spec.Program.Key(), executor)
		}
		counts[executor.Kind]++
	}
	if concrete != 28 || counts[ExecutorCoreConformance] != 18 || counts[ExecutorEngineIntegration] != 10 {
		t.Fatalf("executor partition: total=%d kinds=%#v, want 28 as 18 core and 10 integration", concrete, counts)
	}
}

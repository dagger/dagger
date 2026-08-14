package signoff

import (
	"encoding/json"
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
	if len(programs) != 63 || len(registry) != 63 {
		t.Fatalf("fixed program count: catalog=%d registry=%d, want 63", len(programs), len(registry))
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
		ProgramStandaloneExample: 3,
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
		wantExternal := spec.Program.Kind == ProgramStandaloneClient || spec.Program.Kind == ProgramStandaloneExample ||
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
		selectorMatches := executor.Selector == spec.Program.Value ||
			(executor.Kind == ExecutorStandaloneExample && executor.Selector == spec.Program.Key()) ||
			(executor.Kind == ExecutorScenarioConformance && executor.Expected.Operation == executor.Selector)
		if !selectorMatches || executor.Expected.Category == "" || executor.Expected.Operation == "" {
			t.Fatalf("program %q has an incomplete executor: %#v", spec.Program.Key(), executor)
		}
		counts[executor.Kind]++
	}
	if concrete != 63 || counts[ExecutorCoreConformance] != 18 || counts[ExecutorEngineIntegration] != 10 || counts[ExecutorScenarioConformance] != 32 || counts[ExecutorStandaloneExample] != 3 {
		t.Fatalf("executor partition: total=%d kinds=%#v, want all 63 fixed programs concrete", concrete, counts)
	}
}

func TestCompleteCatalogRetainsAllRowsButRunsOnlyReviewedRustSuites(t *testing.T) {
	t.Parallel()
	observableBytes, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-observable-programs.json")
	if err != nil {
		t.Fatalf("read observable programs: %v", err)
	}
	observable, err := DecodeObservablePrograms(observableBytes)
	if err != nil {
		t.Fatalf("decode observable programs: %v", err)
	}
	registry, err := CompleteProgramRegistry(observable)
	if err != nil {
		t.Fatalf("complete program registry: %v", err)
	}
	realizationBytes, candidateBytes, runnerBytes := checkedScenarioInputs(t)
	realizations, err := DecodeScenarioRealizations(realizationBytes, candidateBytes, runnerBytes)
	if err != nil {
		t.Fatalf("decode scenario realizations: %v", err)
	}
	registry, err = ApplyScenarioRealizations(registry, observable, realizations)
	if err != nil {
		t.Fatalf("apply scenario realizations: %v", err)
	}
	if err := RequireConcretePrograms(registry); err != nil {
		t.Fatalf("require concrete programs: %v", err)
	}
	catalogBytes, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-cases.json")
	if err != nil {
		t.Fatalf("read case catalog: %v", err)
	}
	routes, err := DecodeCaseRoutes(catalogBytes, registry)
	if err != nil {
		t.Fatalf("decode case routes: %v", err)
	}
	groups, err := GroupCaseExecutions(routes, registry)
	if err != nil {
		t.Fatalf("group case executions: %v", err)
	}
	retained := 0
	standaloneGroups := 0
	for _, group := range groups {
		retained += len(group.Members)
		if group.Representative.Program.Kind == ProgramStandaloneClient {
			standaloneGroups++
			for _, member := range group.Members {
				if !sameExecutionPolicy(group.Representative.Policy, member.Policy) {
					t.Fatalf("standalone group crosses production policy: representative=%+v member=%+v", group.Representative, member)
				}
			}
		}
	}
	if len(routes) != 675 || retained != len(routes) || len(groups) != 74 {
		t.Fatalf("execution projection: routes=%d retained=%d groups=%d, want 675/675/74", len(routes), retained, len(groups))
	}
	if standaloneGroups != 2 {
		t.Fatalf("standalone selector must split engine-only and immutable-remote policy, got %d groups", standaloneGroups)
	}

	var authored catalogWire
	if err := json.Unmarshal(catalogBytes, &authored); err != nil {
		t.Fatalf("decode authored routes for projection fixture: %v", err)
	}
	executed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		executed[group.Representative.CaseID] = struct{}{}
	}
	projection := make([]facadeAdmissionRouteWire, len(routes))
	for index, route := range routes {
		item := authored.Cases[index]
		if item.ID != route.CaseID {
			t.Fatalf("authored and admitted route order differs at %d", index)
		}
		spec := registry[route.Program.Key()]
		_, selected := executed[route.CaseID]
		projection[index] = facadeAdmissionRouteWire{
			CaseID: route.CaseID, Program: item.Program, FixtureDigest: route.FixtureDigest, Boundary: spec.Boundary,
			ExecutionSelector: spec.Executor.Selector, Executed: selected,
			executionPolicyWire: item.executionPolicyWire,
		}
	}
	projectionBytes, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("encode Rust facade projection fixture: %v", err)
	}
	projectedRoutes, err := DecodeFacadeAdmissionRoutes(projectionBytes, registry)
	if err != nil {
		t.Fatalf("decode Rust facade projection: %v", err)
	}
	if len(projectedRoutes) != len(routes) {
		t.Fatalf("projected route count: got %d, want %d", len(projectedRoutes), len(routes))
	}
	projection[0].ExecutionSelector += "-substituted"
	mutated, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("encode mutated facade projection: %v", err)
	}
	if _, err := DecodeFacadeAdmissionRoutes(mutated, registry); err == nil {
		t.Fatalf("facade projection accepted a substituted execution selector")
	}
}

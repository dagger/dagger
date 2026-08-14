package signoff

// ProgramBoundary is the production API or lifecycle seam selected by a fixed program.
type ProgramBoundary string

const (
	BoundaryCommonHarness   ProgramBoundary = "common-harness-subject"
	BoundaryStableConnector ProgramBoundary = "stable-connector-distribution"
	BoundaryGeneratedCore   ProgramBoundary = "public-generated-core"
	BoundarySharedCLI       ProgramBoundary = "shared-baseline-cli"
	BoundaryModuleRuntime   ProgramBoundary = "production-module-dispatcher"
	BoundaryGeneratedClient ProgramBoundary = "public-generated-client"
	BoundaryStandaloneBuild ProgramBoundary = "standalone-example-build"
	BoundaryRustClient      ProgramBoundary = "public-rust-client"
	BoundaryPackagedRuntime ProgramBoundary = "exact-packaged-runtime"
)

// WorkspacePolicy states where mutable program state may be created.
type WorkspacePolicy string

const (
	WorkspaceBaselineBranch  WorkspacePolicy = "isolated-baseline-branch"
	WorkspaceExternalPackage WorkspacePolicy = "external-packaged-workspace"
)

// ExecutorKind is one closed production operation family used by exact-target sign-off.
type ExecutorKind string

const (
	// ExecutorCoreConformance runs the public generated Rust client and inspects one named result.
	ExecutorCoreConformance ExecutorKind = "rust-core-conformance"
	// ExecutorEngineIntegration runs one existing production engine-integration assertion.
	ExecutorEngineIntegration ExecutorKind = "rust-engine-integration"
	// ExecutorScenarioConformance runs one source-bound authority realization in idiomatic Rust.
	ExecutorScenarioConformance ExecutorKind = "rust-scenario-conformance"
	// ExecutorStandaloneExample builds one committed example and retains its actual local output.
	ExecutorStandaloneExample ExecutorKind = "rust-standalone-example-build"
)

// ObservationExpectation names the exact successful observation owned by an executor.
type ObservationExpectation struct {
	Category  string
	Operation string
}

// ExecutorDefinition fixes the production operation, selector, and expected result for one case.
type ExecutorDefinition struct {
	Kind           ExecutorKind
	Selector       string
	ContractDigest string
	ProofID        string
	Expected       ObservationExpectation
}

// ProgramSpec is one complete fixed production route.
type ProgramSpec struct {
	Program   Program
	Boundary  ProgramBoundary
	Workspace WorkspacePolicy
	Executor  *ExecutorDefinition
}

var commonHarnessChecks = []string{
	"deps-list-succeeds",
	"engine-required-reports-version",
	"generate-exposes-generator",
	"generate-respects-cwd",
	"generate-succeeds",
	"init-module-does-not-remove-existing-files",
	"init-module-does-not-write-config",
	"init-module-honors-custom-path",
	"init-module-seeds-files",
	"init-records-authoring-sdk",
	"init-registers-module",
	"init-scaffolds-module",
	"init-writes-module-config",
	"install-marks-as-sdk",
	"install-registers-sdk",
	"scaffolded-module-loads",
	"sdk-reports-module-options",
}

var coreShapes = []string{
	"scalar", "enum", "input", "object", "interface", "nullable", "list-object", "expected-type", "void",
}

var engineIntegrationPrograms = []string{
	"resolution", "init-empty", "init-existing", "init-no-generate", "operations",
	"runtime-checked", "runtime-legacy", "negative-generated-lock-toolchain",
	"negative-path-ownership", "negative-redaction",
}

var moduleAuthoringPrograms = []string{
	"registration", "constructor-state", "execution-shapes", "types", "handles-context",
	"negative-dispatch", "concurrency-cancellation", "packaged-self-consumer", "common-harness",
}

var standaloneClientPrograms = []string{
	"initialized-local-client", "pinned-remote-client", "schema-regeneration", "core-query", "namespaced-module-query",
}

var standaloneExamplePrograms = []string{"cli", "backend", "frontend"}

var definitiveGoPrograms = []string{
	"directory", "git", "container", "container-mutation", "list", "typed-exec-error",
	"exec-error-output-fields", "exec-error-empty-output", "non-exec-error-separation",
}

// FixedProgramRegistry returns the complete immutable 63-program production registry.
func FixedProgramRegistry() map[string]ProgramSpec {
	registry := make(map[string]ProgramSpec, 63)
	add := func(kind ProgramKind, values []string, boundary ProgramBoundary, workspace WorkspacePolicy) {
		for _, value := range values {
			program := Program{Kind: kind, Value: value}
			registry[program.Key()] = ProgramSpec{
				Program: program, Boundary: boundary, Workspace: workspace,
			}
		}
	}
	add(ProgramCommonHarness, commonHarnessChecks, BoundaryCommonHarness, WorkspaceBaselineBranch)
	connector := Program{Kind: ProgramStableConnector}
	registry[connector.Key()] = ProgramSpec{
		Program: connector, Boundary: BoundaryStableConnector, Workspace: WorkspaceBaselineBranch,
	}
	add(ProgramCoreShape, coreShapes, BoundaryGeneratedCore, WorkspaceBaselineBranch)
	add(ProgramEngineIntegration, engineIntegrationPrograms, BoundarySharedCLI, WorkspaceBaselineBranch)
	add(ProgramModuleAuthoring, moduleAuthoringPrograms, BoundaryModuleRuntime, WorkspaceBaselineBranch)
	packaged := Program{Kind: ProgramModuleAuthoring, Value: "packaged-self-consumer"}
	registry[packaged.Key()] = ProgramSpec{
		Program: packaged, Boundary: BoundaryModuleRuntime, Workspace: WorkspaceExternalPackage,
	}
	add(ProgramStandaloneClient, standaloneClientPrograms, BoundaryGeneratedClient, WorkspaceExternalPackage)
	add(ProgramStandaloneExample, standaloneExamplePrograms, BoundaryStandaloneBuild, WorkspaceExternalPackage)
	add(ProgramDefinitiveGo, definitiveGoPrograms, BoundaryRustClient, WorkspaceBaselineBranch)
	for key, definition := range concreteExecutorDefinitions() {
		spec := registry[key]
		spec.Executor = definition
		registry[key] = spec
	}
	return registry
}

func concreteExecutorDefinitions() map[string]*ExecutorDefinition {
	definitions := make(map[string]*ExecutorDefinition, 63)
	core := map[string]ObservationExpectation{
		"scalar":        {Category: "scalar", Operation: "Query.version"},
		"enum":          {Category: "enum", Operation: "Query.cacheVolume(sharing:)"},
		"input":         {Category: "input-object", Operation: "Directory.dockerBuild(buildArgs:)"},
		"object":        {Category: "lazy-object", Operation: "Query.container"},
		"interface":     {Category: "interface", Operation: "Container.id"},
		"nullable":      {Category: "nullable-handle", Operation: "Container.dockerHealthcheck"},
		"list-object":   {Category: "object-list", Operation: "Container.envVariables"},
		"expected-type": {Category: "expected-type-raw-id", Operation: "Query.node(id:)"},
		"void":          {Category: "void", Operation: "EngineCache.prune"},
	}
	goClient := map[string]ObservationExpectation{
		"directory":                 {Category: "input-object", Operation: "Directory.dockerBuild(buildArgs:)"},
		"git":                       {Category: "explicit-zero-like", Operation: "Query.git(keepGitDir:)"},
		"container":                 {Category: "lazy-object", Operation: "Query.container"},
		"container-mutation":        {Category: "object-mutation", Operation: "Container.withEnvVariable"},
		"list":                      {Category: "object-list", Operation: "Container.envVariables"},
		"typed-exec-error":          {Category: "engine-error", Operation: "Container.stdout"},
		"exec-error-output-fields":  {Category: "engine-error-fields", Operation: "Container.stdout"},
		"exec-error-empty-output":   {Category: "engine-error-empty-output", Operation: "Container.sync"},
		"non-exec-error-separation": {Category: "graphql-error", Operation: "Directory.entries"},
	}
	for value, expected := range core {
		program := Program{Kind: ProgramCoreShape, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorCoreConformance, Selector: value, Expected: expected,
		}
	}
	for value, expected := range goClient {
		program := Program{Kind: ProgramDefinitiveGo, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorCoreConformance, Selector: value, Expected: expected,
		}
	}
	for _, value := range engineIntegrationPrograms {
		program := Program{Kind: ProgramEngineIntegration, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorEngineIntegration, Selector: value,
			Expected: ObservationExpectation{Category: "case-pass", Operation: value},
		}
	}
	module := map[string]string{
		"registration":             "realization/module-registration",
		"constructor-state":        "realization/module-constructor-state",
		"execution-shapes":         "realization/module-execution-shapes",
		"types":                    "realization/module-types",
		"handles-context":          "realization/module-handles-context",
		"negative-dispatch":        "realization/module-negative-dispatch",
		"concurrency-cancellation": "realization/module-concurrency-cancellation",
		"packaged-self-consumer":   "realization/module-packaged-self-consumer",
		"common-harness":           "realization/module-common-harness",
	}
	for value, selector := range module {
		program := Program{Kind: ProgramModuleAuthoring, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorScenarioConformance, Selector: selector,
			Expected: ObservationExpectation{Category: string(RealizationReviewedFixture), Operation: selector},
		}
	}
	for _, value := range commonHarnessChecks {
		program := Program{Kind: ProgramCommonHarness, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorScenarioConformance, Selector: "realization/common-harness",
			Expected: ObservationExpectation{Category: string(RealizationReviewedFixture), Operation: "realization/common-harness"},
		}
	}
	connector := Program{Kind: ProgramStableConnector}
	definitions[connector.Key()] = &ExecutorDefinition{
		Kind: ExecutorScenarioConformance, Selector: "realization/stable-connector",
		Expected: ObservationExpectation{Category: string(RealizationReviewedFixture), Operation: "realization/stable-connector"},
	}
	for _, value := range standaloneClientPrograms {
		program := Program{Kind: ProgramStandaloneClient, Value: value}
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorScenarioConformance, Selector: "realization/standalone-clients",
			Expected: ObservationExpectation{Category: string(RealizationReviewedFixture), Operation: "realization/standalone-clients"},
		}
	}
	for _, value := range standaloneExamplePrograms {
		program := Program{Kind: ProgramStandaloneExample, Value: value}
		selector := "standalone-example/" + value
		definitions[program.Key()] = &ExecutorDefinition{
			Kind: ExecutorStandaloneExample, Selector: selector,
			Expected: ObservationExpectation{Category: "build-only-output", Operation: selector},
		}
	}
	return definitions
}

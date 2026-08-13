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
	BoundaryRustClient      ProgramBoundary = "public-rust-client"
	BoundaryPackagedRuntime ProgramBoundary = "exact-packaged-runtime"
)

// WorkspacePolicy states where mutable program state may be created.
type WorkspacePolicy string

const (
	WorkspaceBaselineBranch  WorkspacePolicy = "isolated-baseline-branch"
	WorkspaceExternalPackage WorkspacePolicy = "external-packaged-workspace"
)

// ProgramSpec is one complete fixed production route.
type ProgramSpec struct {
	Program   Program
	Boundary  ProgramBoundary
	Workspace WorkspacePolicy
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

var definitiveGoPrograms = []string{
	"directory", "git", "container", "container-mutation", "list", "typed-exec-error",
	"exec-error-output-fields", "exec-error-empty-output", "non-exec-error-separation",
}

// FixedProgramRegistry returns the complete immutable 60-program production registry.
func FixedProgramRegistry() map[string]ProgramSpec {
	registry := make(map[string]ProgramSpec, 60)
	add := func(kind ProgramKind, values []string, boundary ProgramBoundary, workspace WorkspacePolicy) {
		for _, value := range values {
			program := Program{Kind: kind, Value: value}
			registry[program.Key()] = ProgramSpec{Program: program, Boundary: boundary, Workspace: workspace}
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
	add(ProgramDefinitiveGo, definitiveGoPrograms, BoundaryRustClient, WorkspaceBaselineBranch)
	return registry
}

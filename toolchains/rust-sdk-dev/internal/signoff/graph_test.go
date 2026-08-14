// Package signoff audits exact-target graph construction without initializing Dagger bindings.
package signoff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
)

func TestBuildExportsOneFocusedTargetWithoutStartingAService(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	build := findFunction(t, source, "SignoffArtifact")
	for selector, expected := range map[string]int{
		"EngineContent":                      1,
		"ContainerWithFocusedRustSdkcontent": 1,
		"AsTarball":                          1,
		"artifactTool":                       1,
	} {
		if got := selectorCount(build, selector); got != expected {
			t.Fatalf("build graph %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{
		"AsService",
		"ServiceWithFocusedRustSdkcontent",
		"EngineIntegration",
		"Release",
		"Publish",
	} {
		if got := selectorCount(build, forbidden); got != 0 {
			t.Fatalf("build graph must not call %s, got %d sites", forbidden, got)
		}
	}
	if got := stringLiteralCount(build, "dagger-rust-sdk-signoff"); got != 0 {
		t.Fatalf("build graph must use the fixed binary constant rather than a caller command")
	}
	if got := identifierCount(build, "payload"); got < 3 {
		t.Fatalf("the exported payload must be retained for scanner and runner seams, got %d references", got)
	}
}

func TestImportVerifiesBeforeItsOnlyContainerImport(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	importer := findFunction(t, source, "importSignoffArtifact")
	if got := selectorCount(importer, "Import"); got != 1 {
		t.Fatalf("import branch must contain one container import, got %d", got)
	}
	if got := selectorCount(importer, "artifactTool"); got != 1 {
		t.Fatalf("import branch must verify the host bundle exactly once, got %d", got)
	}
	for _, forbidden := range []string{
		"EngineContent",
		"RustSdkcontent",
		"ContainerWithFocusedRustSdkcontent",
		"AsTarball",
		"DaggerCli",
		"AsService",
	} {
		if got := selectorCount(importer, forbidden); got != 0 {
			t.Fatalf("import branch must not call %s, got %d sites", forbidden, got)
		}
	}
	verifierOffset := selectorOffset(t, importer, "artifactTool")
	importOffset := selectorOffset(t, importer, "Import")
	if verifierOffset >= importOffset {
		t.Fatalf("bundle verification must be constructed before Container.Import")
	}
}

func TestArtifactToolIsEngineFreeAndClosed(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	tool := findFunction(t, source, "artifactTool")
	if got := selectorCount(tool, "DevContainer"); got != 1 {
		t.Fatalf("artifact policy must use one Rust-only development container, got %d", got)
	}
	for _, forbidden := range []string{"DaggerEngine", "AsService", "WithServiceBinding", "WithExec"} {
		if got := selectorCount(tool, forbidden); got != 0 {
			t.Fatalf("artifact tool constructor must not call %s", forbidden)
		}
	}
}

func TestInstalledBaselineOwnsOneServiceOneInstallAndTheArtifactCLI(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	baseline := findFunction(t, source, "installedRustBaseline")
	for selector, expected := range map[string]int{
		"AsService":          1,
		"WithMountedFile":    1,
		"WithServiceBinding": 1,
		"WithoutEnvVariable": 1,
	} {
		if got := selectorCount(baseline, selector); got != expected {
			t.Fatalf("installed baseline %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := stringLiteralCount(baseline, "--here"); got != 1 {
		t.Fatalf("installed baseline must perform exactly one Rust SDK install, got %d", got)
	}
	for _, forbidden := range []string{"EngineContent", "DaggerCli", "ContainerWithFocusedRustSdkcontent", "Import"} {
		if got := selectorCount(baseline, forbidden); got != 0 {
			t.Fatalf("installed baseline must not reconstruct artifact content through %s", forbidden)
		}
	}
}

func TestProgramBranchesIsolateEveryMutableCoordinateWithoutSharedWork(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	branch := findFunction(t, source, "programBranch")
	for selector, expected := range map[string]int{
		"WithWorkdir":      1,
		"WithMountedCache": 1,
	} {
		if got := selectorCount(branch, selector); got != expected {
			t.Fatalf("program branch %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{"AsService", "Import", "EngineContent", "RustSdkcontent"} {
		if got := selectorCount(branch, forbidden); got != 0 {
			t.Fatalf("isolated program branch must not perform shared work through %s", forbidden)
		}
	}
	if got := stringLiteralCount(branch, "--here"); got != 0 {
		t.Fatalf("isolated program branch must not reinstall the Rust SDK, got %d", got)
	}
	stop := findFunction(t, source, "stop")
	if got := selectorCount(stop, "Stop"); got != 1 {
		t.Fatalf("exact-target cleanup must have one stop site, got %d", got)
	}
}

func TestTopLevelSignoffHasOneAdmissionArtifactScanBaselineFanoutAndCleanup(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	facade := findFunction(t, source, "Signoff")
	for selector, expected := range map[string]int{
		"admitSignoffInputs":    1,
		"SignoffArtifact":       1,
		"importSignoffArtifact": 1,
		"installedRustBaseline": 1,
		"ExecuteBounded":        1,
		"stop":                  1,
	} {
		if got := selectorCount(facade, selector); got != expected {
			t.Fatalf("top-level signoff %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := identifierCount(facade, "scanSignoffPayload"); got != 1 {
		t.Fatalf("top-level signoff scanner edge count: got %d, want 1", got)
	}
	for _, forbidden := range []string{
		"EngineIntegration", "CoreConformance", "Release", "ReleaseDryRun", "GeneratedClientCheck",
	} {
		if got := selectorCount(facade, forbidden); got != 0 {
			t.Fatalf("top-level signoff must not enter feature-local or distribution path %s", forbidden)
		}
	}
}

func TestInputAdmissionClosesCompleteDynamicRegistryBeforeTargetWork(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	admission := findFunction(t, source, "admitSignoffInputs")
	for selector, expected := range map[string]int{
		"DecodeObservablePrograms":   1,
		"DecodeScenarioRealizations": 1,
		"CompleteProgramRegistry":    1,
		"ApplyScenarioRealizations":  1,
		"RequireConcretePrograms":    1,
		"DecodeCaseRoutes":           1,
	} {
		if got := selectorCount(admission, selector); got != expected {
			t.Fatalf("input admission %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{"SignoffArtifact", "Import", "AsService", "installedRustBaseline"} {
		if got := selectorCount(admission, forbidden); got != 0 {
			t.Fatalf("input admission must precede target graph work through %s", forbidden)
		}
	}
}

func TestCaseDispatchDoesNotSubstituteBoundaryReachabilityForAssertions(t *testing.T) {
	t.Parallel()

	dispatch := findFunction(t, parseGoFile(t, "../../signoff.go"), "runSignoffCase")
	if got := stringLiteralCount(dispatch, "dagger version"); got != 0 {
		t.Fatalf("case dispatch must not use version reachability as conformance evidence")
	}
	for supported, expected := range map[string]int{
		"ExecutorCoreConformance":     1,
		"ExecutorEngineIntegration":   1,
		"ExecutorScenarioConformance": 2,
	} {
		if got := identifierCount(dispatch, supported); got != expected {
			t.Fatalf("concrete executor %s count: got %d, want %d", supported, got, expected)
		}
	}
	for _, unsupported := range []string{
		"ProgramCommonHarness", "ProgramStableConnector", "ProgramCoreShape", "ProgramEngineIntegration",
		"ProgramModuleAuthoring", "ProgramStandaloneClient", "ProgramDefinitiveGo", "ProgramIntegration",
	} {
		if got := identifierCount(dispatch, unsupported); got != 0 {
			t.Fatalf("program %s must fail closed until it has a concrete executor", unsupported)
		}
	}
	if got := stringLiteralCount(dispatch, "sign-off program %q has no concrete production executor"); got != 1 {
		t.Fatalf("missing concrete executors must have one stable fail-closed path, got %d", got)
	}
}

func TestConcreteExecutorsReuseReviewedProductionAssertions(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	core := findFunction(t, source, "runCoreConformanceCase")
	if got := stringLiteralCount(core, "DAGGER_RUST_SIGNOFF_SELECTOR"); got != 1 {
		t.Fatalf("core executor must bind exactly one reviewed selector, got %d", got)
	}
	if got := selectorCount(core, "Stdout"); got != 1 {
		t.Fatalf("core executor must collect exactly one structured result, got %d", got)
	}
	integration := findFunction(t, source, "runEngineIntegrationSignoffCase")
	for assertion, expected := range map[string]int{
		"verifyInstalledRustResolution": 1,
		"runEngineIntegrationCase":      1,
		"stableCaseObservation":         1,
	} {
		if got := identifierCount(integration, assertion); got != expected {
			t.Fatalf("engine-integration executor %s count: got %d, want %d", assertion, got, expected)
		}
	}
	scenario := findFunction(t, source, "runScenarioConformanceCase")
	if got := stringLiteralCount(scenario, "DAGGER_RUST_SCENARIO_REALIZATION"); got != 1 {
		t.Fatalf("scenario executor must bind exactly one reviewed selector, got %d", got)
	}
	if got := selectorCount(scenario, "Stdout"); got != 1 {
		t.Fatalf("scenario executor must collect exactly one structured result, got %d", got)
	}
}

func TestFocusedSourceClosureExcludesUnrelatedSDKs(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../main.go")
	focused := findFunction(t, source, "focusedEngineSource")
	for _, foreign := range []string{"!sdk/python", "!sdk/typescript", "!sdk/java", "!sdk/php"} {
		if got := stringLiteralCount(focused, foreign); got != 0 {
			t.Fatalf("focused source must not include unrelated SDK %q", foreign)
		}
	}
	if got := stringLiteralCount(focused, "!sdk/go/**"); got != 1 {
		t.Fatalf("focused source must retain exactly one mandatory Go runtime closure")
	}
}

func parseGoFile(t *testing.T, path string) *ast.File {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func findFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func selectorCount(node ast.Node, name string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			count++
		}
		return true
	})
	return count
}

func identifierCount(node ast.Node, name string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			count++
		}
		return true
	})
	return count
}

func stringLiteralCount(node ast.Node, value string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		decoded, err := strconv.Unquote(literal.Value)
		if err == nil && decoded == value {
			count++
		}
		return true
	})
	return count
}

func selectorOffset(t *testing.T, node ast.Node, name string) token.Pos {
	t.Helper()
	var position token.Pos
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && position == token.NoPos {
			position = selector.Pos()
		}
		return true
	})
	if position == token.NoPos {
		t.Fatalf("selector %s not found", name)
	}
	return position
}

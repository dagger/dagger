// Package signoff audits exact-target graph construction without initializing Dagger bindings.
package signoff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestBuildExportsOneFocusedTargetWithoutStartingAService(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	build := findFunction(t, source, "SignoffArtifact")
	for selector, expected := range map[string]int{
		"rederiveSignoffArtifactSeed":        1,
		"signoffEngineContent":               1,
		"ContainerWithFocusedRustSdkcontent": 1,
		"observeSignoffTargetComponents":     1,
		"sealSignoffArtifactPlan":            1,
		"AsTarball":                          1,
		"Digest":                             1,
		"artifactTool":                       1,
	} {
		if got := selectorCount(build, selector); got != expected {
			t.Fatalf("build graph %s count: got %d, want %d", selector, got, expected)
		}
	}
	if selectorOffset(t, build, "rederiveSignoffArtifactSeed") >= selectorOffset(t, build, "signoffEngineContent") {
		t.Fatal("immutable seed must be rederived before target graph construction")
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
	for _, argument := range []string{"--observation", "--receipt-output"} {
		if got := stringLiteralCount(build, argument); got != 1 {
			t.Fatalf("build graph must pass receipt argument %q once, got %d", argument, got)
		}
	}
	if got := identifierCount(build, "payload"); got < 3 {
		t.Fatalf("the exported payload must be retained for scanner and runner seams, got %d references", got)
	}
}

func TestBuildPlanSealsObservedComponentsWithoutAnotherBuilder(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	sealer := findFunction(t, source, "sealSignoffArtifactPlan")
	for selector, expected := range map[string]int{"signoffPolicyContainer": 1} {
		if got := selectorCount(sealer, selector); got != expected {
			t.Fatalf("artifact plan sealer %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := stringLiteralCount(sealer, "artifact-plan"); got != 1 {
		t.Fatalf("component identities must cross the Rust plan boundary once, got %d", got)
	}
	for _, forbidden := range []string{
		"EngineContent", "RustSdkcontent", "ContainerWithFocusedRustSdkcontent", "AsTarball", "AsService", "Import",
	} {
		if got := selectorCount(sealer, forbidden); got != 0 {
			t.Fatalf("artifact plan sealer must not reconstruct target work through %s", forbidden)
		}
	}
	observer := findFunction(t, source, "observeSignoffTargetComponents")
	for selector, expected := range map[string]int{"WithMountedFile": 2, "EnvVariable": 2} {
		if got := selectorCount(observer, selector); got != expected {
			t.Fatalf("component observer %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := stringLiteralCount(observer, "sha256sum"); got != 1 {
		t.Fatalf("engine and CLI identities must share one checksum invocation, got %d", got)
	}
	for _, forbidden := range []string{"EngineContent", "RustSdkcontent", "AsTarball", "AsService", "Import"} {
		if got := selectorCount(observer, forbidden); got != 0 {
			t.Fatalf("component observer must not reconstruct target work through %s", forbidden)
		}
	}
}

func TestArtifactSeedIsRederivedFromImmutableGitBeforeTargetWork(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	rederive := findFunction(t, source, "rederiveSignoffArtifactSeed")
	for selector, expected := range map[string]int{
		"AdmitArtifactSubject":              1,
		"Git":                               1,
		"Commit":                            1,
		"Tree":                              1,
		"Contents":                          1,
		"VerifyArtifactSeed":                1,
		"verifySignoffImplementationSource": 1,
	} {
		if got := selectorCount(rederive, selector); got != expected {
			t.Fatalf("immutable seed boundary %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{
		"EngineContent", "signoffEngineContent", "ContainerWithFocusedRustSdkcontent", "AsTarball", "AsService",
	} {
		if got := selectorCount(rederive, forbidden); got != 0 {
			t.Fatalf("seed admission must precede target work through %s", forbidden)
		}
	}
	if got := stringLiteralCount(rederive, "--repository"); got != 1 {
		t.Fatalf("immutable seed derivation must bind one canonical repository argument, got %d", got)
	}
}

func TestLiveDaggerImplementationsMustMatchTheImmutableSubject(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	verification := findFunction(t, source, "verifySignoffImplementationSource")
	for selector, expected := range map[string]int{
		"Git":    1,
		"Commit": 1,
		"Tree":   1,
		"Filter": 2,
		"Digest": 2,
	} {
		if got := selectorCount(verification, selector); got != expected {
			t.Fatalf("implementation-source verification %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, path := range []string{
		"engine/distconsts/**", "toolchains/cli-dev/**", "toolchains/engine-dev/**",
		"toolchains/go/**", "toolchains/rust-sdk-dev/**",
	} {
		if got := stringLiteralCount(verification, path); got != 1 {
			t.Fatalf("implementation-source verification must retain %q once, got %d", path, got)
		}
	}
	for _, forbidden := range []string{"DaggerEngine", "AsService", "ContainerWithFocusedRustSdkcontent"} {
		if got := selectorCount(verification, forbidden); got != 0 {
			t.Fatalf("implementation-source verification must precede target work through %s", forbidden)
		}
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
	if got := selectorCount(importer, "observeSignoffTargetComponents"); got != 1 {
		t.Fatalf("import branch must independently observe imported component bytes once, got %d", got)
	}
	if got := selectorCount(importer, "VerifyTargetComponents"); got != 1 {
		t.Fatalf("import branch must compare imported components with the admitted plan once, got %d", got)
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
	if got := selectorCount(tool, "signoffPolicyContainer"); got != 1 {
		t.Fatalf("artifact policy must use one immutable subject container, got %d", got)
	}
	for _, forbidden := range []string{"DaggerEngine", "AsService", "WithServiceBinding", "WithExec"} {
		if got := selectorCount(tool, forbidden); got != 0 {
			t.Fatalf("artifact tool constructor must not call %s", forbidden)
		}
	}
}

func TestArtifactPolicyImplementationComesFromTheImmutableSubject(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	policy := findFunction(t, source, "signoffPolicyContainer")
	for selector, expected := range map[string]int{
		"Git":           1,
		"Commit":        1,
		"Tree":          1,
		"WithDirectory": 1,
		"WithWorkdir":   1,
	} {
		if got := selectorCount(policy, selector); got != expected {
			t.Fatalf("immutable policy %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{"DevContainer", "EngineContent", "DaggerEngine", "AsService"} {
		if got := selectorCount(policy, forbidden); got != 0 {
			t.Fatalf("immutable policy boundary must not call %s", forbidden)
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
		"WithSecretVariable": 1,
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
		"importSignoffArtifact": 1,
		"installedRustBaseline": 1,
		"startAndProbe":         1,
		"secretSignoffReport":   1,
		"ExecutePolicyBounded":  1,
		"WithoutCancel":         1,
		"WithTimeout":           1,
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
		"SignoffArtifact", "EngineContent", "EngineIntegration", "CoreConformance", "Release", "ReleaseDryRun", "GeneratedClientCheck",
	} {
		if got := selectorCount(facade, forbidden); got != 0 {
			t.Fatalf("top-level signoff must not enter feature-local or distribution path %s", forbidden)
		}
	}
	assertLiveBaselineReturnsUseCleanup(t, facade)
}

func TestExactTargetStartsAndReportsItsVersionBeforeInstallation(t *testing.T) {
	t.Parallel()

	probe := findFunction(t, parseGoFile(t, "../../signoff.go"), "startAndProbe")
	for selector, expected := range map[string]int{
		"Start":              1,
		"WithMountedFile":    1,
		"WithServiceBinding": 1,
		"WithExec":           1,
		"Stdout":             1,
	} {
		if got := selectorCount(probe, selector); got != expected {
			t.Fatalf("exact-target readiness %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := stringLiteralCount(probe, "{version}"); got != 1 {
		t.Fatalf("readiness must issue one bounded public version query, got %d", got)
	}
	for _, forbidden := range []string{"EngineContent", "Import", "AsService", "RustSdkcontent", "SignoffArtifact"} {
		if got := selectorCount(probe, forbidden); got != 0 {
			t.Fatalf("readiness probe must not construct shared work through %s", forbidden)
		}
	}
}

func assertLiveBaselineReturnsUseCleanup(t *testing.T, facade *ast.FuncDecl) {
	t.Helper()
	start := selectorOffset(t, facade, "installedRustBaseline")
	var end token.Pos
	ast.Inspect(facade.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if !ok || identifier.Name != "finishBaseline" || len(call.Args) != 1 {
			return true
		}
		argument, ok := call.Args[0].(*ast.Ident)
		if ok && argument.Name == "nil" {
			end = call.Pos()
		}
		return true
	})
	if !end.IsValid() || end <= start {
		t.Fatal("top-level signoff has no terminal cleanup call after baseline creation")
	}
	returns := 0
	ast.Inspect(facade.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || statement.Pos() <= start || statement.Pos() >= end {
			return true
		}
		returns++
		usesCleanup := false
		ast.Inspect(statement, func(child ast.Node) bool {
			identifier, ok := child.(*ast.Ident)
			if ok && identifier.Name == "finishBaseline" {
				usesCleanup = true
			}
			return true
		})
		if !usesCleanup {
			t.Fatalf("return at offset %d can abandon a live exact-target baseline", statement.Pos())
		}
		return true
	})
	if returns == 0 {
		t.Fatal("live baseline interval unexpectedly has no fail-closed exits to audit")
	}
}

func TestSecretEvidenceUsesOneEphemeralSeedAndTheRustAdmissionBoundary(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	producer := findFunction(t, source, "secretSignoffReport")
	for selector, expected := range map[string]int{
		"WithMountedDirectory": 1,
		"WithMountedSecret":    1,
		"WithDirectory":        1,
		"WithWorkdir":          1,
		"DevContainer":         0,
	} {
		if got := selectorCount(producer, selector); got != expected {
			t.Fatalf("secret evidence %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := identifierCount(producer, "rustBaseContainer"); got != 1 {
		t.Fatalf("secret evidence must use one immutable-compatible base container, got %d", got)
	}
	if got := selectorCount(producer, "Files"); got != 1 {
		t.Fatalf("secret evidence must admit the complete bounded domain set once, got %d", got)
	}
	if got := selectorCount(producer, "WithNewFile"); got != 2 {
		t.Fatalf("secret evidence must mount the closed actual domains and packaged scan, got %d", got)
	}
	if got := stringLiteralCount(producer, "secret-report"); got != 1 {
		t.Fatalf("secret evidence must invoke the Rust admission command once, got %d", got)
	}
	for _, forbidden := range []string{"EngineContent", "SignoffArtifact", "AsService", "Import", "Release"} {
		if got := selectorCount(producer, forbidden); got != 0 {
			t.Fatalf("secret evidence must not construct target work through %s", forbidden)
		}
	}
	canaries := findFunction(t, source, "newSignoffCanarySet")
	for _, environment := range []string{
		"RUST_SDK_SIGNOFF_SESSION", "RUST_SDK_SIGNOFF_REGISTRY", "RUST_SDK_SIGNOFF_GIT",
		"RUST_SDK_SIGNOFF_ENVIRONMENT", "RUST_SDK_SIGNOFF_TRACE", "RUST_SDK_SIGNOFF_URL",
	} {
		if got := stringLiteralCount(canaries, environment); got != 1 {
			t.Fatalf("ephemeral canary set must define %q exactly once, got %d", environment, got)
		}
	}
}

func TestPackagedEvidenceReadsTheThreeActualStandaloneOutputsBeforeEngineStop(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	facade := findFunction(t, source, "Signoff")
	if got := selectorCount(facade, "packagedSignoffScan"); got != 1 {
		t.Fatalf("sign-off must scan the actual standalone workspaces once, got %d", got)
	}
	scanner := findFunction(t, source, "packagedSignoffScan")
	for selector, expected := range map[string]int{
		"Size":                 1,
		"WithMountedFile":      1,
		"WithFile":             1,
		"WithMountedDirectory": 1,
		"WithMountedSecret":    1,
		"Contents":             1,
	} {
		if got := selectorCount(scanner, selector); got != expected {
			t.Fatalf("packaged scanner actual-byte edge %s count: got %d, want %d", selector, got, expected)
		}
	}
	for literal, expected := range map[string]int{
		"sha256sum":                1,
		"packaged-scan":            1,
		"build/cli":                1,
		"build/backend-image.tar":  1,
		"build/frontend-image.tar": 1,
	} {
		if got := stringLiteralCount(scanner, literal); got != expected {
			t.Fatalf("packaged scanner literal %q count: got %d, want %d", literal, got, expected)
		}
	}
	for _, forbidden := range []string{"Publish", "AsService", "SignoffArtifact", "EngineContent"} {
		if got := selectorCount(scanner, forbidden); got != 0 {
			t.Fatalf("packaged scanner must not invoke %s, got %d", forbidden, got)
		}
	}
	executor := findFunction(t, source, "runStandaloneExampleSignoffCase")
	for selector, expected := range map[string]int{"WithExec": 1, "Size": 1, "Directory": 2} {
		if got := selectorCount(executor, selector); got != expected {
			t.Fatalf("standalone output retention %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := selectorCount(executor, "Publish"); got != 0 {
		t.Fatalf("standalone sign-off must never publish, got %d calls", got)
	}
	for identifier, expected := range map[string]int{
		"ParseStandaloneResolvedImages": 1,
		"identifySignoffFile":           1,
		"canonicalizeJSON":              1,
	} {
		if got := identifierCount(executor, identifier); got != expected {
			t.Fatalf("standalone structured evidence %s count: got %d, want %d", identifier, got, expected)
		}
	}
	hasher := findFunction(t, source, "identifySignoffFile")
	for selector, expected := range map[string]int{"WithMountedFile": 1, "WithExec": 1, "Stdout": 1} {
		if got := selectorCount(hasher, selector); got != expected {
			t.Fatalf("standalone actual output identity %s count: got %d, want %d", selector, got, expected)
		}
	}
}

func TestSecretEvidenceRetainsActualProductionEdgesInsteadOfIdentityProxies(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	facade := findFunction(t, source, "Signoff")
	for selector, expected := range map[string]int{"secretSignoffReport": 1} {
		if got := selectorCount(facade, selector); got != expected {
			t.Fatalf("production secret evidence %s count: got %d, want %d", selector, got, expected)
		}
	}
	for function, expected := range map[string]int{
		"collectDirectoryEvidence": 2, "collectArtifactEntryEvidence": 1,
	} {
		if got := identifierCount(facade, function); got != expected {
			t.Fatalf("production secret evidence %s count: got %d, want %d", function, got, expected)
		}
	}
	for path, expected := range map[string]int{
		"toolchains/rust-sdk-dev/testdata/core_conformance.rs":     2,
		"toolchains/rust-sdk-dev/testdata/scenario_conformance.rs": 2,
		"sdk/rust/examples/**": 1,
	} {
		if got := stringLiteralCount(facade, path); got != expected {
			t.Fatalf("exact subject case evidence %q count: got %d, want %d", path, got, expected)
		}
	}
	for _, actual := range []string{
		"sourceEvidence", "generatedEvidence", "artifactEntryEvidence", "processBytes",
		"errorsAndDebug", "cacheAndProvenance", "casesEvidence", "reports", "draft",
	} {
		if got := identifierCount(facade, actual); got < 2 {
			t.Fatalf("actual evidence edge %q is not produced and consumed, got %d references", actual, got)
		}
	}
	for _, proxy := range []string{"retainedDiagnostic", "reviewed-rust-assertion-passed", "reviewed-rust-executor-unavailable"} {
		if got := identifierCount(facade, proxy) + stringLiteralCount(facade, proxy); got != 0 {
			t.Fatalf("secret evidence must not manufacture proxy diagnostic %q", proxy)
		}
	}

	directory := findFunction(t, source, "collectDirectoryEvidence")
	for selector, expected := range map[string]int{"WithDirectory": 1, "WithExec": 1, "Contents": 1} {
		if got := selectorCount(directory, selector); got != expected {
			t.Fatalf("actual directory evidence %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := selectorCount(directory, "Digest"); got != 0 {
		t.Fatalf("actual directory evidence was replaced by %d digest observations", got)
	}
	artifact := findFunction(t, source, "collectArtifactEntryEvidence")
	for selector, expected := range map[string]int{"WithMountedFile": 1, "WithExec": 1, "Contents": 1} {
		if got := selectorCount(artifact, selector); got != expected {
			t.Fatalf("actual artifact-entry evidence %s count: got %d, want %d", selector, got, expected)
		}
	}
}

func TestInstalledBaselineFactsComeFromThePreServiceRunner(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	observer := findFunction(t, source, "observeInitialFacts")
	for selector, expected := range map[string]int{"WithExec": 4, "Stdout": 4} {
		if got := selectorCount(observer, selector); got != expected {
			t.Fatalf("baseline fact observer %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, forbidden := range []string{"AsService", "Start", "SignoffArtifact", "EngineContent"} {
		if got := selectorCount(observer, forbidden); got != 0 {
			t.Fatalf("pre-service baseline observation must not invoke %s, got %d", forbidden, got)
		}
	}
	for _, evidence := range []string{"git", "status", "--porcelain", "command -v dagger"} {
		if got := stringLiteralCount(observer, evidence); got != 1 {
			t.Fatalf("baseline fact observer must retain %q exactly once, got %d", evidence, got)
		}
	}
}

func TestExactPayloadScannerUsesOneFileAndTheRustTranslator(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	scanner := findFunction(t, source, "scanSignoffPayload")
	for selector, expected := range map[string]int{
		"WithMountedFile":      1,
		"WithMountedCache":     0,
		"CacheVolume":          0,
		"WithMountedDirectory": 1,
		"WithDirectory":        1,
		"WithWorkdir":          1,
		"DevContainer":         0,
	} {
		if got := selectorCount(scanner, selector); got != expected {
			t.Fatalf("exact scanner %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := identifierCount(scanner, "rustBaseContainer"); got != 1 {
		t.Fatalf("scanner translator must use one immutable-compatible base container, got %d", got)
	}
	if got := stringLiteralCount(scanner, "scanner-translate"); got != 1 {
		t.Fatalf("exact scanner must invoke the Rust translator once, got %d", got)
	}
	if got := identifierCount(scanner, "payload"); got != 3 {
		t.Fatalf("exact scanner must validate and consume only its supplied payload edge, got %d references", got)
	}
	if got := identifierCount(scanner, "signoffDatabaseRepository"); got != 1 {
		t.Fatalf("exact scanner must consume the digest-pinned database repository once, got %d", got)
	}
	if got := identifierCount(scanner, "signoffDatabaseArtifactDigest"); got != 1 {
		t.Fatalf("exact scanner must bind the database artifact digest once, got %d", got)
	}
	for fragment, expected := range map[string]int{
		`trivy image --download-db-only --db-repository "$RUST_SIGNOFF_TRIVY_DB"`: 1,
		"trivy image --skip-db-update":                                            1,
		"sha256sum trivy.db metadata.json":                                        1,
		"sha256sum metadata.json trivy.db":                                        0,
	} {
		if got := stringLiteralSubstringCount(scanner, fragment); got != expected {
			t.Fatalf("exact scanner source fragment %q count: got %d, want %d", fragment, got, expected)
		}
	}
	for value, expected := range map[string]int{
		"ghcr.io/aquasecurity/trivy-db@":                                          1,
		"sha256:10a3832219beaf45a3eb86065e30b39e528ae9c1650aa5f733d4666afd0712c5": 1,
	} {
		if got := stringLiteralCount(source, value); got != expected {
			t.Fatalf("exact scanner immutable database identity %q count: got %d, want %d", value, got, expected)
		}
	}
	for _, forbidden := range []string{
		"EngineContent", "SignoffArtifact", "importSignoffArtifact", "AsService", "Release",
	} {
		if got := selectorCount(scanner, forbidden); got != 0 {
			t.Fatalf("exact scanner must not construct target work through %s", forbidden)
		}
	}
}

func TestInputAdmissionClosesCompleteDynamicRegistryBeforeTargetWork(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	admission := findFunction(t, source, "admitSignoffInputs")
	for selector, expected := range map[string]int{
		"DecodeObservablePrograms":    1,
		"DecodeScenarioRealizations":  1,
		"CompleteProgramRegistry":     1,
		"ApplyScenarioRealizations":   1,
		"RequireConcretePrograms":     1,
		"DecodeFacadeAdmissionRoutes": 1,
		"GroupCaseExecutions":         1,
	} {
		if got := selectorCount(admission, selector); got != expected {
			t.Fatalf("input admission %s count: got %d, want %d", selector, got, expected)
		}
	}
	if got := stringLiteralCount(admission, "facade-admit"); got != 1 {
		t.Fatalf("input admission must invoke the Rust pre-target compiler once, got %d", got)
	}
	for _, forbidden := range []string{"SignoffArtifact", "Import", "AsService", "installedRustBaseline"} {
		if got := selectorCount(admission, forbidden); got != 0 {
			t.Fatalf("input admission must precede target graph work through %s", forbidden)
		}
	}
}

func TestCaseDispatchDoesNotSubstituteBoundaryReachabilityForAssertions(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	dispatch := findFunction(t, source, "runSignoffCaseAttempt")
	if got := stringLiteralCount(dispatch, "dagger version"); got != 0 {
		t.Fatalf("case dispatch must not use version reachability as conformance evidence")
	}
	for supported, expected := range map[string]int{
		"ExecutorCoreConformance":     1,
		"ExecutorEngineIntegration":   1,
		"ExecutorScenarioConformance": 1,
		"ExecutorStandaloneExample":   1,
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
	groupDispatch := findFunction(t, source, "executeSignoffGroupAttempt")
	if got := stringLiteralCount(groupDispatch, "sign-off program %q has no concrete production executor"); got != 1 {
		t.Fatalf("missing concrete executors must have one stable fail-closed path, got %d", got)
	}
}

func TestCaseExecutionCarriesAdmittedPolicyIntoSchedulerAttemptsAndBranches(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	facade := findFunction(t, source, "Signoff")
	if got := selectorCount(facade, "ExecutePolicyBounded"); got != 1 {
		t.Fatalf("top-level signoff must enter the policy scheduler once, got %d", got)
	}
	if got := selectorCount(facade, "ExecuteBounded"); got != 0 {
		t.Fatalf("top-level signoff bypasses admitted concurrency policy %d times", got)
	}
	group := findFunction(t, source, "runSignoffExecutionGroup")
	if got := selectorCount(group, "ExecutePolicyAttempts"); got != 1 {
		t.Fatalf("execution group must apply timeout and retry policy once, got %d", got)
	}
	branch := findFunction(t, source, "programBranch")
	for variable, expected := range map[string]int{
		"RUST_SDK_SIGNOFF_ATTEMPT":        1,
		"RUST_SDK_SIGNOFF_NAMESPACE":      1,
		"RUST_SDK_SIGNOFF_NETWORK_POLICY": 1,
	} {
		if got := stringLiteralCount(branch, variable); got != expected {
			t.Fatalf("program branch %s count: got %d, want %d", variable, got, expected)
		}
	}
}

func TestConcreteExecutorsReuseReviewedProductionAssertions(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	core := findFunction(t, source, "runCoreConformanceCase")
	if got := stringLiteralCount(core, "DAGGER_RUST_SIGNOFF_SELECTOR"); got != 1 {
		t.Fatalf("core executor must bind exactly one reviewed selector, got %d", got)
	}
	if got := identifierCount(core, "readSignoffProcessEvidence"); got != 1 {
		t.Fatalf("core executor must retain actual stdout and stderr once, got %d", got)
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
	if got := stringLiteralCount(scenario, "DAGGER_RUST_SCENARIO_CONTRACTS"); got != 1 {
		t.Fatalf("scenario executor must bind the complete reviewed contract set once, got %d", got)
	}
	if got := stringLiteralCount(scenario, "signoff-observation"); got != 1 {
		t.Fatalf("scenario executor must compile the instance-bound connector observer once, got %d", got)
	}
	if got := identifierCount(scenario, "canonicalizeJSON"); got != 1 {
		t.Fatalf("scenario executor must retain one canonical structured connector observation, got %d", got)
	}
	if got := identifierCount(scenario, "readSignoffProcessEvidence"); got != 1 {
		t.Fatalf("scenario executor must retain actual stdout and stderr once, got %d", got)
	}
	process := findFunction(t, source, "readSignoffProcessEvidence")
	for selector, expected := range map[string]int{"Stdout": 1, "Stderr": 1} {
		if got := selectorCount(process, selector); got != expected {
			t.Fatalf("process evidence %s count: got %d, want %d", selector, got, expected)
		}
	}
	for _, model := range []string{"scenarioConformanceContract", "scenarioConformanceObservation"} {
		fields := structJSONTags(t, source, model)
		for _, field := range []string{"case_id", "contract_digest", "proof_id"} {
			if !fields[field] {
				t.Fatalf("%s omits assertion-specific field %q", model, field)
			}
		}
	}
}

func TestRawObservationSeparatesVerdictRowsFromReviewedExecutions(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	observation := structJSONTags(t, source, "rawSignoffFacadeObservation")
	for _, field := range []string{
		"baseline_directory_digest", "baseline_observation_digest", "case_executions", "cases", "closure_replays",
		"cli_component_builds", "dependency", "engine_component_builds",
		"engine_observation_digest", "exact_target_child_reaps", "go_runtime_component_builds",
		"installed_config_digest", "runnable_execution_millis", "runner_image_digest",
		"rust_sdk_component_builds", "scanner_observation", "scanner_result_digest", "secret_report", "stable_connector", "total_millis", "unrelated_actions",
		"verified_rust_dependency", "verified_rust_dependency_descriptor_digest",
	} {
		if !observation[field] {
			t.Fatalf("raw facade observation omits %q", field)
		}
	}
	caseFields := structJSONTags(t, source, "rawSignoffCase")
	for _, field := range []string{
		"attempt_outcomes", "executed", "execution_selector", "observation_digest",
	} {
		if !caseFields[field] {
			t.Fatalf("raw case observation omits %q", field)
		}
	}
}

func TestInstalledDependencyIsObservedFromAnIsolatedPackagedSDKBranch(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../signoff.go")
	observer := findFunction(t, source, "observeInstalledDependency")
	if got := selectorCount(observer, "WithExec"); got != 1 {
		t.Fatalf("installed dependency proof must run one packaged SDK initializer, got %d", got)
	}
	if got := selectorCount(observer, "ObserveInstalledRustDependency"); got != 1 {
		t.Fatalf("installed dependency proof must cross the pure admission boundary once, got %d", got)
	}
	for _, argument := range []string{"module", "init", "rust", "--no-generate"} {
		if got := stringLiteralCount(observer, argument); got != 1 {
			t.Fatalf("installed dependency proof argument %q count: got %d", argument, got)
		}
	}
	if got := selectorCount(observer, "EngineContent"); got != 0 {
		t.Fatalf("installed dependency observation reconstructed target work %d times", got)
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

func structJSONTags(t *testing.T, file *ast.File, name string) map[string]bool {
	t.Helper()
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range generic.Specs {
			typed, ok := specification.(*ast.TypeSpec)
			if !ok || typed.Name.Name != name {
				continue
			}
			structure, ok := typed.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is not a struct", name)
			}
			tags := make(map[string]bool, len(structure.Fields.List))
			for _, field := range structure.Fields.List {
				if field.Tag == nil {
					continue
				}
				decoded, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatalf("decode %s field tag: %v", name, err)
				}
				const prefix = `json:"`
				if len(decoded) <= len(prefix) || decoded[:len(prefix)] != prefix {
					continue
				}
				end := len(prefix)
				for end < len(decoded) && decoded[end] != '"' && decoded[end] != ',' {
					end++
				}
				tags[decoded[len(prefix):end]] = true
			}
			return tags
		}
	}
	t.Fatalf("struct %s not found", name)
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

func stringLiteralSubstringCount(node ast.Node, value string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		decoded, err := strconv.Unquote(literal.Value)
		if err == nil {
			count += strings.Count(decoded, value)
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

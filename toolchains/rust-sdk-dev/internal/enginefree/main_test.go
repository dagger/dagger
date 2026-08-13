// Package enginefree verifies the Rust SDK development module without initializing
// generated Dagger bindings, which themselves require an active engine session.
package enginefree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestEngineFreeFunctionsDoNotConstructDaggerEngine(t *testing.T) {
	t.Parallel()

	file := parseGoFile(t, "../../main.go")
	for _, function := range []string{"New", "DevContainer", "devContainer", "EngineUnit"} {
		declaration := findFunction(t, file, function)
		if selectorCount(declaration, "DaggerEngine") != 0 {
			t.Fatalf("%s must remain engine-free", function)
		}
	}

	boundary := findFunction(t, file, "engineClientContainer")
	if got := selectorCount(boundary, "DaggerEngine"); got != 1 {
		t.Fatalf("engineClientContainer must own exactly one engine construction, got %d", got)
	}

	constructor := findFunction(t, file, "New")
	for _, exclusion := range []string{"sdk/rust/target", "sdk/rust/**/target"} {
		if got := stringLiteralCount(constructor, exclusion); got != 1 {
			t.Fatalf("Rust SDK workspace must exclude local build output %q", exclusion)
		}
	}
}

func TestEngineUnitGeneratedAdapterIsWired(t *testing.T) {
	t.Parallel()

	file := parseGoFile(t, "../../dagger.gen.go")
	if got := selectorCount(file, "EngineUnit"); got != 1 {
		t.Fatalf("generated adapter must dispatch EngineUnit exactly once, got %d", got)
	}
	if got := stringLiteralCount(file, "EngineUnit"); got != 2 {
		t.Fatalf("generated adapter must expose and dispatch EngineUnit, got %d registrations", got)
	}
	if got := identifierCount(file, "ClientDockerConfig"); got < 5 {
		t.Fatalf("generated adapter must preserve private client credentials across calls, got %d bindings", got)
	}
}

func TestEngineUnitCoversTheBoundedPackagedRustInitializerSurface(t *testing.T) {
	t.Parallel()

	engineUnit := findFunction(t, parseGoFile(t, "../../main.go"), "EngineUnit")
	pattern := "^(TestSDKResolveInstall|TestPackagedRustSDKRegistersOnlyImplementedInitializer)$"
	if got := stringLiteralCount(engineUnit, pattern); got != 1 {
		t.Fatalf("engine-unit must select the exact packaged Rust CLI boundaries once, got %d", got)
	}
	if got := stringLiteralCount(engineUnit, "./core/sdk"); got != 1 {
		t.Fatalf("engine-unit must retain the reflected SDK hook-surface tests, got %d", got)
	}
}

func TestReusableEngineContentBoundaryIsGenerated(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../main.go")
	engineContent := findFunction(t, source, "EngineContent")
	if got := selectorCount(engineContent, "DaggerEngine"); got != 1 {
		t.Fatalf("EngineContent must construct exactly one engine-dev graph, got %d", got)
	}
	resolution := findFunction(t, source, "Resolution")
	if got := selectorCount(resolution, "RustSdkcontent"); got != 0 {
		t.Fatalf("Resolution must reuse the retained content object, got %d rebuilds", got)
	}
	integration := findFunction(t, source, "EngineIntegration")
	if got := selectorCount(integration, "RustSdkcontent"); got != 0 {
		t.Fatalf("EngineIntegration must reuse the retained content object, got %d rebuilds", got)
	}

	generated := parseGoFile(t, "../../dagger.gen.go")
	for _, function := range []string{"EngineContent", "EngineIntegration", "EngineEvidence", "Resolution"} {
		if got := selectorCount(generated, function); got != 1 {
			t.Fatalf("generated adapter must dispatch %s exactly once, got %d", function, got)
		}
		if got := stringLiteralCount(generated, function); got != 2 {
			t.Fatalf("generated adapter must expose and dispatch %s, got %d registrations", function, got)
		}
	}
}

func TestFocusedContainerAndServiceShareOneConstructionBoundary(t *testing.T) {
	t.Parallel()

	engineSource := parseGoFile(t, "../../../engine-dev/main.go")
	container := findFunction(t, engineSource, "ContainerWithFocusedRustSDKContent")
	if got := identifierCount(container, "focusedRustContainer"); got != 1 {
		t.Fatalf("focused export must delegate to the shared constructor exactly once, got %d", got)
	}
	service := findFunction(t, engineSource, "ServiceWithFocusedRustSDKContent")
	if got := identifierCount(service, "focusedRustContainer"); got != 1 {
		t.Fatalf("focused service must delegate to the shared constructor exactly once, got %d", got)
	}
}

func TestEngineEvidenceOwnsTheCompleteClosedCaseSet(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../main.go")
	integration := findFunction(t, source, "EngineIntegration")
	if got := identifierCount(integration, "engineIntegrationCases"); got < 2 {
		t.Fatalf("EngineIntegration must validate and default from the one closed case inventory, got %d references", got)
	}
	evidence := findFunction(t, source, "EngineEvidence")
	if got := identifierCount(evidence, "engineIntegrationCases"); got != 1 {
		t.Fatalf("EngineEvidence must consume the complete case inventory exactly once, got %d", got)
	}
	if got := identifierCount(evidence, "requireCompleteEngineCaseSet"); got != 1 {
		t.Fatalf("EngineEvidence must reject incomplete observations before publication")
	}
	want := []string{
		"resolution",
		"init-empty",
		"init-existing",
		"init-no-generate",
		"operations",
		"runtime-checked",
		"runtime-legacy",
		"negative-generated-lock-toolchain",
		"negative-path-ownership",
		"negative-redaction",
	}
	got := variableStringSlice(t, source, "engineIntegrationCases")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("closed engine case inventory changed: got %v, want %v", got, want)
	}
}

func TestEngineIntegrationUsesTheFocusedSourceGraph(t *testing.T) {
	t.Parallel()

	source := parseGoFile(t, "../../main.go")
	focusedSource := findFunction(t, source, "focusedEngineSource")
	if got := stringLiteralCount(focusedSource, "*"); got != 1 {
		t.Fatalf("focused engine source must exclude the workspace by default, got %d wildcard exclusions", got)
	}
	for _, forbidden := range []string{
		"!sdk/rust/target",
		"!sdk/rust/examples",
		"!sdk/python",
		"!sdk/typescript",
	} {
		if got := stringLiteralCount(focusedSource, forbidden); got != 0 {
			t.Fatalf("focused engine source must not re-include %q", forbidden)
		}
	}
	if got := stringLiteralCount(focusedSource, "!sdk/go/**"); got != 1 {
		t.Fatalf("focused engine source must retain the root module's local Go SDK replacement")
	}

	engineContent := findFunction(t, source, "EngineContent")
	if got := selectorCount(engineContent, "WithSource"); got != 1 {
		t.Fatalf("EngineContent must apply exactly one focused source boundary, got %d", got)
	}
	serviceBoundary := findFunction(t, source, "focusedService")
	if got := selectorCount(serviceBoundary, "ServiceWithFocusedRustSdkcontent"); got != 1 {
		t.Fatalf("focusedService must own exactly one focused engine-service construction, got %d", got)
	}
	for _, function := range []string{"Resolution", "EngineIntegration"} {
		declaration := findFunction(t, source, function)
		if got := selectorCount(declaration, "focusedService"); got != 1 {
			t.Fatalf("%s must request exactly one shared focused Rust engine service, got %d", function, got)
		}
		if got := selectorCount(declaration, "ServiceWithFocusedRustSdkcontent"); got != 0 {
			t.Fatalf("%s must not construct a second focused engine service", function)
		}
	}
	integration := findFunction(t, source, "EngineIntegration")
	if got := selectorCount(integration, "installedBaseline"); got != 1 {
		t.Fatalf("EngineIntegration must construct one shared installed runner baseline, got %d", got)
	}
	resolution := findFunction(t, source, "Resolution")
	if got := selectorCount(resolution, "installedBaseline"); got != 1 {
		t.Fatalf("Resolution must use the common installed runner baseline, got %d", got)
	}
	baseline := findFunction(t, source, "installedBaseline")
	if got := stringLiteralCount(baseline, "--here"); got != 1 {
		t.Fatalf("the common baseline must contain exactly one Rust SDK install, got %d", got)
	}
	runResolution := findFunction(t, source, "runResolution")
	if got := stringLiteralCount(runResolution, "--here"); got != 0 {
		t.Fatalf("the resolution case must not reinstall the shared Rust baseline, got %d", got)
	}

	baseImage := constantString(t, source, "focusedEngineBaseImage")
	if !strings.Contains(baseImage, "@sha256:") {
		t.Fatalf("focused engine baseline must be digest-pinned, got %q", baseImage)
	}
	if revision := constantString(t, source, "focusedEngineBaseCommit"); len(revision) != 40 {
		t.Fatalf("focused engine baseline must use a full commit, got %q", revision)
	}
}

func TestOperationsFixtureSeparatesCheckedGenerationFromClientSchemaLoading(t *testing.T) {
	t.Parallel()

	fixtureSource := parseGoFile(t, "../enginefixture/operations.go")
	for name, expected := range map[string]string{
		"operationsSchemaModule":   ".dagger/modules/client-schema",
		"operationsSchemaConfig":   "/work/.dagger/modules/client-schema/dagger.json",
		"operationsModuleManifest": ".dagger/modules/operations/.dagger/rust/operation-manifest.json",
		"operationsClientRoot":     ".dagger/modules/client-schema/clients/rust",
	} {
		if got := constantString(t, fixtureSource, name); got != expected {
			t.Fatalf("operations fixture anchor %s: got %q, want %q", name, got, expected)
		}
	}
	if got := stringLiteralCount(fixtureSource, "generated-context-changeset"); got != 1 {
		t.Fatalf("operations fixture must traverse the schema module generated context exactly once")
	}
	if got := stringLiteralCount(fixtureSource, "--auto-apply"); got != 0 {
		t.Fatalf("operations fixture must not claim the deliberately absent client initializer")
	}
	if got := stringLiteralCount(fixtureSource, "dagger-rust-sdk:generate-clients"); got != 0 {
		t.Fatalf("operations fixture must invoke the ClientGenerator hook rather than its workspace wrapper")
	}

	integration := findFunction(t, parseGoFile(t, "../../main.go"), "runEngineIntegrationCase")
	if got := selectorCount(integration, "NewOperationsPlan"); got != 1 {
		t.Fatalf("operations case must consume the validated engine-free plan exactly once, got %d", got)
	}
}

func TestForkSDKDependencyRevisionIsExplicitAndContentChecked(t *testing.T) {
	t.Parallel()

	engineContent := findFunction(t, parseGoFile(t, "../../main.go"), "EngineContent")
	if got := selectorCount(engineContent, "SDKDependencyRevision"); got != 2 {
		t.Fatalf("EngineContent must test and forward the explicit SDK dependency revision, got %d references", got)
	}

	generatedClient := findFunction(t, parseGoFile(t, "../dagger/dagger-engine.gen.go"), "RustSdkcontent")
	for _, argument := range []string{"dependencyRepository", "dependencyRevision"} {
		if got := stringLiteralCount(generatedClient, argument); got != 1 {
			t.Fatalf("generated engine client must forward %s exactly once, got %d", argument, got)
		}
	}

	builderSource := parseGoFile(t, "../../../engine-dev/build/sdk.go")
	builder := findFunction(t, builderSource, "RustSDKContent")
	if got := identifierCount(builder, "validateRustSDKGitDependency"); got != 1 {
		t.Fatalf("Rust SDK content must validate its Git dependency exactly once, got %d", got)
	}
	validator := findFunction(t, builderSource, "validateRustSDKGitDependency")
	if got := selectorCount(validator, "Filter"); got != 2 {
		t.Fatalf("Git dependency validation must compare the same focused source closure, got %d filters", got)
	}
	if got := selectorCount(validator, "Diff"); got != 2 {
		t.Fatalf("Git dependency validation must compare local and remote package entries bidirectionally, got %d diffs", got)
	}
	rawBuilder, err := os.ReadFile("../../../engine-dev/build/sdk.go")
	if err != nil {
		t.Fatalf("read engine Rust SDK builder: %v", err)
	}
	if strings.Contains(string(rawBuilder), "dependencyValue = build.vcsCommit") {
		t.Fatalf("local engine provenance must not become the public SDK dependency revision")
	}
}

func TestFocusedEngineStateFollowsRustContentIdentity(t *testing.T) {
	t.Parallel()

	engineSource := parseGoFile(t, "../../../engine-dev/main.go")
	cacheIdentity := findFunction(t, engineSource, "engineStateCacheVolume")
	if got := selectorCount(cacheIdentity, "TrimPrefix"); got != 1 {
		t.Fatalf("engine state cache must normalize exactly one Rust manifest digest, got %d", got)
	}
	if got := stringLiteralCount(cacheIdentity, "sha256:"); got != 1 {
		t.Fatalf("engine state cache must key the canonical Rust manifest identity")
	}
}

func TestRuntimeSemanticDigestExcludesOnlyGeneratedRepresentations(t *testing.T) {
	t.Parallel()

	runtimeSource := parseGoFile(t, "../../../../sdk/rust/runtime/main.go")
	identity := findFunction(t, runtimeSource, "semanticModuleSourceDigest")
	for _, generated := range []string{
		".dagger/rust", ".dagger/rust/**",
		"src/dagger_generated", "src/dagger_generated/**",
		"src/bin/dagger-module.rs",
		".gitattributes", ".gitignore",
	} {
		if got := stringLiteralCount(identity, generated); got != 1 {
			t.Fatalf("semantic module digest must exclude %q exactly once, got %d", generated, got)
		}
	}
	for _, retained := range []string{"dagger-module.toml", "dagger.json"} {
		if got := stringLiteralCount(identity, retained); got != 0 {
			t.Fatalf("semantic module digest must retain module config %q", retained)
		}
	}
	if got := identifierCount(identity, "ExcludeMetadata"); got != 1 {
		t.Fatalf("semantic module digest must exclude unstable file metadata exactly once, got %d", got)
	}
	if got := selectorCount(identity, "UpdatedConfigDirectory"); got != 1 {
		t.Fatalf("semantic module digest must overlay normalized module config exactly once, got %d", got)
	}
}

func TestRuntimeBuildAuditPreservesCrossSDKHygiene(t *testing.T) {
	t.Parallel()

	goRuntime := findFunction(t, parseGoFile(t, "../../../../core/sdk/go_sdk.go"), "Runtime")
	for field, expected := range map[string]int{
		"withEntrypoint": 1,
		"withWorkdir":    1,
		"withoutMount":   2,
	} {
		if got := stringLiteralCount(goRuntime, field); got != expected {
			t.Fatalf("definitive Go runtime audit anchor %s changed: got %d, want %d", field, got, expected)
		}
	}

	pythonTrusted := findFunction(t, parseGoFile(t, "../../../../sdk/python/runtime/main.go"), "moduleRuntimeTrusted")
	for call := range map[string]struct{}{
		"requireGeneratedFiles": {},
		"WithInstall":           {},
	} {
		if got := selectorCount(pythonTrusted, call); got != 1 {
			t.Fatalf("Python checked-generation audit anchor %s changed: got %d", call, got)
		}
	}

	typescriptRuntime := findFunction(t, parseGoFile(t, "../../../../sdk/typescript/runtime/main.go"), "ModuleRuntime")
	if got := selectorCount(typescriptRuntime, "SetupContainer"); got != 3 {
		t.Fatalf("TypeScript runtime variants changed: got %d SetupContainer branches", got)
	}

	javaRuntime := findFunction(t, parseGoFile(t, "../../../../sdk/java/runtime/main.go"), "ModuleRuntime")
	for call := range map[string]struct{}{
		"jreContainer":   {},
		"WithFile":       {},
		"WithEntrypoint": {},
	} {
		if got := selectorCount(javaRuntime, call); got != 1 {
			t.Fatalf("Java promoted-runtime audit anchor %s changed: got %d", call, got)
		}
	}

	rustRuntime := findFunction(t, parseGoFile(t, "../../../../sdk/rust/runtime/runtime.go"), "ModuleRuntime")
	for call, expected := range map[string]int{
		"From":               2,
		"WithMountedCache":   3,
		"WithoutDefaultArgs": 1,
		"WithEntrypoint":     1,
	} {
		if got := selectorCount(rustRuntime, call); got != expected {
			t.Fatalf("Rust runtime must preserve audited %s count: got %d, want %d", call, got, expected)
		}
	}
	for path := range map[string]struct{}{
		"/usr/local/cargo/registry": {},
		"/usr/local/cargo/git":      {},
		"/scratch":                  {},
	} {
		if got := stringLiteralCount(rustRuntime, path); got != 1 {
			t.Fatalf("Rust runtime audit path %q changed: got %d", path, got)
		}
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

func constantString(t *testing.T, file *ast.File, name string) string {
	t.Helper()

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, identifier := range value.Names {
				if identifier.Name != name || index >= len(value.Values) {
					continue
				}
				literal, ok := value.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("constant %s is not a string literal", name)
				}
				decoded, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("decode constant %s: %v", name, err)
				}
				return decoded
			}
		}
	}
	t.Fatalf("constant %s not found", name)
	return ""
}

func variableStringSlice(t *testing.T, file *ast.File, name string) []string {
	t.Helper()

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name || len(value.Values) != 1 {
				continue
			}
			composite, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatalf("variable %s is not a composite literal", name)
			}
			values := make([]string, 0, len(composite.Elts))
			for _, element := range composite.Elts {
				literal, ok := element.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("variable %s contains a non-string case", name)
				}
				decoded, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("decode variable %s: %v", name, err)
				}
				values = append(values, decoded)
			}
			return values
		}
	}
	t.Fatalf("variable %s not found", name)
	return nil
}

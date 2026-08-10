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
	for _, function := range []string{"EngineContent", "EngineIntegration", "Resolution"} {
		if got := selectorCount(generated, function); got != 1 {
			t.Fatalf("generated adapter must dispatch %s exactly once, got %d", function, got)
		}
		if got := stringLiteralCount(generated, function); got != 2 {
			t.Fatalf("generated adapter must expose and dispatch %s, got %d registrations", function, got)
		}
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
	for _, function := range []string{"Resolution", "EngineIntegration"} {
		declaration := findFunction(t, source, function)
		if got := selectorCount(declaration, "ServiceWithFocusedRustSdkcontent"); got != 1 {
			t.Fatalf("%s must start exactly one focused Rust engine service, got %d", function, got)
		}
		if got := selectorCount(declaration, "ServiceWithRustSdkcontent"); got != 0 {
			t.Fatalf("%s must not fall back to the complete engine distribution", function)
		}
	}

	baseImage := constantString(t, source, "focusedEngineBaseImage")
	if !strings.Contains(baseImage, "@sha256:") {
		t.Fatalf("focused engine baseline must be digest-pinned, got %q", baseImage)
	}
	if revision := constantString(t, source, "focusedEngineBaseCommit"); len(revision) != 40 {
		t.Fatalf("focused engine baseline must use a full commit, got %q", revision)
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

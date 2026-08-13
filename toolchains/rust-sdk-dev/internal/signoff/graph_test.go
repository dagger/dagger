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

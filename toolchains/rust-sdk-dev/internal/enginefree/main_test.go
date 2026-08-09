// Package enginefree verifies the Rust SDK development module without initializing
// generated Dagger bindings, which themselves require an active engine session.
package enginefree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
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

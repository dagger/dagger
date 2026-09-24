package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

// The graph views themselves are covered in dagql/dagui (recipe_test.go);
// these cover the command's input handling.

func buildTestID(t *testing.T) *call.ID {
	t.Helper()
	ctrType := &ast.Type{NamedType: "Container", NonNull: true}
	tree := call.New().
		Append(&ast.Type{NamedType: "GitRepository", NonNull: true}, "git",
			call.WithArgs(call.NewArgument("url", call.NewLiteralString("https://example.com/repo"), false))).
		Append(&ast.Type{NamedType: "Directory", NonNull: true}, "tree")
	return call.New().
		Append(ctrType, "container").
		Append(ctrType, "from",
			call.WithArgs(call.NewArgument("address", call.NewLiteralString("alpine"), false))).
		Append(ctrType, "withDirectory",
			call.WithArgs(
				call.NewArgument("path", call.NewLiteralString("/src"), false),
				call.NewArgument("directory", call.NewLiteralID(tree), false),
			))
}

func writeTestID(t *testing.T, id *call.ID) string {
	t.Helper()
	enc, err := id.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id.txt")
	if err := os.WriteFile(path, []byte(enc+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	src, err := load(writeTestID(t, buildTestID(t)))
	if err != nil {
		t.Fatal(err)
	}
	if src.Graph == nil {
		t.Fatal("no graph")
	}
	// container, from, withDirectory, git, tree
	if got := len(src.Graph.Calls()); got != 5 {
		t.Errorf("distinct calls = %d, want 5", got)
	}
	if src.Encoded == 0 || src.RawBytes == 0 {
		t.Errorf("size provenance not recorded: encoded=%d raw=%d", src.Encoded, src.RawBytes)
	}
	if src.dag.GetRecipe() == nil {
		t.Error("proto DAG not retained for -json")
	}
}

// TestLoadStdin covers the no-argument compatibility path: a base64 ID piped
// on stdin, as the pre-flags dump-id consumed it.
func TestLoadStdin(t *testing.T) {
	path := writeTestID(t, buildTestID(t))
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	orig := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = orig }()

	src, err := load("")
	if err != nil {
		t.Fatal(err)
	}
	if src.Label != "<stdin>" {
		t.Errorf("label = %q, want %q", src.Label, "<stdin>")
	}
	if got := len(src.Graph.Calls()); got != 5 {
		t.Errorf("distinct calls = %d, want 5", got)
	}
}

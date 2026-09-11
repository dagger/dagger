package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

// buildTestID constructs a small recipe with a diamond in it:
//
//	container.from(address: "alpine").withDirectory(directory: <ID git.tree>)
//
// where the git chain also hangs off the root, so its calls are reachable
// both as receivers and as a nested ID argument.
func buildTestID(t *testing.T) *call.ID {
	t.Helper()

	dirType := &ast.Type{NamedType: "Directory", NonNull: true}
	ctrType := &ast.Type{NamedType: "Container", NonNull: true}

	tree := call.New().
		Append(&ast.Type{NamedType: "GitRepository", NonNull: true}, "git",
			call.WithArgs(call.NewArgument("url", call.NewLiteralString("https://example.com/repo"), false))).
		Append(dirType, "tree")

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
	if src.graph == nil {
		t.Fatal("no graph")
	}
	// container, from, withDirectory, git, tree
	if got := len(src.graph.calls); got != 5 {
		t.Errorf("distinct calls = %d, want 5", got)
	}
	if got := src.graph.qualName(src.graph.root); got != "Container.withDirectory" {
		t.Errorf("root qualName = %q, want %q", got, "Container.withDirectory")
	}
	if got := len(src.graph.spine(src.graph.root)); got != 3 {
		t.Errorf("root spine depth = %d, want 3", got)
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
	if src.label != "<stdin>" {
		t.Errorf("label = %q, want %q", src.label, "<stdin>")
	}
	if got := len(src.graph.calls); got != 5 {
		t.Errorf("distinct calls = %d, want 5", got)
	}
}

func TestStats(t *testing.T) {
	src, err := load(writeTestID(t, buildTestID(t)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	src.graph.printStats(&buf, src, formatOpts{maxLit: 72, spine: 12})
	out := buf.String()
	for _, want := range []string{
		"distinct calls: 5",
		"root:          Container.withDirectory Container!",
		"Query.git",
		"GitRepository.tree",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("stats output missing %q:\n%s", want, out)
		}
	}
}

func TestTree(t *testing.T) {
	src, err := load(writeTestID(t, buildTestID(t)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	src.graph.printTree(&buf, formatOpts{maxLit: 72, spine: 12}, 0)
	out := buf.String()
	for _, want := range []string{
		`from(address: "alpine")`,
		"arg:directory = ID (2 selectors)",
		`git(url: "https://example.com/repo")`,
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("tree output missing %q:\n%s", want, out)
		}
	}
}

func TestFind(t *testing.T) {
	src, err := load(writeTestID(t, buildTestID(t)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	src.graph.printFind(&buf, regexp.MustCompile(`GitRepository\.tree`), formatOpts{maxLit: 72, spine: 12})
	out := buf.String()
	for _, want := range []string{
		"== 1 call(s) matching",
		"GitRepository.tree -> Directory!",
		"arg:directory",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("find output missing %q:\n%s", want, out)
		}
	}
}

func TestDiff(t *testing.T) {
	base := buildTestID(t)
	extended := base.Append(&ast.Type{NamedType: "Container", NonNull: true}, "withExec",
		call.WithArgs(call.NewArgument("args", call.NewLiteralList(call.NewLiteralString("true")), false)))

	a, err := load(writeTestID(t, base))
	if err != nil {
		t.Fatal(err)
	}
	b, err := load(writeTestID(t, extended))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printDiff(&buf, a, b, formatOpts{maxLit: 72, spine: 12})
	out := buf.String()
	for _, want := range []string{
		"calls:    5 -> 6 (+1)",
		"root spine: 3 -> 4 selectors, identical prefix of 3",
		`withExec(args: ["true"])`,
		"Container.withExec",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("diff output missing %q:\n%s", want, out)
		}
	}
}

// TestPaths verifies the expansion count: a call referenced from two places
// counts one root→call path per reference chain.
func TestPaths(t *testing.T) {
	// the same tree ID is used as an argument twice
	dirType := &ast.Type{NamedType: "Directory", NonNull: true}
	ctrType := &ast.Type{NamedType: "Container", NonNull: true}
	tree := call.New().
		Append(&ast.Type{NamedType: "GitRepository", NonNull: true}, "git",
			call.WithArgs(call.NewArgument("url", call.NewLiteralString("https://example.com/repo"), false))).
		Append(dirType, "tree")
	id := call.New().
		Append(ctrType, "container").
		Append(ctrType, "withDirectory",
			call.WithArgs(call.NewArgument("directory", call.NewLiteralID(tree), false))).
		Append(ctrType, "withDirectory",
			call.WithArgs(call.NewArgument("directory", call.NewLiteralID(tree), false)))

	src, err := load(writeTestID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	g := src.graph
	var treeDgst string
	for dgst := range g.calls {
		if g.qualName(dgst) == "GitRepository.tree" {
			treeDgst = dgst
		}
	}
	if treeDgst == "" {
		t.Fatal("tree call not found")
	}
	if got := g.paths(treeDgst).Int64(); got != 2 {
		t.Errorf("tree expansions = %d, want 2", got)
	}
}

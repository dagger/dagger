package dagui

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

// buildRecipeTestID constructs a small recipe with a diamond in it:
//
//	container.from(address: "alpine").withDirectory(directory: <ID git.tree>)
//
// where the git chain also hangs off the root, so its calls are reachable
// both as receivers and as a nested ID argument.
func buildRecipeTestID(t *testing.T) *call.ID {
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

func recipeSourceOf(t *testing.T, label string, id *call.ID) RecipeSource {
	t.Helper()
	dag, err := id.ToProto()
	if err != nil {
		t.Fatalf("to proto: %v", err)
	}
	return RecipeSource{Label: label, Graph: NewRecipeGraph(dag.GetRecipe())}
}

var recipeTestFormat = RecipeFormat{MaxLiteral: 72, Spine: 12}

func TestRecipeGraph(t *testing.T) {
	g := recipeSourceOf(t, "id", buildRecipeTestID(t)).Graph
	// container, from, withDirectory, git, tree
	if got := len(g.Calls()); got != 5 {
		t.Errorf("distinct calls = %d, want 5", got)
	}
	if got := g.qualName(g.Root()); got != "Container.withDirectory" {
		t.Errorf("root qualName = %q, want %q", got, "Container.withDirectory")
	}
	if got := len(g.spine(g.Root())); got != 3 {
		t.Errorf("root spine depth = %d, want 3", got)
	}
}

func TestRecipeStats(t *testing.T) {
	src := recipeSourceOf(t, "id", buildRecipeTestID(t))
	var buf bytes.Buffer
	src.Graph.WriteStats(&buf, src, recipeTestFormat)
	out := buf.String()
	for _, want := range []string{
		"== id ==",
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

func TestRecipeTree(t *testing.T) {
	src := recipeSourceOf(t, "id", buildRecipeTestID(t))
	var buf bytes.Buffer
	src.Graph.WriteTree(&buf, recipeTestFormat, 0)
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

func TestRecipeFind(t *testing.T) {
	src := recipeSourceOf(t, "id", buildRecipeTestID(t))
	var buf bytes.Buffer
	src.Graph.WriteFind(&buf, regexp.MustCompile(`GitRepository\.tree`), recipeTestFormat)
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

func TestRecipeDiff(t *testing.T) {
	base := buildRecipeTestID(t)
	extended := base.Append(&ast.Type{NamedType: "Container", NonNull: true}, "withExec",
		call.WithArgs(call.NewArgument("args", call.NewLiteralList(call.NewLiteralString("true")), false)))

	a := recipeSourceOf(t, "a", base)
	b := recipeSourceOf(t, "b", extended)
	var buf bytes.Buffer
	WriteRecipeDiff(&buf, a, b, recipeTestFormat)
	out := buf.String()
	for _, want := range []string{
		"== diff a -> b ==",
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

// TestRecipePaths verifies the expansion count: a call referenced from two
// places counts one root→call path per reference chain.
func TestRecipePaths(t *testing.T) {
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

	g := recipeSourceOf(t, "id", id).Graph
	var treeDgst string
	for dgst := range g.Calls() {
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

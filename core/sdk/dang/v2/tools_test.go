package dangv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/core"
)

const dangToolsTestSDL = `
interface Node {
  id: ID!
}

type Query {
  node(id: ID!): Node
  """
  Creates a scratch container.
  """
  container: Container!
  """
  Reads a secret.
  """
  secret(name: String!): Secret!
}

"""
An OCI-compatible container.
"""
type Container implements Node {
  id: ID!
  """
  Execute a command in the container.
  """
  withExec(args: [String!]!): Container!
  """
  The output stream of the last executed command.
  """
  stdout: String!
  """
  Forces evaluation of the pipeline.
  """
  sync: Container!
  """
  Gives a conversation a module's tools.
  """
  install(llm: ID!, ref: String!): LLM!
}

type Directory implements Node {
  id: ID!
  entries: [String!]!
}

type Workspace implements Node {
  id: ID!
  """
  Returns a Directory from the workspace.
  """
  directory(path: String!): Directory!
  withNewFile(path: String!, contents: String!): Workspace!
  changes: Changeset!
}

type Changeset implements Node {
  id: ID!
}

type Secret implements Node {
  id: ID!
  plaintext: String!
}

type LLM implements Node {
  id: ID!
  withPrompt(prompt: String!): LLM!
}

type Agent implements Node {
  id: ID!
  handle: String!
}
`

func dangToolsTestSchema(t *testing.T) *introspection.Schema {
	t.Helper()
	schema, err := dang.SchemaFromSDL(dangToolsTestSDL, "test.graphqls")
	require.NoError(t, err)
	// An object is passed for an ID argument the API expects one of, as the
	// engine's schema says with @expectedType, which SDL doesn't carry.
	for _, field := range schema.Types.Get("Container").Fields {
		if field.Name != "install" {
			continue
		}
		for i, arg := range field.Args {
			if arg.Name == "llm" {
				name := `"LLM"`
				field.Args[i].Directives = introspection.Directives{{
					Name: "expectedType",
					Args: []*introspection.DirectiveArg{{Name: "name", Value: &name}},
				}}
			}
		}
	}
	return schema
}

func workspaceTestScope(t *testing.T) *dangScope {
	return &dangScope{
		fsys:   fstest.MapFS{},
		dir:    ".",
		label:  "workspace scope",
		values: valueDeclarations([]dangValue{{name: "ws", typeName: "Workspace", id: "ws-id"}}),
		schema: dangToolsTestSchema(t),
	}
}

// moduleTestScope is a module at mod/ of a workspace.
func moduleTestScope(t *testing.T, files map[string]string) *dangScope {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return &dangScope{
		fsys:   subdirFS{prefix: "mod", fsys: fsys},
		dir:    "mod",
		module: true,
		label:  "module mod (mod)",
		schema: dangToolsTestSchema(t),
	}
}

func checkTest(t *testing.T, sc *dangScope, source, search string) string {
	t.Helper()
	out, err := checkDang(context.Background(), sc, source, search)
	require.NoError(t, err)
	return out.Text
}

func TestDangCheckWorkspaceScope(t *testing.T) {
	t.Parallel()
	sc := workspaceTestScope(t)

	t.Run("a value's type, and an object's members", func(t *testing.T) {
		out := checkTest(t, sc, `["a"]`, "")
		require.Equal(t, "[String!]!\nok · workspace scope · nothing ran\n", out, "a list's members are a lookup of List away")
		out = checkTest(t, sc, `container.withExec(args: ["x"])`, "")
		require.Contains(t, out, "Dagger.Container! has ")
		require.Contains(t, out, "\n  withExec(args: [String!]!): Dagger.Container! — Execute a command in the container.\n")
		// The type name is the discovery path for a list's members.
		out = checkTest(t, sc, `List`, "")
		require.Contains(t, out, "\n  reduce(initial: b) { acc: b, item: a => b }: b — ")
		require.True(t, strings.HasSuffix(out, "ok · workspace scope · nothing ran\n"), out)
	})

	t.Run("a member reference", func(t *testing.T) {
		out := checkTest(t, sc, `[1].reduce`, "")
		require.True(t, strings.HasPrefix(out, "List.reduce(initial: b) { acc: b, item: a => b }: b\n"), out)
		require.Contains(t, out, "\n  example: ")
		require.NotContains(t, out, "error")
	})

	t.Run("a static member", func(t *testing.T) {
		out := checkTest(t, sc, `JSON.encode`, "")
		require.Contains(t, out, "JSON.encode(")
	})

	t.Run("a schema type", func(t *testing.T) {
		out := checkTest(t, sc, `Container`, "")
		require.True(t, strings.HasPrefix(out, "type Container\n  An OCI-compatible container.\n"), out)
		require.Contains(t, out, "\n  withExec(args: [String!]!): Dagger.Container! — Execute a command in the container.\n")
	})

	t.Run("a member of a schema type", func(t *testing.T) {
		out := checkTest(t, sc, `Container.withExec`, "")
		require.True(t, strings.HasPrefix(out, "Container.withExec(args: [String!]!): Dagger.Container!\n  Execute a command in the container.\n"), out)
	})

	t.Run("a member selected from a value", func(t *testing.T) {
		out := checkTest(t, sc, `container.withExec`, "")
		require.True(t, strings.HasPrefix(out, "Dagger.Container.withExec(args: [String!]!): Dagger.Container!\n"), out)
	})

	t.Run("ws is bound", func(t *testing.T) {
		out := checkTest(t, sc, `ws`, "")
		require.True(t, strings.HasPrefix(out, "ws: Dagger.Workspace!\n"), out)
		require.Contains(t, out, "Dagger.Workspace! has 4 members:")
		require.Contains(t, out, "directory(path: String!): Dagger.Directory! — Returns a Directory from the workspace.")
		require.NotContains(t, out, "declared at")
	})

	t.Run("a type error", func(t *testing.T) {
		out := checkTest(t, sc, "let xs = [\"b\"]\nxs.size", "")
		require.Contains(t, out, "<check>:2:1: error: ")
		require.Contains(t, out, `"size"`)
		require.True(t, strings.HasSuffix(out, "1 error · workspace scope · nothing ran\n"), out)
	})

	t.Run("a type error says what exists", func(t *testing.T) {
		out := checkTest(t, sc, "let names = [\"b\", \"a\"]\nnames.findFirst { n => n == \"a\" }", "")
		require.Contains(t, out, "<check>:2:1: error: list does not have method \"findFirst\"\n  closest: find { item: a => Boolean! }: a?  returns the first element")
		require.Contains(t, out, "\n  [String!]! has 25 members: all any contains")
		out = checkTest(t, sc, `ws.directry(path: ".")`, "")
		require.Contains(t, out, "\n  closest: directory(path: String!): Dagger.Directory!  Returns a Directory from the workspace.\n")
	})

	t.Run("a deprecation", func(t *testing.T) {
		out := checkTest(t, sc, `toJSON([1])`, "")
		require.Contains(t, out, "<check>:1:1: warning: toJSON is deprecated: use JSON.encode instead (quick fix: JSON.encode)")
	})

	t.Run("search", func(t *testing.T) {
		out := checkTest(t, sc, "", "accumulator")
		require.True(t, strings.HasPrefix(out, "search \"accumulator\":\n  List.reduce(initial: b)"), out)
		out = checkTest(t, sc, "", "execute command")
		require.Contains(t, out, "  Container.withExec(args: [String!]!): Dagger.Container! — Execute a command in the container.\n")
		out = checkTest(t, sc, "", "zzznothing")
		require.Contains(t, out, `search "zzznothing": no matches`)
	})

	t.Run("search lists a type once", func(t *testing.T) {
		out := checkTest(t, sc, "", "container")
		require.Equal(t, 1, strings.Count(out, "type Container"), out)
		require.Contains(t, out, "  type Container — An OCI-compatible container.\n")
		require.NotContains(t, out, "Dagger.type")
	})

	t.Run("nothing to check", func(t *testing.T) {
		_, err := checkDang(context.Background(), sc, "", "")
		require.ErrorContains(t, err, "nothing to check")
	})
}

const testModuleMain = `"""
Sorts names, uppercase first.
"""
let stableOrder(items: [String!]!): [String!]! {
  items.sort
}

pub greet(name: String!): String! {
  let greeting = "hello, "
  greeting + name
}
`

func TestDangCheckModuleScope(t *testing.T) {
	t.Parallel()

	t.Run("diagnostics with workspace paths", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{
			"main.dang":   testModuleMain,
			"broken.dang": "let s = toJSON([1])\nlet n: Int! = \"nope\"\n",
		})
		out := checkTest(t, sc, "", "")
		require.Contains(t, out, "mod/broken.dang:1:9: warning: toJSON is deprecated")
		require.Contains(t, out, "mod/broken.dang:2:")
		require.True(t, strings.HasSuffix(out, "1 error, 1 warning · module mod (mod) · 2 files · nothing ran\n"), out)
	})

	t.Run("a clean module", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{"main.dang": testModuleMain})
		out := checkTest(t, sc, "", "")
		require.Equal(t, "ok · module mod (mod) · 1 file · nothing ran\n", out)
	})

	t.Run("one file", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{
			"main.dang":   testModuleMain,
			"broken.dang": "let n: Int! = \"nope\"\n",
		})
		sc.file = "mod/main.dang"
		out := checkTest(t, sc, "", "")
		require.NotContains(t, out, "broken.dang")
		require.True(t, strings.HasPrefix(out, "ok · "), out)
	})

	t.Run("a private helper and its declaration", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{"main.dang": testModuleMain})
		out := checkTest(t, sc, "stableOrder", "")
		require.True(t, strings.HasPrefix(out, "stableOrder(items: [String!]!): [String!]!\n  Sorts names, uppercase first.\n  declared at mod/main.dang:4:5\n"), out)
	})

	t.Run("search finds declarations", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{"main.dang": testModuleMain})
		out := checkTest(t, sc, "", "uppercase")
		require.Contains(t, out, "  stableOrder(items: [String!]!): [String!]! — Sorts names, uppercase first.\n")
	})

	t.Run("at a position", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{"main.dang": testModuleMain})
		sc.file = "mod/main.dang"
		sc.pos = &analysis.Pos{Line: 10, Column: 3}
		out := checkTest(t, sc, "greeting + name", "")
		require.True(t, strings.HasPrefix(out, "String!\n"), out)
		out = checkTest(t, sc, "name.size", "")
		require.Contains(t, out, "<check>:1:1: error: ")
		require.Contains(t, out, "1 error · module mod (mod) · 1 file · nothing ran\n")
		// Outside the function, its locals aren't in scope.
		sc.pos = &analysis.Pos{Line: 1, Column: 1}
		out = checkTest(t, sc, "greeting", "")
		require.Contains(t, out, "error: ")
	})
}

// fakeAPI answers the queries a snippet sends with canned data.
type fakeAPI struct {
	mu      sync.Mutex
	queries []string
	answer  func(query string) string
	// fail, if set, fails the queries it returns an error for.
	fail func(query string) error
}

func (f *fakeAPI) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	f.mu.Lock()
	f.queries = append(f.queries, req.Query)
	f.mu.Unlock()
	if f.fail != nil {
		if err := f.fail(req.Query); err != nil {
			return err
		}
	}
	return json.Unmarshal([]byte(f.answer(req.Query)), resp.Data)
}

func evalTest(t *testing.T, sc *dangScope, source string, api *fakeAPI) (string, error) {
	t.Helper()
	rep, err := evalReport(t, sc, source, api)
	return rep.Text, err
}

// evalReport runs source and returns its report, checking what it logged to
// the tool call's span: the same, line for line.
func evalReport(t *testing.T, sc *dangScope, source string, api *fakeAPI) (core.DangReport, error) {
	t.Helper()
	if api == nil {
		api = &fakeAPI{answer: func(q string) string { t.Errorf("unexpected query %s", q); return "{}" }}
	}
	var spanLogs strings.Builder
	rep, err := evalDang(context.Background(), sc, source, api, &spanLogs, nil)
	if err == nil {
		require.Equal(t, rep.Text, spanLogs.String(), "the span's logs are the report")
	}
	return rep, err
}

func TestDangEval(t *testing.T) {
	t.Parallel()
	sc := workspaceTestScope(t)

	t.Run("output and result", func(t *testing.T) {
		out, err := evalTest(t, sc, "print(\"hello\")\n[1, 2].map { x => x * 2 }", nil)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(out, "output:\nhello\n[Int!]! = [2, 4]\nok · "), out)
		require.Regexp(t, `\nok · \d+ms · 0 engine calls · workspace scope\n$`, out)
	})

	t.Run("only an object's members are listed", func(t *testing.T) {
		for _, source := range []string{`"s"`, `1`, `true`, `[container]`, `{{a: 1}}`, "let c: Container = null\nc"} {
			out, err := evalTest(t, sc, source, nil)
			require.NoError(t, err)
			require.NotContains(t, out, "members:", source)
		}
		out, err := evalTest(t, sc, `container`, nil)
		require.NoError(t, err)
		require.Contains(t, out, "members: id install stdout sync withExec\n")
	})

	t.Run("objects stay lazy", func(t *testing.T) {
		out, err := evalTest(t, sc, `container.withExec(["echo", "hi"])`, nil)
		require.NoError(t, err)
		require.Contains(t, out, "Container! = <Container: container.withExec(args: …)>\n(not evaluated; select a field such as .sync or .stdout to run it)\n")
		require.Contains(t, out, "members: id install stdout sync withExec\n")
		require.Contains(t, out, " · 0 engine calls · ")
	})

	t.Run("engine calls are counted", func(t *testing.T) {
		api := &fakeAPI{answer: func(string) string {
			return `{"container":{"withExec":{"stdout":"hi\n"}}}`
		}}
		out, err := evalTest(t, sc, `container.withExec(["echo", "hi"]).stdout`, api)
		require.NoError(t, err)
		require.Contains(t, out, "String! = \"hi\\n\"\n")
		require.Contains(t, out, " · 1 engine call · ")
		require.Len(t, api.queries, 1)
	})

	t.Run("a type error runs nothing", func(t *testing.T) {
		out, err := evalTest(t, sc, "print(\"never\")\nnope", nil)
		require.NoError(t, err, "a type error is reported, not a tool error")
		require.Contains(t, out, "<check>:2:1: error: ")
		require.Contains(t, out, "1 error · workspace scope · nothing ran")
		require.NotContains(t, out, "never\n")
	})

	t.Run("a failure keeps its output", func(t *testing.T) {
		out, err := evalTest(t, sc, "print(\"before\")\nraise \"boom\"", nil)
		require.NoError(t, err, "a failure is reported, not a tool error")
		require.True(t, strings.HasPrefix(out, "output:\nbefore\n"), out)
		require.Contains(t, out, "boom")
		require.Contains(t, out, "failed · ")
	})

	t.Run("secrets are scrubbed", func(t *testing.T) {
		api := &fakeAPI{answer: func(string) string {
			return `{"secret":{"plaintext":"hunter2"}}`
		}}
		out, err := evalTest(t, sc, "let s = secret(name: \"pw\").plaintext\nprint(\"pw is \" + s)\ns", api)
		require.NoError(t, err)
		require.NotContains(t, out, "hunter2")
		require.Contains(t, out, "pw is ***")
		require.Contains(t, out, `String! = "***"`)
	})

	t.Run("a module's top level runs first", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{
			"main.dang": testModuleMain + "\nprint(\"module loaded\")\n",
		})
		out, err := evalTest(t, sc, `greet("dang")`, nil)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(out, "output:\nmodule loaded\nString! = \"hello, dang\"\n"), out)
		require.Contains(t, out, " · module mod (mod)\n")
	})

	t.Run("a long result is described in full", func(t *testing.T) {
		// Bounding what the model sees is the engine's, as for a module
		// tool's output; the report and the logs hold it all.
		stdout := func(s string) *fakeAPI {
			return &fakeAPI{answer: func(string) string {
				data, err := json.Marshal(map[string]any{"container": map[string]any{"withExec": map[string]any{"stdout": s}}})
				require.NoError(t, err)
				return string(data)
			}}
		}
		var file strings.Builder
		for i := range 300 {
			fmt.Fprintf(&file, "line %03d of a README\n", i)
		}
		rep, err := evalReport(t, sc, "print(\"reading\")\ncontainer.withExec([\"cat\"]).stdout", stdout(file.String()))
		require.NoError(t, err)
		// A file's contents read as its lines, between verbatim quotes.
		require.Contains(t, rep.Text, "output:\nreading\nString! = '''\n"+file.String()+"'''\nok · ")
		require.False(t, rep.Rejected)

		// Verbatim: a missing final newline is said, not added.
		out, err := evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(strings.TrimSuffix(file.String(), "\n")))
		require.NoError(t, err)
		require.Contains(t, out, "line 299 of a README\n''' (no final newline)\nok · ")

		// `${` and `\` are text in a `'''` block, and so is `'''`: the
		// block says it ends at its last line.
		script := file.String() + "echo \"${HOME}\" \\\n"
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(script))
		require.NoError(t, err)
		require.Contains(t, out, "String! = '''\n"+script+"'''\nok · ")
		script += "say '''hi''' \"\"\"\n"
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(script))
		require.NoError(t, err)
		require.Contains(t, out, "String! = '''\n"+script+"''' (the text holds '''; the block ends at its last line)\nok · ")

		// A one-line string that fits a line isn't cut, nor a list of a few
		// hundred elements.
		line := strings.Repeat("ab", 700)
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(line))
		require.NoError(t, err)
		require.Contains(t, out, `String! = "`+line+`"`)
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout.split("")`, stdout(line[:500]))
		require.NoError(t, err)
		require.Equal(t, 500, strings.Count(out, `"a"`)+strings.Count(out, `"b"`), out)
		require.NotContains(t, out, "more)")
		require.NotContains(t, out, "omitted")
		// A longer one is counted past dangItemsMax.
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout.split("")`, stdout(line))
		require.NoError(t, err)
		require.Contains(t, out, fmt.Sprintf("… (%d more)", len(line)-dangItemsMax))
		// Nested collections keep Describe's default, so a result's work
		// doesn't grow as dangItemsMax^depth.
		out, err = evalTest(t, sc, "let xs = container.withExec([\"cat\"]).stdout.split(\"\")\nxs.map { x => xs }", stdout(line))
		require.NoError(t, err)
		require.Contains(t, out, "… (1368 more)")
		require.Less(t, len(out), 64<<10)

		// A block's line too long for the line clamp keeps its end.
		out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(file.String()+strings.Repeat("y", 5000)+"THE END\n"))
		require.NoError(t, err)
		require.Contains(t, out, "bytes omitted)…yyy")
		require.Contains(t, out, "THE END\n''' (long lines cut in the middle)\n")
		for l := range strings.SplitSeq(out, "\n") {
			require.LessOrEqual(t, len(l), dangLineMax, "every line fits the line clamp")
		}

		// A one-line string a line can't hold keeps its head and its tail,
		// where a line clamp would keep only its head, whether its
		// characters are narrow, wide or escaped.
		for _, long := range []string{
			strings.Repeat("x", 3000) + "THE END",
			strings.Repeat("日本語", 1000) + "THE END",
			strings.Repeat(`["a","b","c"],`, 300) + "THE END",
		} {
			out, err = evalTest(t, sc, `container.withExec(["cat"]).stdout`, stdout(long))
			require.NoError(t, err)
			require.Contains(t, out, "chars omitted)…")
			require.Contains(t, out, `THE END"`)
			for l := range strings.SplitSeq(out, "\n") {
				require.LessOrEqual(t, len(l), dangLineMax, "every line fits the line clamp")
			}
		}
	})

	t.Run("a short multi-line string stays quoted", func(t *testing.T) {
		out, err := evalTest(t, sc, `"a\nb\n"`, nil)
		require.NoError(t, err)
		require.Contains(t, out, "String! = \"a\\nb\\n\"\n")
	})
}

// Printed output is logged under the report's heading for it, and the rest
// of a report on a line of its own, after output that didn't end one.
func TestLineEndWriter(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	w := &lineEndWriter{w: &logs, heading: dangOutputHeading}
	w.WriteRest("")
	_, _ = w.Write(nil)
	require.Empty(t, logs.String(), "no output, no heading")
	_, _ = w.Write([]byte("no newline"))
	w.WriteRest("Int! = 1\n")
	require.Equal(t, "output:\nno newline\nInt! = 1\n", logs.String())

	logs.Reset()
	w = &lineEndWriter{w: &logs, heading: dangOutputHeading}
	_, _ = w.Write([]byte("line\n"))
	w.WriteRest("Int! = 1\n")
	require.Equal(t, "output:\nline\nInt! = 1\n", logs.String())
}

// A snippet that fails to parse or type-check is rejected: the engine points
// the model at the dang-language skill. Nothing else is.
func TestDangRejected(t *testing.T) {
	t.Parallel()
	sc := workspaceTestScope(t)
	for _, tc := range []struct {
		name, source string
		rejected     bool
	}{
		{"a type error", "nope", true},
		{"a parse error", "let x = ", true},
		{"a runtime error", `raise "boom"`, false},
		{"success", "1 + 1", false},
	} {
		t.Run("DangEval: "+tc.name, func(t *testing.T) {
			rep, err := evalReport(t, sc, tc.source, nil)
			require.NoError(t, err)
			require.Equal(t, tc.rejected, rep.Rejected, rep.Text)
		})
		t.Run("DangCheck: "+tc.name, func(t *testing.T) {
			if tc.name == "a runtime error" {
				t.Skip("DangCheck runs nothing")
			}
			rep, err := checkDang(context.Background(), sc, tc.source, "")
			require.NoError(t, err)
			require.Equal(t, tc.rejected, rep.Rejected, rep.Text)
		})
	}

	t.Run("DangCheck: a module's own errors", func(t *testing.T) {
		sc := moduleTestScope(t, map[string]string{"main.dang": "pub broken: String! { nope }\n"})
		rep, err := checkDang(context.Background(), sc, "", "")
		require.NoError(t, err)
		require.Contains(t, rep.Text, "error")
		require.False(t, rep.Rejected, "only a snippet is rejected")
	})
}

func TestDangBoundValues(t *testing.T) {
	t.Parallel()
	sc := workspaceTestScope(t)
	sc.values = valueDeclarations([]dangValue{
		{name: "ws", typeName: "Workspace", id: "ws-id"},
		{name: "scratch", typeName: "Directory", id: "dir-id"},
	})

	out := checkTest(t, sc, "scratch", "")
	require.True(t, strings.HasPrefix(out, "scratch: Dagger.Directory!\n"), out)

	api := &fakeAPI{answer: func(q string) string {
		require.Contains(t, q, "dir-id", "the bound value is loaded from its ID")
		if strings.Contains(q, "__typename") {
			return `{"node":{"__typename":"Directory"}}`
		}
		return `{"node":{"entries":["a.txt"]}}`
	}}
	out, err := evalTest(t, sc, "scratch.entries", api)
	require.NoError(t, err)
	require.Contains(t, out, `[String!]! = ["a.txt"]`)
	require.Regexp(t, ` · \d engine calls · `, out)

	t.Run("a value that fails to load is named, not its hidden declaration", func(t *testing.T) {
		sc := workspaceTestScope(t)
		bound := []dangValue{
			{name: "ws", typeName: "Workspace", id: "ws-id"},
			{name: "scratch", typeName: "Directory", id: "dir-id"},
		}
		sc.values = valueDeclarations(bound)
		sc.bound = bound
		api := &fakeAPI{
			answer: func(string) string { return "{}" },
			fail: func(q string) error {
				if strings.Contains(q, "dir-id") {
					return errors.New("module: load: no host")
				}
				return nil
			},
		}
		out, err := evalTest(t, sc, "scratch.entries", api)
		require.NoError(t, err, "a failure is reported, not a tool error")
		require.Contains(t, out, "error: `scratch` (bound tool object) failed to load: ")
		require.Contains(t, out, "module: load: no host")
		require.NotContains(t, out, "<workspace>")
		require.Contains(t, out, "failed · ")
	})
}

// writeAPI answers the queries of snippets over a bound workspace ("ws-id")
// and container ("box-id"): each new value's ID is its call's name.
func writeAPI() *fakeAPI {
	return &fakeAPI{answer: func(q string) string {
		switch {
		case strings.Contains(q, "__typename") && strings.Contains(q, "ws-id"):
			return `{"node":{"__typename":"Workspace"}}`
		case strings.Contains(q, "__typename"):
			return `{"node":{"__typename":"Container"}}`
		case strings.Contains(q, "withNewFile"):
			return `{"node":{"withNewFile":{"id":"ws-2"}}}`
		case strings.Contains(q, "withExec"):
			return `{"node":{"withExec":{"id":"box-2"}}}`
		}
		return `{}`
	}}
}

// recordWrites is a writer that records what it is given.
func recordWrites(got *[]dangWrite, calls *int) dangWriter {
	return func(_ context.Context, writes []dangWrite) (string, error) {
		*calls++
		*got = writes
		if len(writes) == 0 {
			return "0 writes", nil
		}
		names := make([]string, len(writes))
		for i, w := range writes {
			names[i] = w.name + " written"
		}
		return strings.Join(names, " · "), nil
	}
}

func TestDangEvalWrites(t *testing.T) {
	t.Parallel()
	bound := []dangValue{
		{name: "ws", typeName: "Workspace", id: "ws-id"},
		{name: "box", typeName: "Container", id: "box-id"},
	}
	scope := func(sc *dangScope) *dangScope {
		sc.values = valueDeclarations(bound)
		sc.bound = bound
		return sc
	}
	eval := func(t *testing.T, sc *dangScope, source string) (string, []dangWrite, int) {
		t.Helper()
		var got []dangWrite
		var calls int
		out, err := evalDang(context.Background(), sc, source, writeAPI(), nil, recordWrites(&got, &calls))
		require.NoError(t, err)
		return out.Text, got, calls
	}

	t.Run("assigning ws writes it", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), "ws = ws.withNewFile(path: \"a\", contents: \"b\")\n\"done\"")
		require.Equal(t, []dangWrite{{name: "ws", typeName: "Workspace", id: "ws-2"}}, got)
		require.True(t, strings.HasSuffix(out, "\nws written\n"), out)
	})

	t.Run("assigning a tool object rebinds it", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), `box = box.withExec(args: ["x"])`)
		require.Equal(t, []dangWrite{{name: "box", typeName: "Container", id: "box-2"}}, got)
		require.Contains(t, out, "Container! = <Container: ")
		require.NotContains(t, out, "(not evaluated", "an assignment is what it meant to do")
	})

	t.Run("ending in an assignment to ws gives no hint", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), `ws = ws.withNewFile(path: "a", contents: "b")`)
		require.Equal(t, []dangWrite{{name: "ws", typeName: "Workspace", id: "ws-2"}}, got)
		require.Contains(t, out, "Workspace! = <Workspace: node(id: …).withNewFile(contents: …, path: …)>\n")
		require.NotContains(t, out, "(not evaluated", "an assignment is what it meant to do")
		require.NotContains(t, out, "(not applied")
		require.True(t, strings.HasSuffix(out, "\nws written\n"), out)
	})

	t.Run("ending in an assignment to a local keeps the hint", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), "let c = box\nc = c.withExec(args: [\"x\"])")
		require.Empty(t, got)
		require.Contains(t, out, "(not evaluated; select a field such as .sync or .stdout to run it)")
	})

	t.Run("ending in an assignment to a module's binding keeps the hint", func(t *testing.T) {
		sc := scope(moduleTestScope(t, map[string]string{
			"main.dang": "let base: Container! = container.withExec(args: [\"a\"])\n",
		}))
		out, got, _ := eval(t, sc, `base = base.withExec(args: ["b"])`)
		require.Empty(t, got)
		require.Contains(t, out, "(not evaluated; select a field such as .sync or .stdout to run it)", "nothing ran, and nothing was written")
	})

	t.Run("calling methods writes nothing", func(t *testing.T) {
		out, got, calls := eval(t, scope(workspaceTestScope(t)), `ws.withNewFile(path: "a", contents: "b")`)
		require.Empty(t, got)
		require.Zero(t, calls)
		require.Contains(t, out, "(not applied; to write it, assign it: ws = …)")
		require.True(t, strings.HasSuffix(out, "\n0 writes\n"), out)
	})

	t.Run("a read-only probe reports no writes", func(t *testing.T) {
		out, _, calls := eval(t, scope(workspaceTestScope(t)), `[1, 2].length`)
		require.Zero(t, calls)
		require.NotContains(t, out, "writes")
	})

	t.Run("an unassigned changeset is not applied", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), `ws.changes`)
		require.Empty(t, got)
		require.Contains(t, out, "(not applied; to write it, assign it: ws = ws.withChanges(…))")
	})

	t.Run("an earlier value stays readable", func(t *testing.T) {
		out, got, _ := eval(t, scope(workspaceTestScope(t)), "let before = ws\nws = ws.withNewFile(path: \"a\", contents: \"b\")\n[before, ws]")
		require.Equal(t, []dangWrite{{name: "ws", typeName: "Workspace", id: "ws-2"}}, got)
		require.Contains(t, out, "<Workspace: node(id: …)>")
		require.Contains(t, out, "<Workspace: node(id: …).withNewFile(contents: …, path: …)>")
	})

	t.Run("a failure writes nothing", func(t *testing.T) {
		out, got, calls := eval(t, scope(workspaceTestScope(t)), "ws = ws.withNewFile(path: \"a\", contents: \"b\")\nraise \"boom\"")
		require.Empty(t, got)
		require.Zero(t, calls)
		require.Contains(t, out, "failed · ")
	})

	t.Run("a type error writes nothing", func(t *testing.T) {
		_, _, calls := eval(t, scope(workspaceTestScope(t)), "ws = ws.withNewFile(path: \"a\")")
		require.Zero(t, calls)
	})

	t.Run("in a module's scope", func(t *testing.T) {
		sc := scope(moduleTestScope(t, map[string]string{"main.dang": testModuleMain}))
		out, got, _ := eval(t, sc, "ws = ws.withNewFile(path: \"a\", contents: greet(\"x\"))")
		require.Equal(t, []dangWrite{{name: "ws", typeName: "Workspace", id: "ws-2"}}, got)
		require.Contains(t, out, " · module mod (mod)\n")
	})
}

// conversationBound are bound values of a conversation's scope: its
// workspace, the conversation, its agent and a tool object.
var conversationBound = []dangValue{
	{name: "ws", typeName: "Workspace", id: "ws-id"},
	{name: "chat", typeName: "LLM", id: "chat-id"},
	{name: "me", typeName: "Agent", id: "me-id", readOnly: true},
	{name: "box", typeName: "Container", id: "box-id"},
}

// conversationScope is sc binding bound.
func conversationScope(sc *dangScope, bound []dangValue) *dangScope {
	sc.values = valueDeclarations(bound)
	sc.bound = bound
	return sc
}

// conversationAPI answers the queries of snippets over conversationBound:
// each new value's ID is its call's name.
func conversationAPI() *fakeAPI {
	return &fakeAPI{answer: func(q string) string {
		switch {
		case strings.Contains(q, "__typename") && strings.Contains(q, "chat-id"):
			return `{"node":{"__typename":"LLM"}}`
		case strings.Contains(q, "__typename") && strings.Contains(q, "me-id"):
			return `{"node":{"__typename":"Agent"}}`
		case strings.Contains(q, "__typename") && strings.Contains(q, "ws-id"):
			return `{"node":{"__typename":"Workspace"}}`
		case strings.Contains(q, "__typename"):
			return `{"node":{"__typename":"Container"}}`
		case strings.Contains(q, "install"):
			return `{"node":{"install":{"id":"chat-2"}}}`
		case strings.Contains(q, "withPrompt"):
			return `{"node":{"withPrompt":{"id":"chat-3"}}}`
		case strings.Contains(q, "handle"):
			return `{"node":{"handle":"agent-handle"}}`
		case strings.Contains(q, "withNewFile"):
			return `{"node":{"withNewFile":{"id":"ws-2"}}}`
		case strings.Contains(q, "withExec"):
			return `{"node":{"withExec":{"id":"box-2"}}}`
		}
		return `{}`
	}}
}

// `chat` is written as any other binding is, but alone: the LLM it assigns
// carries its own workspace and tool objects. `me` is read-only.
func TestDangEvalConversationWrites(t *testing.T) {
	t.Parallel()
	eval := func(t *testing.T, sc *dangScope, source string) (core.DangReport, []dangWrite, int, *fakeAPI) {
		t.Helper()
		var got []dangWrite
		var calls int
		api := conversationAPI()
		rep, err := evalDang(context.Background(), sc, source, api, nil, recordWrites(&got, &calls))
		require.NoError(t, err)
		return rep, got, calls, api
	}
	scope := func(t *testing.T) *dangScope {
		return conversationScope(workspaceTestScope(t), conversationBound)
	}

	t.Run("assigning chat writes it", func(t *testing.T) {
		rep, got, _, _ := eval(t, scope(t), `chat = chat.withPrompt(prompt: "hi")`)
		require.Equal(t, []dangWrite{{name: "chat", typeName: "LLM", id: "chat-3"}}, got, rep.Text)
		require.True(t, strings.HasSuffix(rep.Text, "\nchat written\n"), rep.Text)
		require.NotContains(t, rep.Text, "members:", "the write's summary says what came of it")
		require.NotContains(t, rep.Text, "(not evaluated", "an assignment is what it meant to do")
	})

	t.Run("a tool object read to make chat isn't written", func(t *testing.T) {
		rep, got, _, _ := eval(t, scope(t), `chat = box.install(llm: chat, ref: "github.com/x/y")`)
		require.Equal(t, []dangWrite{{name: "chat", typeName: "LLM", id: "chat-2"}}, got, rep.Text)
	})

	for _, tc := range []struct{ name, source, other string }{
		{"chat and ws", "ws = ws.withNewFile(path: \"a\", contents: \"b\")\nchat = chat.withPrompt(prompt: \"hi\")", "`ws`"},
		{"chat and a tool object", "box = box.withExec(args: [\"x\"])\nchat = box.install(llm: chat, ref: \"r\")", "`box`"},
	} {
		t.Run(tc.name+" together run nothing", func(t *testing.T) {
			rep, got, calls, api := eval(t, scope(t), tc.source)
			require.Empty(t, got)
			require.Zero(t, calls)
			require.Empty(t, api.queries, "nothing ran")
			require.True(t, rep.Rejected)
			require.Contains(t, rep.Text, "error: cannot assign to `chat` and "+tc.other+" in one snippet: "+core.DangChatAloneMessage)
			require.Contains(t, rep.Text, "1 error · workspace scope · nothing ran")
		})
	}

	t.Run("me is read-only", func(t *testing.T) {
		rep, got, calls, api := eval(t, scope(t), "me = null\n1")
		require.Empty(t, got)
		require.Zero(t, calls)
		require.Empty(t, api.queries, "nothing ran")
		require.Contains(t, rep.Text, "<check>:1:1: error: cannot assign to `me`: it is the conversation's agent, bound read-only")
	})

	t.Run("me reads the agent", func(t *testing.T) {
		rep, got, _, _ := eval(t, scope(t), `me.handle`)
		require.Empty(t, got)
		require.Contains(t, rep.Text, `String! = "agent-handle"`)
	})

	t.Run("me is null without an agent", func(t *testing.T) {
		bound := slices.Clone(conversationBound)
		bound[2].id = ""
		sc := conversationScope(workspaceTestScope(t), bound)
		rep, _, _, _ := eval(t, sc, `me == null`)
		require.Contains(t, rep.Text, "Boolean! = true")
		require.Contains(t, checkTest(t, sc, "me", ""), "me: Dagger.Agent\n")
	})

	t.Run("a local shadowing a binding isn't it", func(t *testing.T) {
		rep, got, calls, _ := eval(t, scope(t), "[1].map { x =>\n  let me = x\n  let chat = x\n  me = 2\n  chat = 3\n  ws = ws.withNewFile(path: \"a\", contents: \"b\")\n  me + chat\n}")
		require.False(t, rep.Rejected, rep.Text)
		require.Equal(t, []dangWrite{{name: "ws", typeName: "Workspace", id: "ws-2"}}, got, rep.Text)
		require.Equal(t, 1, calls)
	})

	t.Run("in a module's scope", func(t *testing.T) {
		sc := conversationScope(moduleTestScope(t, map[string]string{"main.dang": testModuleMain}), conversationBound)
		rep, got, _, _ := eval(t, sc, "ws = ws.withNewFile(path: \"a\", contents: greet(\"x\"))\nchat = chat.withPrompt(prompt: \"hi\")")
		require.Empty(t, got)
		require.Contains(t, rep.Text, core.DangChatAloneMessage)
	})
}

// DangCheck reports the writes DangEval refuses, where they are.
func TestDangCheckConversationWrites(t *testing.T) {
	t.Parallel()
	sc := conversationScope(workspaceTestScope(t), conversationBound)

	out := checkTest(t, sc, "me = null", "")
	require.Contains(t, out, "<check>:1:1: error: cannot assign to `me`: it is the conversation's agent, bound read-only")

	out = checkTest(t, sc, "ws = ws.withNewFile(path: \"a\", contents: \"b\")\nchat = chat.withPrompt(prompt: \"hi\")", "")
	require.Contains(t, out, "<check>:2:1: error: cannot assign to `chat` and `ws` in one snippet: ")

	out = checkTest(t, sc, `chat = box.install(llm: chat, ref: "r")`, "")
	require.NotContains(t, out, "error")
	require.Contains(t, checkTest(t, sc, "chat", ""), "chat: Dagger.LLM!\n")
	require.Contains(t, checkTest(t, sc, "me", ""), "me: Dagger.Agent!\n")

	t.Run("at a position", func(t *testing.T) {
		sc := conversationScope(moduleTestScope(t, map[string]string{"main.dang": testModuleMain}), conversationBound)
		sc.file, sc.pos = "mod/main.dang", &analysis.Pos{Line: 1, Column: 1}
		require.Contains(t, checkTest(t, sc, "me = null", ""), "error: cannot assign to `me`")
	})

	t.Run("a module's own files", func(t *testing.T) {
		sc := conversationScope(moduleTestScope(t, map[string]string{"main.dang": testModuleMain + "\nme = null\n"}), conversationBound)
		require.Contains(t, checkTest(t, sc, "", ""), "mod/main.dang:")
		require.Contains(t, checkTest(t, sc, "", ""), "cannot assign to `me`")
	})
}

// A DangEval call is scheduled as a continuation when its snippet may
// reassign `chat`, by its syntax.
func TestDangReassigned(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"chat"}, dangTools{}.Reassigned(`chat = editor.install(llm: chat, ref: "x")`))
	require.Equal(t, []string{"chat", "ws"}, dangTools{}.Reassigned("ws = ws\nchat = chat"))
	require.Empty(t, dangTools{}.Reassigned(`editor.install(llm: chat, ref: "x")`))
	require.Empty(t, dangTools{}.Reassigned(`chat = `))
	// Source only an older Dang parses, for a module scope of that version.
	require.Equal(t, []string{"chat"}, dangTools{}.Reassigned("let p = \"\"\"Fix \\d+\nmore\n\"\"\"\nchat = chat"))
}

func TestModuleScopeNames(t *testing.T) {
	t.Parallel()
	sc := moduleTestScope(t, map[string]string{
		"main.dang": testModuleMain,
		"ws.dang":   "let ws = \"mine\"\n",
		"llm.dang":  "let ask(chat: LLM!): LLM! { chat }\n",
	})
	names := moduleScopeNames(context.Background(), sc.fsys, sc.dir, "")
	require.True(t, names.declared["ws"])
	require.True(t, names.declared["stableOrder"])
	require.False(t, names.declared["box"])
	// A file or the call's source mentioning a name, as a whole word.
	require.True(t, names.mentions("chat"))
	require.False(t, names.mentions("me"))
	require.True(t, scopeNames{source: "chat = x"}.mentions("chat"))
	require.True(t, scopeNames{source: "f(llm: chat)"}.mentions("chat"))
	require.False(t, scopeNames{source: "chatter + mychat + chat_x"}.mentions("chat"))
	require.False(t, scopeNames{}.mentions("chat"))
}

func TestParseDangAt(t *testing.T) {
	t.Parallel()
	target, pos, err := parseDangAt("mod/main.dang:10:3")
	require.NoError(t, err)
	require.Equal(t, "mod/main.dang", target)
	require.Equal(t, &analysis.Pos{Line: 10, Column: 3}, pos)

	target, pos, err = parseDangAt("mod/main.dang:7")
	require.NoError(t, err)
	require.Equal(t, "mod/main.dang", target)
	require.Equal(t, &analysis.Pos{Line: 7, Column: 1}, pos)

	target, pos, err = parseDangAt("my-module")
	require.NoError(t, err)
	require.Equal(t, "my-module", target)
	require.Nil(t, pos)

	_, _, err = parseDangAt("main.dang:0:1")
	require.Error(t, err)
}

func TestDangDisplayPaths(t *testing.T) {
	t.Parallel()
	sc := moduleTestScope(t, map[string]string{"main.dang": "let n: Int! = \"nope\"\n"})
	sc.cwd = "mod"
	out := checkTest(t, sc, "", "")
	require.True(t, strings.HasPrefix(out, "main.dang:1:"), "relative to the cwd, as `at` resolves it: %s", out)
	sc.cwd = "elsewhere"
	out = checkTest(t, sc, "", "")
	require.True(t, strings.HasPrefix(out, "/mod/main.dang:1:"), "anchored at the root outside the cwd: %s", out)
}

func TestRelPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ base, target, want string }{
		{".", ".", "."},
		{".", "src", "src"},
		{"mod", "mod", "."},
		{"mod", "mod/src", "src"},
	} {
		got, ok := relPath(tc.base, tc.target)
		require.True(t, ok, tc)
		require.Equal(t, tc.want, got, tc)
	}
	_, ok := relPath("mod", "other")
	require.False(t, ok)
}

// An installed module's constructor is a Query field like any other, so the
// default scope names it as a global, by its field name, and search finds
// its API: the module named go is `go`, its main object `Go`.
func TestDangInstalledModuleGlobals(t *testing.T) {
	t.Parallel()
	sdl := strings.Replace(dangToolsTestSDL, "type Query {", `type Query {
  """
  Go toolchain helpers.
  """
  go: Go!`, 1) + `
"""
Builds and tests Go packages.
"""
type Go implements Node {
  id: ID!
  """
  Run the tests of the selected packages.
  """
  test(packages: [String!]): String!
}
`
	schema, err := dang.SchemaFromSDL(sdl, "test.graphqls")
	require.NoError(t, err)
	sc := workspaceTestScope(t)
	sc.schema = schema

	out := checkTest(t, sc, `go.test(packages: ["./..."])`, "")
	require.True(t, strings.HasPrefix(out, "String!\n"), out)
	out = checkTest(t, sc, `Go`, "")
	require.True(t, strings.HasPrefix(out, "type Go\n  Builds and tests Go packages.\n"), out)
	out = checkTest(t, sc, "", "go")
	require.Contains(t, out, "  type Go — Builds and tests Go packages.\n")
	out = checkTest(t, sc, "", "selected packages")
	require.Contains(t, out, "Go.test(packages: [String!]): String! — Run the tests of the selected packages.")
}

// A module scope with no .dang files, such as a module written with another
// SDK, says so as a diagnostic instead of failing the call.
func TestDangCheckModuleWithoutDangFiles(t *testing.T) {
	t.Parallel()
	sc := moduleTestScope(t, map[string]string{"main.go": "package main\n"})
	sc.label = "module go (dagger.io/go)"
	sc.sdk = "go"
	out := checkTest(t, sc, "", "")
	require.Equal(t, "error: module go (dagger.io/go) is written with the go SDK, not Dang: it has no .dang files to diagnose; pass source to check Dang against its API\n"+
		"1 error · module go (dagger.io/go) · 0 files · nothing ran\n", out)

	// Snippets still check against its API.
	out = checkTest(t, sc, `container.withExec(args: ["true"]).stdout`, "")
	require.Contains(t, out, "ok · module go (dagger.io/go) · 0 files · nothing ran")

	sc.sdk = "dang"
	out = checkTest(t, sc, "", "")
	require.True(t, strings.HasPrefix(out, "error: no .dang files in mod\n"), out)
}

// An `at` naming nothing to scope to is reported as the call's result, with
// what is installed, not as a tool error.
func TestDangAtReport(t *testing.T) {
	t.Parallel()
	scope := &core.DangScope{LoadFailures: []core.ModuleLoadFailure{{Name: "broken", Message: "exit code 1"}}}
	loc := dangLocation{installed: "broken", source: "github.com/acme/broken"}
	require.Equal(t, "module broken (github.com/acme/broken) failed to load: exit code 1", unloadedModuleMessage(scope, loc))
	loc = dangLocation{installed: "other", source: "github.com/acme/other"}
	require.Equal(t, "module other (github.com/acme/other) is installed, but didn't load", unloadedModuleMessage(scope, loc))
	scope.InstalledErr = errors.New("bad dagger.toml")
	require.Contains(t, unloadedModuleMessage(scope, loc), "the workspace's modules didn't load: bad dagger.toml")

	require.Equal(t, " (installed: broken (failed to load))", installedNames(scope))
	require.Empty(t, installedNames(&core.DangScope{}))

	report, ok := dangAtReport(fmt.Errorf("wrapped: %w", &dangAtError{at: "nope", msg: "nope is neither a module installed in the workspace nor one of its directories"}))
	require.True(t, ok)
	require.Equal(t, "error: at \"nope\": nope is neither a module installed in the workspace nor one of its directories\n1 error · nothing ran\n", report)
	_, ok = dangAtReport(errors.New("boom"))
	require.False(t, ok)
	_, ok = dangAtReport(nil)
	require.False(t, ok)
}

// A name the default scope doesn't find may be an installed module that
// failed to load: a report that couldn't find one says which did.
func TestDangUnloadedNote(t *testing.T) {
	t.Parallel()
	require.Empty(t, unloadedNote(&core.DangScope{}))
	scope := &core.DangScope{LoadFailures: []core.ModuleLoadFailure{
		{Name: "go", Message: "exit code 1\nlong build log"},
		{Name: "broken", Message: "no dagger-module.toml"},
	}}
	note := unloadedNote(scope)
	require.Equal(t, "note: installed modules that failed to load aren't in scope: broken (no dagger-module.toml), go (exit code 1)", note)

	sc := workspaceTestScope(t)
	sc.unloaded = note
	out := checkTest(t, sc, "go", "")
	require.True(t, strings.HasSuffix(withUnloadedNote(sc, out), "1 error · workspace scope · nothing ran\n"+note+"\n"), out)
	out = checkTest(t, sc, `true`, "")
	require.Equal(t, out, withUnloadedNote(sc, out), "only a report that couldn't find a name")

	scope = &core.DangScope{InstalledErr: errors.New("bad dagger.toml")}
	require.Equal(t, "note: the workspace's installed modules aren't in scope: bad dagger.toml", unloadedNote(scope))
}

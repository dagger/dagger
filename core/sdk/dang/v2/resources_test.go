package dangv2

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/introspection"
)

const resourcesEntrypointContract = `
  pub types(workspace: Workspace!): [TypeDef!]! { [] }
  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! { ("{}" :: JSON!) }
`

func resourcesEntrypoint(members string) string {
	return "type Entrypoint implements ModuleEntrypoint {\n" + members + resourcesEntrypointContract + "}\n"
}

func writeDangFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return dir
}

func TestDeclaresEntrypointResources(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		files    map[string]string
		declared bool
	}{
		{
			name:     "function member",
			files:    map[string]string{"main.dang": resourcesEntrypoint(`  pub resources: [String!]! { ["../lib"] }`)},
			declared: true,
		},
		{
			name:     "function member with parentheses",
			files:    map[string]string{"main.dang": resourcesEntrypoint(`  pub resources(): [String!]! { ["../lib"] }`)},
			declared: true,
		},
		{
			name: "comment and string mentions",
			files: map[string]string{"main.dang": "# resources are listed elsewhere\n" +
				resourcesEntrypoint(`  let label: String! = "resources"`)},
		},
		{
			name:  "private member",
			files: map[string]string{"main.dang": resourcesEntrypoint(`  let resources: [String!]! { ["../lib"] }`)},
		},
		{
			name: "member of a type that is not the entrypoint",
			files: map[string]string{"main.dang": resourcesEntrypoint("") +
				"type Other {\n  pub resources: [String!]! { [\"../lib\"] }\n}\n"},
		},
		{
			name: "member in a sub-directory file",
			files: map[string]string{
				"main.dang":     resourcesEntrypoint(""),
				"sub/more.dang": resourcesEntrypoint(`  pub resources: [String!]! { ["../lib"] }`),
			},
		},
		{
			name:  "no mention",
			files: map[string]string{"main.dang": resourcesEntrypoint("")},
		},
		{
			name: "syntax error beside a declaration",
			files: map[string]string{
				"main.dang":   resourcesEntrypoint(`  pub resources: [String!]! { ["../lib"] }`),
				"broken.dang": "# resources\nlet broken = \"unterminated\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared, err := DeclaresEntrypointResources(writeDangFiles(t, tc.files))
			require.NoError(t, err)
			require.Equal(t, tc.declared, declared)
		})
	}
}

// countingClient counts the requests that reach the client the engine installs.
type countingClient struct {
	requests int
}

func (c *countingClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.requests++
	return refusingClient{}.MakeRequest(ctx, req, resp)
}

func resourcesTestSchema() *introspection.Schema {
	nonNull := func(ref *introspection.TypeRef) *introspection.TypeRef {
		return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: ref}
	}
	object := func(name string) *introspection.TypeRef {
		return nonNull(&introspection.TypeRef{Kind: introspection.TypeKindObject, Name: name})
	}
	str := nonNull(&introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"})
	arg := func(name string) introspection.InputValue {
		return introspection.InputValue{Name: name, TypeRef: str}
	}
	field := func(name string, typ *introspection.TypeRef, args ...introspection.InputValue) *introspection.Field {
		return &introspection.Field{Name: name, TypeRef: typ, Args: args}
	}
	objectType := func(name string, fields ...*introspection.Field) *introspection.Type {
		return &introspection.Type{Kind: introspection.TypeKindObject, Name: name, Fields: fields}
	}
	scalarType := func(name string) *introspection.Type {
		return &introspection.Type{Kind: introspection.TypeKindScalar, Name: name}
	}
	schema := &introspection.Schema{Types: introspection.Types{
		scalarType("String"),
		scalarType("Boolean"),
		scalarType("ID"),
		scalarType("JSON"),
		objectType("Query",
			field("container", object("Container")),
			field("currentModule", object("CurrentModule")),
			field("moduleSource", object("ModuleSource"), arg("refString")),
		),
		objectType("Container",
			field("from", object("Container"), arg("address")),
			field("file", object("File"), arg("path")),
		),
		objectType("File", field("contents", str)),
		objectType("CurrentModule", field("source", object("Directory"))),
		objectType("Directory", field("entries", nonNull(&introspection.TypeRef{Kind: introspection.TypeKindList, OfType: str}))),
		objectType("ModuleSource", field("digest", str)),
		objectType("Workspace"),
		objectType("TypeDef"),
	}}
	schema.QueryType.Name = "Query"
	return schema
}

// The engine caches resources on the entrypoint program alone, so the
// evaluation must not reach the API: building a query is lazy, and the first
// scalar read is refused.
func TestEntrypointResourcesRefusesTheAPI(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		members  string
		top      string
		patterns []string
		requests int
		err      string
	}{
		{
			name:     "literal list",
			members:  `  pub resources: [String!]! { ["../lib", "!../lib/testdata"] }`,
			patterns: []string{"../lib", "!../lib/testdata"},
		},
		{
			name: "lazy member that builds a query",
			members: `  pub resources: [String!]! { ["../lib"] }
  let dispatch: File! { container.from("alpine").file("/etc/os-release") }`,
			patterns: []string{"../lib"},
		},
		{
			name: "eager member that builds a query",
			members: `  pub resources: [String!]! { ["../lib"] }
  let base = container.from("alpine")`,
			patterns: []string{"../lib"},
		},
		{
			name:     "member reads a file",
			members:  `  pub resources: [String!]! { [container.from("alpine").file("/etc/os-release").contents] }`,
			requests: 1,
			err:      ErrEntrypointResourcesCallsAPI.Error(),
		},
		{
			name:     "member reads the current module",
			members:  `  pub resources: [String!]! { currentModule.source.entries }`,
			requests: 1,
			err:      ErrEntrypointResourcesCallsAPI.Error(),
		},
		{
			name:     "member reads another module source",
			members:  `  pub resources: [String!]! { [moduleSource("github.com/dagger/dagger").digest] }`,
			requests: 1,
			err:      ErrEntrypointResourcesCallsAPI.Error(),
		},
		{
			name:     "top-level read",
			members:  `  pub resources: [String!]! { ["../lib"] }`,
			top:      `let release = container.from("alpine").file("/etc/os-release").contents` + "\n",
			requests: 1,
			err:      ErrEntrypointResourcesCallsAPI.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeDangFiles(t, map[string]string{"main.dang": tc.top + resourcesEntrypoint(tc.members)})
			client := &countingClient{}
			patterns, err := evalEntrypointResources(context.Background(), resourcesTestSchema(), client, dir, dangModule{name: "m", originalName: "m"})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.patterns, patterns)
			}
			require.Equal(t, tc.requests, client.requests)
		})
	}
}

func TestEntrypointResourcesShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		members string
		err     string
	}{
		{
			name:    "value field",
			members: `  pub resources: [String!]! = ["../lib"]`,
			err:     "resources must be a function",
		},
		{
			name:    "required argument",
			members: `  pub resources(base: String!): [String!]! { [base] }`,
			err:     `resources must not require an argument: the engine passes none, but "base" is required`,
		},
		{
			name:    "raised error",
			members: `  pub resources: [String!]! { raise "no resources here" }`,
			err:     "no resources here",
		},
		{
			name:    "list of numbers",
			members: `  pub resources: [Int!]! { [1, 2] }`,
			err:     "resources element 0 is",
		},
		{
			name:    "string",
			members: `  pub resources: String! { "../lib" }`,
			err:     "resources returned",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeDangFiles(t, map[string]string{"main.dang": resourcesEntrypoint(tc.members)})
			declared, err := DeclaresEntrypointResources(dir)
			require.True(t, declared)
			if err == nil {
				_, err = evalEntrypointResources(context.Background(), resourcesTestSchema(), refusingClient{}, dir, dangModule{name: "m", originalName: "m"})
			}
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestEntrypointResourcesOptionalArgument(t *testing.T) {
	t.Parallel()

	dir := writeDangFiles(t, map[string]string{"main.dang": resourcesEntrypoint(`  pub resources(extra: String): [String!]! { ["../lib"] }`)})
	declared, err := DeclaresEntrypointResources(dir)
	require.NoError(t, err)
	require.True(t, declared)
	patterns, err := evalEntrypointResources(context.Background(), resourcesTestSchema(), refusingClient{}, dir, dangModule{name: "m", originalName: "m"})
	require.NoError(t, err)
	require.Equal(t, []string{"../lib"}, patterns)
}

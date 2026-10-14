package dangv2

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"
)

const withoutSourcesMethods = `
  pub types(workspace: Workspace!): [TypeDef!]! { [] }
  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! { ("{}" :: JSON!) }
`

const sourcesMember = `
  pub sources(workspace: Workspace!): Directory! { workspace.directory(".") }
`

// goSDKMethods is the shape the Go SDK generates for a module that declares
// sources, so the same entrypoint conforms to both contracts and its types
// can tell an engine without sources by the workspace it receives.
const goSDKMethods = `
  pub types(workspace: Workspace): [TypeDef!]! {
    if (workspace != null) {
      raise "use a newer engine"
    }
    []
  }
  pub call(
    workspace: Workspace,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
    callDigest: String,
  ): JSON! { ("{}" :: JSON!) }
`

const workspacelessMethods = `
  pub types: [TypeDef!]! { [] }
  pub call(
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! { ("{}" :: JSON!) }
`

func entrypointSource(members ...string) string {
	src := "type Entrypoint implements ModuleEntrypoint {\n"
	for _, member := range members {
		src += member
	}
	return src + "}\n"
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

func TestDeclaresEntrypointSources(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		files    map[string]string
		declared bool
	}{
		{
			name:     "function member",
			files:    map[string]string{"main.dang": entrypointSource(sourcesMember, goSDKMethods)},
			declared: true,
		},
		{
			name:     "member in another top-level file",
			files:    map[string]string{"a.dang": "let x = 1\n", "main.dang": entrypointSource(sourcesMember, goSDKMethods)},
			declared: true,
		},
		{
			name: "comment and string mentions",
			files: map[string]string{"main.dang": "# sources are elsewhere\n" +
				entrypointSource(`  let label: String! = "sources"`+"\n", withoutSourcesMethods)},
		},
		{
			name:  "private member",
			files: map[string]string{"main.dang": entrypointSource("  let sources: String! = \"x\"\n", withoutSourcesMethods)},
		},
		{
			name: "member of a type that is not the entrypoint",
			files: map[string]string{"main.dang": entrypointSource(withoutSourcesMethods) +
				"type Other {\n  pub sources: String! = \"x\"\n}\n"},
		},
		{
			name: "member in a sub-directory file",
			files: map[string]string{
				"main.dang":     entrypointSource(withoutSourcesMethods),
				"sub/more.dang": entrypointSource(sourcesMember, goSDKMethods),
			},
		},
		{
			name:  "no mention",
			files: map[string]string{"main.dang": entrypointSource(withoutSourcesMethods)},
		},
		{
			name: "syntax error",
			files: map[string]string{
				"main.dang": entrypointSource(sourcesMember, goSDKMethods) + "let broken = \"unterminated\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared, err := DeclaresEntrypointSources(writeDangFiles(t, tc.files))
			require.NoError(t, err)
			require.Equal(t, tc.declared, declared)
		})
	}
}

// failingClient fails every request because type checking the interface sends
// none.
type failingClient struct{}

func (failingClient) MakeRequest(context.Context, *graphql.Request, *graphql.Response) error {
	return errors.New("unexpected API request")
}

func contractTestSchema() *introspection.Schema {
	nonNull := func(ref *introspection.TypeRef) *introspection.TypeRef {
		return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: ref}
	}
	object := func(name string) *introspection.TypeRef {
		return nonNull(&introspection.TypeRef{Kind: introspection.TypeKindObject, Name: name})
	}
	str := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}
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
		objectType("Query", &introspection.Field{Name: "directory", TypeRef: object("Directory")}),
		objectType("Directory"),
		objectType("Workspace", &introspection.Field{
			Name:    "directory",
			TypeRef: object("Directory"),
			Args:    []introspection.InputValue{{Name: "path", TypeRef: nonNull(str)}},
		}),
		objectType("TypeDef"),
	}}
	schema.QueryType.Name = "Query"
	return schema
}

func runContract(t *testing.T, source string, sources bool) error {
	t.Helper()
	ctx := dang.ContextWithImportConfigs(context.Background(), dang.ImportConfig{
		Name:       "Dagger",
		Client:     failingClient{},
		Schema:     contractTestSchema(),
		AutoImport: true,
	})
	_, err := runEntrypointDir(ctx, writeDangFiles(t, map[string]string{"main.dang": source}), sources)
	return err
}

// The engine installs the interface itself, so these shapes pin what an
// entrypoint may declare under each contract.
func TestEntrypointContractConformance(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		source  string
		sources bool
		err     string
	}{
		{
			name:   "without sources, required workspace",
			source: entrypointSource(withoutSourcesMethods),
		},
		{
			name:   "Go SDK sources shape on the contract without sources",
			source: entrypointSource(sourcesMember, goSDKMethods),
		},
		{
			name:    "Go SDK sources shape",
			source:  entrypointSource(sourcesMember, goSDKMethods),
			sources: true,
		},
		{
			name:    "sources without a workspace argument",
			source:  entrypointSource(sourcesMember, workspacelessMethods),
			sources: true,
		},
		{
			name:    "sources with a required workspace in types and call",
			source:  entrypointSource(sourcesMember, withoutSourcesMethods),
			sources: true,
			err:     `field "types", argument "workspace": additional arguments not in interface must be optional`,
		},
		{
			name:    "sources as a value field",
			source:  entrypointSource("  pub sources: Directory! = directory\n", workspacelessMethods),
			sources: true,
			err:     `field "sources"`,
		},
		{
			name:    "sources returning a string",
			source:  entrypointSource("  pub sources(workspace: Workspace!): String! { \"\" }\n", workspacelessMethods),
			sources: true,
			err:     `field "sources": return type`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := runContract(t, tc.source, tc.sources)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.err)
		})
	}
}

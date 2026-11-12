package dangv2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
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
			// The entrypoint runs from a copy of its Dang files alone, so it
			// declares sources as that copy parses.
			name: "a dang.toml the run does not read",
			files: map[string]string{
				"dang.toml": "dang = \n",
				"main.dang": entrypointSource(sourcesMember, goSDKMethods),
			},
			declared: true,
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
			declared, err := DeclaresEntrypointSources(t.Context(), writeDangFiles(t, tc.files), "")
			require.NoError(t, err)
			require.Equal(t, tc.declared, declared)
		})
	}
}

// Every module source load and every entrypoint run asks whether the
// entrypoint declares sources, each from its own mount of the entrypoint. Once
// one has parsed the entrypoint, asking again about the same content costs a
// copy of its syntax, not another parse. Parsing allocates many times what
// the copy does, so this compares the bytes each allocates.
func TestDeclaresEntrypointSourcesParsesOncePerContent(t *testing.T) {
	members := []string{sourcesMember, goSDKMethods}
	for i := range 100 {
		members = append(members, fmt.Sprintf("  pub greeting%d(name: String!): String! { \"hello %d, \" + name }\n", i, i))
	}
	// Content of its own, so no earlier test has parsed it.
	source := fmt.Sprintf("# %s\n", t.Name()) + entrypointSource(members...)

	allocated := func(f func()) uint64 {
		var before, after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)
		f()
		goruntime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	declares := func() {
		declared, err := DeclaresEntrypointSources(t.Context(), writeDangFiles(t, map[string]string{"main.dang": source}), "")
		require.NoError(t, err)
		require.True(t, declared)
	}

	parse := allocated(func() {
		_, err := dang.Parse("main.dang", []byte(source))
		require.NoError(t, err)
	})
	declares()
	again := allocated(declares)
	require.Less(t, again, parse/4, "asking again parsed the entrypoint again: %d bytes allocated, against %d for a parse", again, parse)
}

// The entrypoint's copy has no dang.toml, so the Dang version its module
// source records decides how the run parses it. The check must parse it the
// same way, or it can find no sources member where the run has one. Postfix
// rescue is syntax from Dang 2.1; in 2.0 it does not parse.
func TestDeclaresEntrypointSourcesAtTheContextVersion(t *testing.T) {
	t.Parallel()
	dir := writeDangFiles(t, map[string]string{
		"main.dang": entrypointSource(sourcesMember, goSDKMethods) + "let caught: Int! = 1 rescue { e: Error => 2 }\n",
	})
	ctx := dang.ContextWithImportConfigs(t.Context(), dang.ImportConfig{
		Name:       "Dagger",
		Client:     failingClient{},
		Schema:     contractTestSchema(),
		AutoImport: true,
	})

	declared, err := DeclaresEntrypointSources(ctx, dir, "2.1")
	require.NoError(t, err)
	require.True(t, declared)
	_, err = runEntrypointDir(ctx, dir, "2.1", true)
	require.NoError(t, err)

	declared, err = DeclaresEntrypointSources(ctx, dir, "2.0")
	require.NoError(t, err)
	require.False(t, declared)
	_, err = runEntrypointDir(ctx, dir, "2.0", true)
	require.ErrorContains(t, err, "failed to parse file", "the run parses the entrypoint as the check does")
}

const prebuildMember = `
  pub prebuild: File! { directory.withNewFile("dispatch", "binary").file("dispatch") }
`

// The prebuild check reads the entrypoint as the run reads it: through the
// syntax cache, so an entrypoint the sources check has parsed isn't parsed
// again.
func TestDeclaresEntrypointPrebuildParsesOncePerContent(t *testing.T) {
	members := []string{sourcesMember, goSDKMethods, prebuildMember}
	for i := range 100 {
		members = append(members, fmt.Sprintf("  pub greeting%d(name: String!): String! { \"hello %d, \" + name }\n", i, i))
	}
	// Content of its own, so no earlier test has parsed it.
	source := fmt.Sprintf("# %s\n", t.Name()) + entrypointSource(members...)
	dir := writeDangFiles(t, map[string]string{"main.dang": source})

	allocated := func(f func()) uint64 {
		var before, after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)
		f()
		goruntime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	parse := allocated(func() {
		_, err := dang.Parse("main.dang", []byte(source))
		require.NoError(t, err)
	})
	declared, err := DeclaresEntrypointSources(t.Context(), dir, "")
	require.NoError(t, err)
	require.True(t, declared)
	again := allocated(func() {
		// At the entrypoint's version, as the run checks it (see
		// contextWithEntrypointVersion).
		ctx := dang.ContextWithVersion(t.Context(), DefaultEntrypointDangVersion)
		declared, err := declaresEntrypointMember(ctx, dir, EntrypointPrebuildMember)
		require.NoError(t, err)
		require.True(t, declared)
	})
	require.Less(t, again, parse/4, "the prebuild check parsed the entrypoint again: %d bytes allocated, against %d for a parse", again, parse)
}

// The prebuild check parses at the Dang version on the context, as the run
// and the sources check do, so the run never builds from a member the check
// misses, or the other way round. Postfix rescue is syntax from Dang 2.1; in
// 2.0 it does not parse.
func TestDeclaresEntrypointPrebuildAtTheContextVersion(t *testing.T) {
	t.Parallel()
	dir := writeDangFiles(t, map[string]string{
		"main.dang": entrypointSource(sourcesMember, goSDKMethods, prebuildMember) + "let caught: Int! = 1 rescue { e: Error => 2 }\n",
	})
	declared, err := declaresEntrypointMember(dang.ContextWithVersion(t.Context(), dang.Version2_1), dir, EntrypointPrebuildMember)
	require.NoError(t, err)
	require.True(t, declared)
	declared, err = declaresEntrypointMember(dang.ContextWithVersion(t.Context(), dang.Version2_0), dir, EntrypointPrebuildMember)
	require.NoError(t, err)
	require.False(t, declared)
}

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
	_, err := runEntrypointDir(ctx, writeDangFiles(t, map[string]string{"main.dang": source}), "", sources)
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
			name: "sources with parentheses on types",
			source: entrypointSource(sourcesMember, strings.Replace(workspacelessMethods,
				"pub types: [TypeDef!]!", "pub types(): [TypeDef!]!", 1)),
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

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
)

// renamedCoreNames are the core names renamed to the GraphQL casing
// convention at engine.IdentifierCasingVersion, by type: the new name of a
// field or an argument (field.arg), and its old spelling.
var renamedCoreNames = []struct {
	typeName, cased, legacy string
}{
	{"Cloud", "traceUrl", "traceURL"},
	{"Container", "from.insecureSkipTlsVerify", "from.insecureSkipTLSVerify"},
	{"Container", "publish.insecureSkipTlsVerify", "publish.insecureSkipTLSVerify"},
	{"Container", "withGpu", "withGPU"},
	{"Container", "experimentalWithGpu", "experimentalWithGPU"},
	{"Container", "experimentalWithAllGpus", "experimentalWithAllGPUs"},
	{"File", "asJson", "asJSON"},
	{"GeneratedCode", "withVcsGeneratedPaths", "withVCSGeneratedPaths"},
	{"GeneratedCode", "withVcsIgnoredPaths", "withVCSIgnoredPaths"},
	{"GitBundle", "prerequisiteShas", "prerequisiteSHAs"},
	{"GitCommit", "treeSha", "treeSHA"},
	{"GitPushResult", "previousSha", "previousSHA"},
	{"GitRef", "commitSha", "commitSHA"},
	{"GitRef", "push.expectedRemoteSha", "push.expectedRemoteSHA"},
	{"LLM", "withMcpServer", "withMCPServer"},
	{"Module", "introspectionSchemaJson", "introspectionSchemaJSON"},
	{"ModuleSource", "withSdk", "withSDK"},
	{"ModuleSource", "introspectionSchemaJson", "introspectionSchemaJSON"},
	{"ModuleSource", "clientSchemaIntrospectionJson", "clientSchemaIntrospectionJSON"},
	{"ModuleSource", "htmlUrl", "htmlURL"},
	{"ModuleSource", "htmlRepoUrl", "htmlRepoURL"},
	{"Workspace", "withSdk", "withSDK"},
	{"Workspace", "withoutSdk", "withoutSDK"},
}

// hasCoreSchemaName reports whether schema has a field or an argument
// (field.arg) of typeName, not counting internal arguments.
func hasCoreSchemaName(schema *ast.Schema, typeName, name string) bool {
	def := schema.Types[typeName]
	if def == nil {
		return false
	}
	fieldName, argName, isArg := strings.Cut(name, ".")
	field := def.Fields.ForName(fieldName)
	if field == nil {
		return false
	}
	if isArg {
		arg := field.Arguments.ForName(argName)
		return arg != nil && arg.Directives.ForName("internal") == nil
	}
	return true
}

// Callers declaring a version before engine.IdentifierCasingVersion see only
// the old names; from it on, including dev builds and the v1.0.0 release,
// only the new ones. Versions are declared client versions, mapped to views
// as the engine does.
func TestCoreRenamedNamesViews(t *testing.T) {
	for _, tc := range []struct {
		version string
		cased   bool
	}{
		{version: "v1.0.0-0"},
		{version: "v1.0.0-beta.17"},
		{version: "v1.0.0-beta.21"},
		{version: engine.IdentifierCasingVersion, cased: true},
		{version: "v1.0.0-dev-0123456789ab", cased: true},
		{version: "v1.0.0", cased: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(tc.version)))
			schema := dag.SchemaForView(dag.View)
			for _, name := range renamedCoreNames {
				require.Equal(t, tc.cased, hasCoreSchemaName(schema, name.typeName, name.cased), "%s.%s", name.typeName, name.cased)
				require.Equal(t, !tc.cased, hasCoreSchemaName(schema, name.typeName, name.legacy), "%s.%s", name.typeName, name.legacy)
			}
		})
	}
}

// v0 views keep the old names.
func TestCoreRenamedNamesPreV1(t *testing.T) {
	_, dag := newNestingTestServer(t, "v0.21.0")
	schema := dag.SchemaForView(dag.View)
	for _, name := range renamedCoreNames {
		require.False(t, hasCoreSchemaName(schema, name.typeName, name.cased), "%s.%s", name.typeName, name.cased)
	}
	for _, name := range []string{"traceURL", "asJSON", "experimentalWithGPU", "experimentalWithAllGPUs"} {
		found := false
		for _, typeName := range []string{"Cloud", "File", "Container"} {
			found = found || hasCoreSchemaName(schema, typeName, name)
		}
		require.True(t, found, name)
	}
}

// legacyCoreLoaderField reports a legacy load<Type>FromID root field. They
// are kept as they are, for clients of old engines, and replaced by
// node(id:): they're no part of the API being renamed (and only exist in
// views before v0.22.0).
func legacyCoreLoaderField(typeName, fieldName string) bool {
	return typeName == "Query" && strings.HasPrefix(fieldName, "load") && strings.HasSuffix(fieldName, "FromID")
}

// casingExceptions are core names in the latest views that don't follow the
// GraphQL casing convention, as "<kind> <owner>.<name>" (kind is type, field,
// input field, argument or enum value): none. Keep it empty; a name that
// can't be renamed goes here with the reason.
var casingExceptions = map[string]bool{}

// From engine.IdentifierCasingVersion on, every core type name is PascalCase
// with uppercase acronyms (HTTPClient), and every field, argument and input
// field name camelCase with capitalized acronyms (asJson). Internal names
// (_-prefixed types and fields, their arguments, and internal arguments) are
// left out.
func TestCoreSchemaGraphQLCasing(t *testing.T) {
	for _, version := range []string{engine.IdentifierCasingVersion, "v1.0.0"} {
		t.Run(version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(version)))
			schema := dag.SchemaForView(dag.View)
			check := func(kind, owner, name string, casing naming.Casing, style naming.AcronymStyle) {
				t.Helper()
				if casingExceptions[kind+" "+owner+"."+name] {
					return
				}
				id, err := naming.Parse(name)
				require.NoError(t, err, name)
				if got := id.Format(casing, style); got != name {
					t.Errorf("%s %s.%s is not conventional: want %s", kind, owner, name, got)
				}
			}
			for typeName, def := range schema.Types {
				if strings.HasPrefix(typeName, "_") {
					continue
				}
				check("type", "", typeName, naming.Pascal, naming.Uppercase)
				for _, field := range def.Fields {
					if strings.HasPrefix(field.Name, "_") || legacyCoreLoaderField(typeName, field.Name) {
						continue
					}
					kind := "field"
					if def.Kind == ast.InputObject {
						kind = "input field"
					}
					check(kind, typeName, field.Name, naming.Camel, naming.Capitalized)
					for _, arg := range field.Arguments {
						if arg.Directives.ForName("internal") != nil {
							continue
						}
						check("argument", typeName+"."+field.Name, arg.Name, naming.Camel, naming.Capitalized)
					}
				}
			}
		})
	}
}

// Calls to Container.from and publish, which have no view filter, are
// selected in view "" whatever the caller's view, so the old spelling of
// insecureSkipTlsVerify stays an internal argument there: a caller before
// the cut still gets it to the resolver.
func TestInsecureSkipTLSVerifySpellings(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	for _, tc := range []struct {
		view call.View
		arg  string
	}{
		{view: "", arg: "insecureSkipTlsVerify"},
		{view: call.View(engine.APIViewVersion("v1.0.0")), arg: "insecureSkipTlsVerify"},
		{view: call.View(preCutView), arg: "insecureSkipTLSVerify"},
	} {
		t.Run(string(tc.view)+"/"+tc.arg, func(t *testing.T) {
			var ctr dagql.ObjectResult[*core.Container]
			err := dag.Select(ctx, dag.Root(), &ctr,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: "from", View: tc.view, Args: []dagql.NamedInput{
					{Name: "address", Value: dagql.NewString("registry.example/image:tag")},
					{Name: "protocol", Value: dagql.Opt(core.RegistryProtocolHTTP)},
					{Name: tc.arg, Value: dagql.NewBoolean(true)},
				}},
			)
			// HTTP rejects the flag before anything is pulled.
			require.ErrorContains(t, err, tc.arg+" cannot be used with HTTP registry protocol")
		})
	}

	ctr, ok := dag.ObjectType("Container")
	require.True(t, ok)
	for _, fieldName := range []string{"from", "publish"} {
		field, ok := ctr.FieldSpec(fieldName, "")
		require.True(t, ok)
		legacy, ok := field.Args.Input("insecureSkipTLSVerify", "")
		require.True(t, ok, fieldName)
		require.True(t, legacy.Internal, fieldName)
		_, ok = field.Args.Input("insecureSkipTlsVerify", "")
		require.True(t, ok, fieldName)
		_, ok = field.Args.Input("insecureSkipTLSVerify", call.View(engine.APIViewVersion("v1.0.0")))
		require.False(t, ok, "clients past the cut can't send %s(insecureSkipTLSVerify:)", fieldName)
	}
}

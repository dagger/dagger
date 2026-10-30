package schema

import (
	"encoding/json"
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

// renamedEnumValues are the core enum members renamed at
// engine.IdentifierCasingVersion: the member from the cut on, and the one
// before it. FunctionCachePolicy's new members only exist from the cut on;
// the image enums' SCREAMING_SNAKE_CASE members exist in every view since
// v0.18.11, and their old members only before the cut.
var renamedEnumValues = []struct {
	typeName, cased, legacy string
	casedBeforeCut          bool
}{
	{"FunctionCachePolicy", "DEFAULT", "Default", false},
	{"FunctionCachePolicy", "PER_SESSION", "PerSession", false},
	{"FunctionCachePolicy", "NEVER", "Never", false},
	{"ImageLayerCompression", "GZIP", "Gzip", true},
	{"ImageLayerCompression", "ZSTD", "Zstd", true},
	{"ImageLayerCompression", "ESTARGZ", "EStarGZ", true},
	{"ImageLayerCompression", "UNCOMPRESSED", "Uncompressed", true},
	{"ImageMediaTypes", "OCI", "OCIMediaTypes", true},
	{"ImageMediaTypes", "DOCKER", "DockerMediaTypes", true},
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

func hasEnumValue(schema *ast.Schema, typeName, value string) bool {
	def := schema.Types[typeName]
	return def != nil && def.EnumValues.ForName(value) != nil
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
			for _, value := range renamedEnumValues {
				require.Equal(t, tc.cased || value.casedBeforeCut, hasEnumValue(schema, value.typeName, value.cased), "%s.%s", value.typeName, value.cased)
				require.Equal(t, !tc.cased, hasEnumValue(schema, value.typeName, value.legacy), "%s.%s", value.typeName, value.legacy)
			}
		})
	}
}

// v0 views keep the old names, and the image enums' old members.
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
	for _, value := range renamedEnumValues {
		require.True(t, hasEnumValue(schema, value.typeName, value.legacy), "%s.%s", value.typeName, value.legacy)
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
// with uppercase acronyms (HTTPClient), every field, argument and input
// field name camelCase with capitalized acronyms (asJson), and every enum
// member SCREAMING_SNAKE_CASE. Internal names (_-prefixed types and fields,
// their arguments, and internal arguments) are left out.
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
				for _, value := range def.EnumValues {
					check("enum value", typeName, value.Name, naming.ScreamingSnake, naming.Uppercase)
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

// Every spelling of an enum member decodes to the same value, the new
// spelling, which IDs record in every view. Old IDs, which recorded the old
// spelling, still decode.
func TestRenamedEnumValueSpellings(t *testing.T) {
	for _, value := range renamedEnumValues {
		var decoder dagql.InputDecoder
		switch value.typeName {
		case "FunctionCachePolicy":
			decoder = core.FunctionCachePolicyEnum
		case "ImageLayerCompression":
			decoder = core.ImageLayerCompressions
		case "ImageMediaTypes":
			decoder = core.ImageMediaTypesEnum
		}
		for _, spelling := range []string{value.legacy, value.cased} {
			decoded, err := decoder.DecodeInput(spelling)
			require.NoError(t, err, spelling)
			require.Equal(t, call.NewLiteralEnum(value.cased), decoded.ToLiteral(), spelling)
		}
	}
	for _, spelling := range []string{"Never", "NEVER"} {
		policy, err := core.FunctionCachePolicyEnum.Lookup(spelling)
		require.NoError(t, err)
		require.Equal(t, core.FunctionCachePolicyNever, policy)
	}
}

// Values recorded with the old spellings, like a persisted typedef's cache
// policy or a persisted image export's media types, decode to the members.
func TestRenamedEnumValuesUnmarshal(t *testing.T) {
	var policy core.FunctionCachePolicy
	require.NoError(t, json.Unmarshal([]byte(`"Never"`), &policy))
	require.Equal(t, core.FunctionCachePolicyNever, policy)
	var mediaTypes core.ImageMediaTypes
	require.NoError(t, json.Unmarshal([]byte(`"OCIMediaTypes"`), &mediaTypes))
	require.Equal(t, core.OCIMediaTypes, mediaTypes)
	var compression core.ImageLayerCompression
	require.NoError(t, json.Unmarshal([]byte(`"EStarGZ"`), &compression))
	require.Equal(t, core.CompressionEStarGZ, compression)
	require.NoError(t, json.Unmarshal([]byte(`""`), &compression))
	require.Empty(t, compression)
}

// Views before the cut list the enums exactly as they did before the
// values were renamed: the old spelling as the member, and the new one, where
// the view has it, as its alias.
func TestRenamedEnumPreCutSchema(t *testing.T) {
	for _, tc := range []struct {
		view     call.View
		typeName string
		want     []string
	}{
		{call.View(preCutView), "ImageLayerCompression", []string{"Gzip", "GZIP@Gzip", "Zstd", "ZSTD@Zstd", "EStarGZ", "ESTARGZ@EStarGZ", "Uncompressed", "UNCOMPRESSED@Uncompressed"}},
		{call.View(preCutView), "ImageMediaTypes", []string{"OCIMediaTypes", "OCI@OCIMediaTypes", "DockerMediaTypes", "DOCKER@DockerMediaTypes"}},
		{call.View(preCutView), "FunctionCachePolicy", []string{"Default", "PerSession", "Never"}},
		{"v0.18.0", "ImageMediaTypes", []string{"OCIMediaTypes", "DockerMediaTypes"}},
		{"v0.18.0", "FunctionCachePolicy", []string{"Default", "PerSession", "Never"}},
		{call.View(engine.APIViewVersion(engine.IdentifierCasingVersion)), "ImageLayerCompression", []string{"GZIP", "ZSTD", "ESTARGZ", "UNCOMPRESSED"}},
		{call.View(engine.APIViewVersion(engine.IdentifierCasingVersion)), "ImageMediaTypes", []string{"OCI", "DOCKER"}},
		{call.View(engine.APIViewVersion(engine.IdentifierCasingVersion)), "FunctionCachePolicy", []string{"DEFAULT", "PER_SESSION", "NEVER"}},
	} {
		t.Run(string(tc.view)+"/"+tc.typeName, func(t *testing.T) {
			_, dag := newNestingTestServer(t, tc.view)
			def := dag.SchemaForView(dag.View).Types[tc.typeName]
			require.NotNil(t, def)
			var got []string
			for _, value := range def.EnumValues {
				name := value.Name
				if directive := value.Directives.ForName("enumValue"); directive != nil {
					name += "@" + directive.Arguments.ForName("value").Value.Raw
				}
				got = append(got, name)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// Default values are spelled as the view's members: mediaTypes defaults to
// OCI from the cut on, and to OCIMediaTypes before it, including views
// before v0.18.11, which have no OCI member.
func TestRenamedEnumDefaults(t *testing.T) {
	for _, tc := range []struct {
		view call.View
		want string
	}{
		{view: "v0.18.0", want: "OCIMediaTypes"},
		{view: "v0.21.0", want: "OCIMediaTypes"},
		{view: call.View(preCutView), want: "OCIMediaTypes"},
		{view: call.View(engine.APIViewVersion(engine.IdentifierCasingVersion)), want: "OCI"},
		{view: call.View(engine.APIViewVersion("v1.0.0")), want: "OCI"},
	} {
		t.Run(string(tc.view), func(t *testing.T) {
			_, dag := newNestingTestServer(t, tc.view)
			schema := dag.SchemaForView(dag.View)
			arg := schema.Types["Container"].Fields.ForName("publish").Arguments.ForName("mediaTypes")
			require.NotNil(t, arg)
			require.Equal(t, tc.want, arg.DefaultValue.Raw)
			require.True(t, hasEnumValue(schema, "ImageMediaTypes", tc.want))
		})
	}
}

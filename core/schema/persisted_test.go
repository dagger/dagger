package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

const persistedSchemaTestSession = "persisted-schema-session"

// persistedSchemaTestEnv builds the actual core schema over a path-backed
// cache so fixtures exercise real installed classes and loaders.
type persistedSchemaTestEnv struct {
	dbPath string
}

func (env *persistedSchemaTestEnv) open(t *testing.T) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, env.dbPath, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason())
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "persisted-schema-client", SessionID: persistedSchemaTestSession})
	srv := &currentTypeDefsTestServer{}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	root := core.NewRoot(srv)
	srv.deps = core.NewSchemaBuilder(root, []core.Mod{base.CoreMod("")})
	dag, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag
	return core.ContextWithQuery(ctx, root), cache, dag
}

func (env *persistedSchemaTestEnv) restart(t *testing.T, ctx context.Context, cache *dagql.Cache) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	require.NoError(t, cache.ReleaseSession(ctx, persistedSchemaTestSession))
	require.NoError(t, cache.Close(ctx))
	return env.open(t)
}

// TestCoreSchemaObjectsHavePersistedFamilies reads every object type the
// actual core schema installs and checks it has a codec pair and a registered
// reference family. The exceptions are listed explicitly: they are not
// module-returnable values with an established retained route, and none of
// them is a silent type exclusion inside the codec.
func TestCoreSchemaObjectsHavePersistedFamilies(t *testing.T) {
	env := &persistedSchemaTestEnv{dbPath: filepath.Join(t.TempDir(), "cache.db")}
	_, _, dag := env.open(t)

	var names []string
	for name, def := range dag.Schema().Types {
		if def.Kind != ast.Object || strings.HasPrefix(name, "__") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	require.NotEmpty(t, names)

	var missing []string
	families := map[string]string{}
	for _, name := range names {
		objType, ok := dag.ObjectType(name)
		require.True(t, ok, name)
		typed := objType.Typed()
		_, encodes := typed.(dagql.PersistedObject)
		_, decodes := typed.(dagql.PersistedObjectDecoder)
		family, registered := dagql.PersistedObjectFamilyFor(typed)
		if !encodes || !decodes || !registered {
			missing = append(missing, fmt.Sprintf("%s(%T encode=%t decode=%t family=%t)", name, typed, encodes, decodes, registered))
			continue
		}
		families[name] = family.Name
	}
	require.Equal(t, persistedSchemaExpectedWithoutCodec, missing, "every installed core object either has a codec pair with a registered family or is one of the documented exceptions")
	for _, name := range []string{"EnvVariable", "Port", "Label", "HealthcheckConfig", "SDKConfig", "ModuleConfigClient", "Schema", "GitBundleRef", "CurrentModule", "WorkspaceMigration", "WorkspaceMigrationStep", "Cloud", "Terminal", "Check", "CheckGroup", "Up", "UpGroup", "TerminalTarget", "TerminalGroup", "Container", "Directory", "GitRepository"} {
		require.Contains(t, families, name, "%s is an installed core object with a family", name)
	}
}

// persistedSchemaExpectedWithoutCodec is the exact set of installed core
// object types with no persisted codec in this batch, each with its
// source-backed reason. None of them is excluded inside a codec: an admitted
// value of one of these types still fails the whole save, as it does today.
var persistedSchemaExpectedWithoutCodec = []string{
	// The @agent middleware values are deferred with the LLM conversation
	// batch by the Human's scope reduction.
	"Agent(*core.Agent encode=false decode=false family=false)",
	"AgentMessage(*core.AgentMessage encode=false decode=false family=false)",
	"AgentMiddleware(*core.AgentMiddleware encode=false decode=false family=false)",
	"AgentMiddlewareGroup(*core.AgentMiddlewareGroup encode=false decode=false family=false)",
	// Engine inspection values are hidden from module SDKs
	// (core.TypesHiddenFromModuleSDKs), so no module-return route exists;
	// they remain a reachability question, not a permanent exclusion.
	"Engine(*core.Engine encode=false decode=false family=false)",
	"EngineCache(*core.EngineCache encode=false decode=false family=false)",
	"EngineCacheEntry(*core.EngineCacheEntry encode=false decode=false family=false)",
	"EngineCacheEntrySet(*core.EngineCacheEntrySet encode=false decode=false family=false)",
	// The complete LLM conversation and its message, block and skill values
	// belong to the separately commissioned conversation batch.
	"LLM(*core.LLM encode=false decode=false family=false)",
	"LLMContentBlock(*core.LLMContentBlock encode=false decode=false family=false)",
	"LLMMessage(*core.LLMMessage encode=false decode=false family=false)",
	"LLMMessageOrigin(*core.LLMMessageOrigin encode=false decode=false family=false)",
	"LLMSkill(*core.LLMSkill encode=false decode=false family=false)",
	// The root has no value of its own.
	"Query(*core.Query encode=false decode=false family=false)",
	// Introspection reflection helpers returned by the TypeDef directive
	// fields describe schema, not values, and have no retained route.
	"_DirectiveApplication(*introspection.DirectiveApplication encode=false decode=false family=false)",
	"_DirectiveApplicationArg(*introspection.DirectiveApplicationArg encode=false decode=false family=false)",
}

// TestCoreSchemaModuleReturnDispatchSurvivesRestart drives the actual
// core-object return converter that module functions use to hand back
// existing core values by ID, saves the rows, restarts, selects their fields
// through the real schema and saves them again.
func TestCoreSchemaModuleReturnDispatchSurvivesRestart(t *testing.T) {
	env := &persistedSchemaTestEnv{dbPath: filepath.Join(t.TempDir(), "cache.db")}
	ctx, cache, dag := env.open(t)
	coreMod := core.NewSchemaBuilder(core.NewRoot(&currentTypeDefsTestServer{dag: dag}), nil)
	_ = coreMod

	described := "http"
	values := map[string]dagql.Typed{
		"env":    core.EnvVariable{Name: "CI", Value: "true"},
		"port":   core.Port{Port: 80, Protocol: core.NetworkProtocolTCP, Description: &described},
		"label":  Label{Name: "org.opencontainers.image.title", Value: "app"},
		"health": HealthcheckConfig{Args: []string{"CMD", "true"}, Shell: false, Timeout: "3s", Interval: "30s", StartPeriod: "0s", StartInterval: "5s", Retries: 3},
	}
	attach := func(ctx context.Context, cache *dagql.Cache, dag *dagql.Server, field string, self dagql.Typed) dagql.AnyResult {
		t.Helper()
		frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(self.Type())}
		res, err := cache.GetOrInitCall(ctx, persistedSchemaTestSession, dag, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
			objType, ok := dag.ObjectType(self.Type().Name())
			require.True(t, ok)
			valRes, err := dagql.NewResultForCall(self, frame)
			if err != nil {
				return nil, err
			}
			return objType.New(valRes)
		})
		require.NoError(t, err)
		return res
	}
	rowID := func(res dagql.AnyResult) uint64 {
		t.Helper()
		id, err := cache.PersistedResultID(res)
		require.NoError(t, err)
		return id
	}
	encoded := func(ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) dagql.PersistedResultEnvelope {
		t.Helper()
		encoding, err := dagql.DefaultPersistedSelfCodec.EncodeResult(ctx, cache, res)
		require.NoError(t, err)
		return encoding.Envelope
	}

	handles := map[string]string{}
	ids := map[string]uint64{}
	envelopes := map[string]dagql.PersistedResultEnvelope{}
	for name, value := range values {
		res := attach(ctx, cache, dag, name, value)
		ids[name] = rowID(res)
		id, err := res.(dagql.IDable).ID()
		require.NoError(t, err)
		handles[name], err = id.Encode()
		require.NoError(t, err)
		envelopes[name] = encoded(ctx, cache, res)
	}

	// The module return converter loads the exact row a module named.
	converters := map[string]*CoreModObject{}
	for name, value := range values {
		converters[name] = &CoreModObject{name: value.Type().Name()}
		res, err := converters[name].ConvertFromSDKResult(dagql.ContextWithCache(core.ContextWithQuery(ctx, core.NewRoot(&currentTypeDefsTestServer{dag: dag})), cache), handles[name])
		require.NoError(t, err)
		require.Equal(t, ids[name], rowID(res), "%s: the converter returns the exact attached row", name)
	}

	check := func(t *testing.T, ctx context.Context, cache *dagql.Cache, dag *dagql.Server, round string) {
		t.Helper()
		for name := range values {
			t.Run(round+"/"+name, func(t *testing.T) {
				var id call.ID
				require.NoError(t, id.Decode(handles[name]))
				res, err := converters[name].ConvertFromSDKResult(dagql.ContextWithCache(core.ContextWithQuery(ctx, core.NewRoot(&currentTypeDefsTestServer{dag: dag})), cache), handles[name])
				require.NoError(t, err)
				require.Equal(t, ids[name], rowID(res))
				obj, ok := res.(dagql.AnyObjectResult)
				require.True(t, ok)
				switch name {
				case "env":
					var value dagql.String
					require.NoError(t, dag.Select(ctx, obj, &value, dagql.Selector{Field: "value"}))
					require.Equal(t, dagql.String("true"), value)
				case "port":
					var port dagql.Int
					require.NoError(t, dag.Select(ctx, obj, &port, dagql.Selector{Field: "port"}))
					require.Equal(t, dagql.Int(80), port)
					var description dagql.Nullable[dagql.String]
					require.NoError(t, dag.Select(ctx, obj, &description, dagql.Selector{Field: "description"}))
					require.Equal(t, dagql.NonNull(dagql.String("http")), description)
				case "label":
					var value dagql.String
					require.NoError(t, dag.Select(ctx, obj, &value, dagql.Selector{Field: "value"}))
					require.Equal(t, dagql.String("app"), value)
				case "health":
					var retries dagql.Int
					require.NoError(t, dag.Select(ctx, obj, &retries, dagql.Selector{Field: "retries"}))
					require.Equal(t, dagql.Int(3), retries)
					var args dagql.Array[dagql.String]
					require.NoError(t, dag.Select(ctx, obj, &args, dagql.Selector{Field: "args"}))
					require.Equal(t, dagql.Array[dagql.String]{"CMD", "true"}, args)
				}
				require.Equal(t, envelopes[name], encoded(ctx, cache, res), "the second save is identical")
			})
		}
	}

	ctx, cache, dag = env.restart(t, ctx, cache)
	check(t, ctx, cache, dag, "first-restart")
	ctx, cache, dag = env.restart(t, ctx, cache)
	ctx, cache, dag = env.restart(t, ctx, cache)
	check(t, ctx, cache, dag, "after-untouched-save")

	t.Run("label and healthcheck payloads are lossless", func(t *testing.T) {
		for name, value := range map[string]dagql.Typed{"label": values["label"], "health": values["health"]} {
			encoding, err := value.(dagql.PersistedObject).EncodePersistedObject(ctx, nil)
			require.NoError(t, err)
			decoded, err := value.(dagql.PersistedObjectDecoder).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(nil, 0, nil), encoding.JSON)
			require.NoError(t, err)
			require.Equal(t, value, decoded, name)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(encoding.JSON, &payload))
			require.NotEmpty(t, payload)
		}
	})
}

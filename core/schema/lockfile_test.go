package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func withCurrentLockView(ctx context.Context) context.Context {
	return dagql.ContextWithCall(ctx, &dagql.ResultCall{View: call.View(workspaceLockingVersion)})
}

func TestResolveLookupFromLock(t *testing.T) {
	t.Parallel()

	const operation = "oci-sha"
	inputs := []any{"alpine:latest"}

	makeLock := func(t *testing.T, pin string) *workspace.Lock {
		t.Helper()
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, operation, inputs, pin))
		return lock
	}

	t.Run("disabled without a lock", func(t *testing.T) {
		t.Parallel()

		res := resolveLookupFromLoadedLock(nil, operation, inputs)
		require.Empty(t, res.Pin)
		require.False(t, res.ShouldWrite)
	})

	t.Run("existing pin entry", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: makeLock(t, "sha256:abc123")}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.False(t, res.ShouldWrite)
	})

	t.Run("missing entry is written", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: workspace.NewLock()}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Empty(t, res.Pin)
		require.True(t, res.ShouldWrite)
	})

	t.Run("refresh ignores existing pin without writing", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{
			lock:    makeLock(t, "sha256:abc123"),
			refresh: true,
		}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Empty(t, res.Pin)
		require.False(t, res.ShouldWrite)
	})
}

func TestLookupLockForAPI(t *testing.T) {
	t.Parallel()

	const operation = "oci-sha"

	t.Run("older API view disables locking", func(t *testing.T) {
		t.Parallel()

		ctx := dagql.ContextWithCall(context.Background(), &dagql.ResultCall{
			View: call.View("v1.0.0-beta.9"),
		})
		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.Nil(t, lock)
	})

	t.Run("current API view uses available workspace lock", func(t *testing.T) {
		t.Parallel()

		query := &core.Query{Server: &currentTypeDefsTestServer{
			workspaceLock:   workspace.NewLock(),
			workspaceLockOK: true,
		}}
		lock, err := lookupLockForAPI(withCurrentLockView(context.Background()), query, operation)
		require.NoError(t, err)
		require.NotNil(t, lock)
	})

	t.Run("private context disables locking", func(t *testing.T) {
		t.Parallel()

		ctx := withoutWorkspaceLookupLock(withCurrentLockView(context.Background()))
		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.Nil(t, lock)
	})

	t.Run("uses an in-memory overlay lock without a host binding", func(t *testing.T) {
		t.Parallel()

		overlay := workspace.NewLock()
		ctx := withWorkspaceLookupLockOverride(withCurrentLockView(context.Background()), overlay)

		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.NotNil(t, lock)

		inputs := []any{"alpine:latest"}
		want := "sha256:abc123"
		require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, operation, inputs, want))
		got, ok := overlay.GetLookup(workspace.CoreLockNamespace, operation, inputs)
		require.True(t, ok)
		require.Equal(t, want, got)
	})
}

// A noLock lookup resolves live, so no earlier call may answer it: it gets a
// fresh cache key per call. Without noLock the key is what it always was,
// and a full commit SHA, which is immutable, stays shared.
func TestNoLockLookupCacheInputs(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	server := &currentTypeDefsTestServer{}
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	const view = "v1.0.0"
	srv, err := base.Fork(ctx, core.NewRoot(server), view)
	require.NoError(t, err)
	server.dag = srv

	perClient, err := dagql.PerClientInput.Resolver(ctx, nil)
	require.NoError(t, err)
	shared := dagql.NewString("")
	sha := strings.Repeat("a", 40)

	for _, tc := range []struct {
		typeName string
		field    string
		input    string
		args     map[string]dagql.Input
		// today is the input's value without noLock; shared values stay
		// shared with it.
		today  dagql.Input
		shared bool
	}{
		{"GitRepository", "ref", "cachePerClientLock:name", map[string]dagql.Input{"name": dagql.String("main")}, perClient, false},
		{"GitRepository", "ref", "cachePerClientLock:name", map[string]dagql.Input{"name": dagql.String("main~2")}, perClient, false},
		{"GitRepository", "ref", "cachePerClientLock:name", map[string]dagql.Input{"name": dagql.String(sha)}, shared, true},
		{"GitRepository", "ref", "cachePerClientLock:name", map[string]dagql.Input{"name": dagql.String(sha + "~2")}, shared, true},
		{"GitRepository", "head", dagql.PerClientInput.Name, nil, perClient, false},
		{"GitRepository", "branch", dagql.PerClientInput.Name, map[string]dagql.Input{"name": dagql.String("main")}, perClient, false},
		{"GitRepository", "tag", dagql.PerClientInput.Name, map[string]dagql.Input{"name": dagql.String("v1.0.0")}, perClient, false},
		{"GitRepository", "latest", dagql.PerClientInput.Name, nil, perClient, false},
		{"Address", "gitRef", dagql.PerClientInput.Name, nil, perClient, false},
		{"Address", "directory", "cacheAsRequested:noCache", nil, perClient, false},
		{"Address", "file", "cacheAsRequested:noCache", nil, perClient, false},
	} {
		name := tc.typeName + "." + tc.field
		if arg, ok := tc.args["name"]; ok {
			name += "/" + arg.(dagql.String).String()
		}
		t.Run(name, func(t *testing.T) {
			objType, ok := srv.ObjectType(tc.typeName)
			require.True(t, ok)
			spec, ok := objType.FieldSpec(tc.field, view)
			require.True(t, ok)
			_, ok = spec.Args.Input("noLock", call.View(view))
			require.True(t, ok, "field takes noLock")
			var input *dagql.ImplicitInput
			for i := range spec.ImplicitInputs {
				if spec.ImplicitInputs[i].Name == tc.input {
					input = &spec.ImplicitInputs[i]
				}
			}
			require.NotNil(t, input, "implicit input %q keeps its name", tc.input)

			resolve := func(noLock dagql.Input) dagql.Input {
				t.Helper()
				args := map[string]dagql.Input{}
				for k, v := range tc.args {
					args[k] = v
				}
				if noLock != nil {
					args["noLock"] = noLock
				}
				got, err := input.Resolver(ctx, args)
				require.NoError(t, err)
				return got
			}

			require.Equal(t, tc.today, resolve(nil), "without noLock the key is unchanged")
			require.Equal(t, tc.today, resolve(dagql.Boolean(false)), "noLock: false keeps the key")

			first := resolve(dagql.Boolean(true))
			second := resolve(dagql.Boolean(true))
			if tc.shared {
				require.Equal(t, tc.today, first, "an immutable lookup stays shared")
				require.Equal(t, tc.today, second)
				return
			}
			require.NotEqual(t, tc.today, first, "noLock must not reuse the client's lookup")
			require.NotEqual(t, first, second, "each noLock lookup gets a fresh key")
		})
	}
}

package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func rebuildTestID[T dagql.Typed](t *testing.T, e *transferTestEngine, ctx context.Context, sels ...dagql.Selector) dagql.ID[T] {
	t.Helper()
	var res dagql.ObjectResult[T]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &res, sels...))
	id, err := res.ID()
	require.NoError(t, err)
	return dagql.NewID[T](id)
}

func rebuildTestArg(name string, value dagql.Input) dagql.NamedInput {
	return dagql.NamedInput{Name: name, Value: value}
}

// requireRebuildRoute asserts that every part of a saved value can be rebuilt
// on another engine: by its saved operation, or by delegation to its parent.
func requireRebuildRoute(t *testing.T, ctx context.Context, cache *dagql.Cache, value dagql.AnyResult) {
	t.Helper()
	record, err := cache.CapturePersistedRecord(ctx, value)
	require.NoError(t, err)
	family, ok := dagql.PersistedObjectFamilyFor(value.Unwrap())
	require.True(t, ok)
	router, ok := family.Transfer.(dagql.PersistedPartRouter)
	require.True(t, ok)
	var payload struct {
		Parts map[dagql.PartKey]json.RawMessage `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	parts := []dagql.PartKey{"snapshot"}
	if payload.Parts != nil {
		parts = parts[:0]
		for part := range payload.Parts {
			parts = append(parts, part)
		}
	}
	require.NotEmpty(t, parts)
	for _, part := range parts {
		route, err := router.RouteParts(dagql.PersistedPayloadVisit{Call: record.Call, Payload: record.Envelope.ObjectJSON}, part)
		require.NoError(t, err, part)
		require.True(t, route.HasLazyOperation || route.Delegation != nil, "part %s has neither a saved operation nor a delegation", part)
	}
}

// Every Container mutation with its own saved operation keeps it when the
// parent is already evaluated, as it always did for a pending parent, and
// still runs at the call over an evaluated parent.
func TestContainerMutationsSaveRecipeOverEvaluatedParent(t *testing.T) {
	e := newTransferTestEngine(t, transferTestSalt(t))
	ctx := e.session(t, "recipes")
	str := dagql.NewString
	dirID := rebuildTestID[*core.Directory](t, e, ctx, dagql.Selector{Field: "directory"})
	fileID := rebuildTestID[*core.File](t, e, ctx, dagql.Selector{Field: "file", Args: []dagql.NamedInput{rebuildTestArg("name", str("f")), rebuildTestArg("contents", str("c"))}})
	secretID := rebuildTestID[*core.Secret](t, e, ctx, dagql.Selector{Field: "setSecret", Args: []dagql.NamedInput{rebuildTestArg("name", str("s")), rebuildTestArg("plaintext", str("p"))}})
	envFileID := rebuildTestID[*core.EnvFile](t, e, ctx, dagql.Selector{Field: "envFile"}, dagql.Selector{Field: "withVariable", Args: []dagql.NamedInput{rebuildTestArg("name", str("E")), rebuildTestArg("value", str("v"))}})
	serviceID := rebuildTestID[*core.Service](t, e, ctx, dagql.Selector{Field: "container"}, dagql.Selector{Field: "asService", Args: []dagql.NamedInput{rebuildTestArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("true")))}})

	fields := []dagql.Selector{
		{Field: "withUser", Args: []dagql.NamedInput{rebuildTestArg("name", str("u"))}},
		{Field: "withoutUser"},
		{Field: "withLabel", Args: []dagql.NamedInput{rebuildTestArg("name", str("l")), rebuildTestArg("value", str("v"))}},
		{Field: "withoutLabel", Args: []dagql.NamedInput{rebuildTestArg("name", str("l"))}},
		{Field: "withAnnotation", Args: []dagql.NamedInput{rebuildTestArg("name", str("a")), rebuildTestArg("value", str("v"))}},
		{Field: "withoutAnnotation", Args: []dagql.NamedInput{rebuildTestArg("name", str("a"))}},
		{Field: "withDefaultArgs", Args: []dagql.NamedInput{rebuildTestArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("x")))}},
		{Field: "withoutEntrypoint"},
		{Field: "withoutWorkdir"},
		{Field: "withExposedPort", Args: []dagql.NamedInput{rebuildTestArg("port", dagql.NewInt(80))}},
		{Field: "withoutExposedPort", Args: []dagql.NamedInput{rebuildTestArg("port", dagql.NewInt(80))}},
		{Field: "withSecretVariable", Args: []dagql.NamedInput{rebuildTestArg("name", str("S")), rebuildTestArg("secret", secretID)}},
		{Field: "withoutSecretVariable", Args: []dagql.NamedInput{rebuildTestArg("name", str("S"))}},
		{Field: "withMountedSecret", Args: []dagql.NamedInput{rebuildTestArg("path", str("/s")), rebuildTestArg("source", secretID)}},
		{Field: "withMountedTemp", Args: []dagql.NamedInput{rebuildTestArg("path", str("/t"))}},
		{Field: "withServiceBinding", Args: []dagql.NamedInput{rebuildTestArg("alias", str("svc")), rebuildTestArg("service", serviceID)}},
		{Field: "withEnvFileVariables", Args: []dagql.NamedInput{rebuildTestArg("source", envFileID)}},
		{Field: "withVolatileVariable", Args: []dagql.NamedInput{rebuildTestArg("name", str("V")), rebuildTestArg("value", str("v"))}},
		{Field: "withoutVolatileVariable", Args: []dagql.NamedInput{rebuildTestArg("name", str("V"))}},
		{Field: "withGPU", Args: []dagql.NamedInput{rebuildTestArg("devices", dagql.ArrayInput[dagql.String](dagql.NewStringArray("0")))}},
		{Field: "experimentalWithAllGPUs"},
		{Field: "withDockerHealthcheck", Args: []dagql.NamedInput{rebuildTestArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("true")))}},
		{Field: "withoutDockerHealthcheck"},
		{Field: "__withImageConfigMetadata", Args: []dagql.NamedInput{rebuildTestArg("stopSignal", str("SIGTERM"))}},
		{Field: "withDefaultTerminalCmd", Args: []dagql.NamedInput{rebuildTestArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("sh")))}},
		{Field: "withShell", Args: []dagql.NamedInput{rebuildTestArg("interactive", dagql.ArrayInput[dagql.String](dagql.NewStringArray("sh")))}},
		{Field: "withDirectory", Args: []dagql.NamedInput{rebuildTestArg("path", str("/d")), rebuildTestArg("source", dirID)}},
		{Field: "withFile", Args: []dagql.NamedInput{rebuildTestArg("path", str("/f")), rebuildTestArg("source", fileID)}},
		{Field: "withNewFile", Args: []dagql.NamedInput{rebuildTestArg("path", str("/n")), rebuildTestArg("contents", str("c"))}},
		{Field: "withoutDirectory", Args: []dagql.NamedInput{rebuildTestArg("path", str("/d"))}},
		{Field: "withoutFile", Args: []dagql.NamedInput{rebuildTestArg("path", str("/f"))}},
		{Field: "withSymlink", Args: []dagql.NamedInput{rebuildTestArg("target", str("/f")), rebuildTestArg("linkName", str("/l"))}},
	}
	for _, evaluated := range []bool{true, false} {
		// Distinct roots keep the evaluated parent from being the pending one.
		var parent dagql.ObjectResult[*core.Container]
		if !evaluated {
			pendingID := rebuildTestID[*core.Directory](t, e, ctx, dagql.Selector{Field: "directory"}, dagql.Selector{Field: "withNewDirectory", Args: []dagql.NamedInput{rebuildTestArg("path", str("pending"))}})
			require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &parent, dagql.Selector{Field: "container"}, dagql.Selector{Field: "withRootfs", Args: []dagql.NamedInput{rebuildTestArg("directory", pendingID)}}))
			require.True(t, dagql.HasPendingLazyEvaluation(parent))
		} else {
			require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &parent, dagql.Selector{Field: "container"}, dagql.Selector{Field: "withRootfs", Args: []dagql.NamedInput{rebuildTestArg("directory", dirID)}}))
			require.NoError(t, e.cache.Evaluate(ctx, parent))
			require.False(t, dagql.HasPendingLazyEvaluation(parent))
		}
		for _, field := range fields {
			name := "pending/" + field.Field
			if evaluated {
				name = "evaluated/" + field.Field
			}
			switch field.Field {
			case "withDirectory", "withFile", "withNewFile":
				if evaluated {
					// Over a built parent the write runs at the call, which mounts
					// snapshots; TestRebuildContainerWritesOverEvaluatedParent in
					// core/integration covers it.
					continue
				}
			}
			t.Run(name, func(t *testing.T) {
				var child dagql.ObjectResult[*core.Container]
				require.NoError(t, e.dag.Select(ctx, parent, &child, field))
				require.NotNil(t, child.Self().Lazy, "the mutation must install its saved operation")
				// The work still runs when it always did: at the call over a built
				// parent, on first read over a pending one.
				require.Equal(t, !evaluated, dagql.HasPendingLazyEvaluation(child))
				// Settled metadata names the parts; no part is read.
				require.NoError(t, e.cache.EvaluateParts(ctx, child, core.ContainerPartMetadata))
				requireRebuildRoute(t, ctx, e.cache, child)
			})
		}
	}
}

// importForRebuild exports value from a without any blob, merges it into b
// and loads it there.
func importForRebuild(t *testing.T, a *transferTestEngine, actx context.Context, b *transferTestEngine, value dagql.AnyResult) (context.Context, dagql.AnyResult) {
	t.Helper()
	bundle := a.export(t, actx, value)
	// Rebuilding decodes saved operations against the session's server.
	bctx := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	reply, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Imported(), 1)
	loaded, err := b.cache.LoadResultByResultID(bctx, "b1-session", b.dag, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	return bctx, loaded
}

// The image files are saved with the operation that writes them. Building an
// image needs a real engine, so the file is attached here with its operation
// unevaluated, as asTarball and manifest build it; the integration suite
// builds and rebuilds real images.
func TestContainerImageFilesSaveRecipe(t *testing.T) {
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	actx := a.session(t, "a1")
	dirID := rebuildTestID[*core.Directory](t, a, actx, dagql.Selector{Field: "directory"})
	var ctr, variant dagql.ObjectResult[*core.Container]
	require.NoError(t, a.dag.Select(actx, a.dag.Root(), &ctr, dagql.Selector{Field: "container"}, dagql.Selector{Field: "withRootfs", Args: []dagql.NamedInput{rebuildTestArg("directory", dirID)}}))
	require.NoError(t, a.dag.Select(actx, a.dag.Root(), &variant, dagql.Selector{Field: "container"}, dagql.Selector{Field: "withEnvVariable", Args: []dagql.NamedInput{rebuildTestArg("name", dagql.NewString("V")), rebuildTestArg("value", dagql.NewString("1"))}}))
	parentID, err := a.cache.PersistedResultID(ctr)
	require.NoError(t, err)
	for _, field := range []string{"asTarball", "manifest"} {
		t.Run(field, func(t *testing.T) {
			lazy := &core.FileContainerImageLazy{LazyState: core.NewLazyState(), Parent: ctr, Manifest: field == "manifest"}
			if field == "asTarball" {
				lazy.PlatformVariants = []dagql.ObjectResult[*core.Container]{variant}
			}
			value := &core.File{Platform: ctr.Self().Platform, File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Lazy: lazy}
			frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Receiver: &dagql.ResultCallRef{ResultID: parentID}, Type: dagql.NewResultCallType(value.Type())}
			attached, err := a.cache.GetOrInitCall(actx, "a1-session", a.dag, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
				return dagql.NewObjectResultForCall(value, a.dag, frame)
			})
			require.NoError(t, err)
			requireRebuildRoute(t, actx, a.cache, attached)

			// The imported row keeps the operation, with its inputs remapped.
			bctx, loaded := importForRebuild(t, a, actx, b, attached)
			require.True(t, dagql.HasPendingLazyEvaluation(loaded))
			requireRebuildRoute(t, bctx, b.cache, loaded)
		})
	}
}

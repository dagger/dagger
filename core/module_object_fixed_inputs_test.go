package core

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestModuleObjectFixedInputs(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "module-object-fixed-inputs")
	ctx, cache, srv := env.open(t)
	mod := env.attach(t, ctx, cache, srv, "fixed-module", &Module{NameField: "fixed", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	def := NewObjectTypeDef("FixedInputs", "", nil)
	def.Collection = &CollectionConfig{Enabled: true}
	install := func() {
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: def}}))
	}
	install()
	direct := env.directory(t, ctx, cache, srv, "fixed-direct", "fixed-direct-snapshot")
	nested := env.directory(t, ctx, cache, srv, "fixed-nested", "fixed-nested-snapshot")
	handle := env.directory(t, ctx, cache, srv, "fixed-handle", "fixed-handle-snapshot")
	view := env.directory(t, ctx, cache, srv, "fixed-view", "fixed-view-snapshot")
	handleID, err := handle.ID()
	require.NoError(t, err)
	handleString, err := handleID.Encode()
	require.NoError(t, err)
	base := env.attach(t, ctx, cache, srv, "fixed-base", &ModuleObject{Module: mod, TypeDef: def})
	obj := &ModuleObject{
		Module: mod, TypeDef: def, CollectionBase: base,
		ArtifactView: &ArtifactView{sources: []dagql.AnyObjectResult{view}},
		Fields: map[string]any{
			"direct": direct,
			"nested": map[string]any{"list": []any{nested, handleString, handleID, *handleID, dagql.NewAnyID(handleID)}},
		},
	}
	parent := env.attach(t, ctx, cache, srv, "fixed-parent", obj)
	moduleField := env.attach(t, ctx, cache, srv, "fixed-module-field", &ModuleObject{
		Module: mod, TypeDef: def, Fields: map[string]any{"module": mod},
	})
	parentID, baseID := persistedRowID(t, cache, parent), persistedRowID(t, cache, base)
	modID, moduleFieldID := persistedRowID(t, cache, mod), persistedRowID(t, cache, moduleField)
	expected := []uint64{persistedRowID(t, cache, direct), persistedRowID(t, cache, nested), persistedRowID(t, cache, handle), persistedRowID(t, cache, view), baseID}
	check := func(t *testing.T) {
		inputs, err := cache.ResultPayloadInputIDs(ctx, env.session, parentID)
		require.NoError(t, err)
		require.ElementsMatch(t, expected, inputs)
		require.NotContains(t, inputs, modID, "a provider module alone is an ownership edge")
		inputs, err = cache.ResultPayloadInputIDs(ctx, env.session, baseID)
		require.NoError(t, err)
		require.Empty(t, inputs, "a collection must not list itself as an input")
		inputs, err = cache.ResultPayloadInputIDs(ctx, env.session, moduleFieldID)
		require.NoError(t, err)
		require.Equal(t, []uint64{modID}, inputs, "an explicit field can name the provider module")
	}
	t.Run("live", check)
	t.Run("typed inline attachment", func(t *testing.T) {
		typ := &ModuleObjectType{mod: mod, typeDef: NewObjectTypeDef("FixedInline", "", nil)}
		fieldCtx := dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("fixed-inline", &ModuleObject{TypeDef: typ.typeDef}))
		original := map[string]any{"leaf": "before-attachment"}
		updated, deps, err := attachTypedModuleObjectValue(fieldCtx, typ, original, func(value dagql.AnyResult) (dagql.AnyResult, error) {
			inline, ok := dagql.UnwrapAs[*ModuleObject](value)
			require.True(t, ok)
			inline.Fields["leaf"] = map[string]any{"list": []any{direct}}
			return value, nil
		})
		require.NoError(t, err)
		require.Equal(t, "before-attachment", original["leaf"], "attachment must preserve the caller's map")
		require.Equal(t, map[string]any{"leaf": map[string]any{"list": []any{direct}}}, updated)
		require.Equal(t, []dagql.DependencyResult{{Result: direct, Owned: true, ImmutableInput: true}}, deps,
			"fixed inputs must name references stored after attachment")
	})
	legacy, err := obj.AttachDependencyResults(ctx, parent, func(value dagql.AnyResult) (dagql.AnyResult, error) { return value, nil })
	require.NoError(t, err)
	kinds, err := obj.AttachDependencyResultsKinds(ctx, parent, func(value dagql.AnyResult) (dagql.AnyResult, error) { return value, nil })
	require.NoError(t, err)
	var owned []dagql.AnyResult
	for _, dep := range kinds {
		require.True(t, dep.Owned, "attachment must preserve failure attribution")
		owned = append(owned, dep.Result)
	}
	require.Equal(t, legacy, owned, "both methods must return the same ownership edges")
	assertPersistedRefsMatchOwnership(t, ctx, cache, parent)
	ctx, cache, srv = env.restart(t, ctx, cache)
	loadedMod, err := cache.LoadResultByResultID(ctx, env.session, srv, modID)
	require.NoError(t, err)
	require.NotNil(t, loadedMod)
	require.IsType(t, &Module{}, loadedMod.Unwrap())
	var ok bool
	mod, ok = loadedMod.(dagql.ObjectResult[*Module])
	require.True(t, ok)
	require.NotNil(t, mod.Self())
	require.Equal(t, modID, persistedRowID(t, cache, mod))
	install()
	// Read restored metadata before loading the parent payload.
	t.Run("persisted", check)
	restored, err := cache.LoadResultByResultID(ctx, env.session, srv, parentID)
	require.NoError(t, err)
	require.NotNil(t, restored)
	require.IsType(t, &ModuleObject{}, restored.Unwrap())
	require.Equal(t, parentID, persistedRowID(t, cache, restored))
	assertPersistedRefsMatchOwnership(t, ctx, cache, restored)
}

func TestWorkspaceInputFixedMembership(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "fixed-membership")
	ctx, cache, srv := env.open(t)
	mod := env.attach(t, ctx, cache, srv, "membership-module", &Module{NameField: "membership", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	def := NewObjectTypeDef("FixedMembership", "", nil)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: def}}))
	child := env.directory(t, ctx, cache, srv, "membership-child", "membership-snapshot")
	parent := env.attach(t, ctx, cache, srv, "membership-parent", &ModuleObject{Module: mod, TypeDef: def, Fields: map[string]any{"child": child}})
	childID := persistedRowID(t, cache, child)
	parentID := persistedRowID(t, cache, parent)
	collector := &workspaceInputCollector{ctx: ctx, cache: cache, server: srv, session: env.session}
	contains := func(root, target uint64, c *workspaceInputCollector) bool {
		reads := &workspaceInputReads{values: map[uint64]*dagql.ResultCallRef{root: {ResultID: root}}}
		return reads.containsInput(c, &dagql.ResultCallRef{ResultID: target})
	}
	t.Run("busy parent", func(t *testing.T) {
		taskCtx, cancelTask := context.WithCancel(ctx)
		defer cancelTask()
		started, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- cache.RunLazyTask(taskCtx, parent, "lazy:fixed-membership", dagql.LazyTaskSpec{Body: func(context.Context) error {
				close(started)
				<-release
				return nil
			}})
		}()
		defer func() {
			close(release)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				cancelTask()
				t.Error("parent task did not stop")
			}
		}()
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("parent task did not start")
		case <-ctx.Done():
			t.Fatal(context.Cause(ctx))
		}
		_, err := cache.CapturePersistedRecord(ctx, parent)
		require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
		inputs, err := cache.ResultPayloadInputIDs(ctx, env.session, parentID)
		require.NoError(t, err)
		require.Equal(t, []uint64{childID}, inputs)
		require.True(t, contains(parentID, childID, collector), "fixed fields remain valid while the parent evaluates")
	})
	t.Run("wider native shape", func(t *testing.T) {
		native := env.attach(t, ctx, cache, srv, "membership-native", &ModuleObject{
			Module: mod, TypeDef: def, Fields: map[string]any{"native": []dagql.AnyResult{child}},
		})
		nativeID := persistedRowID(t, cache, native)
		inputs, err := cache.ResultPayloadInputIDs(ctx, env.session, nativeID)
		require.NoError(t, err)
		require.Empty(t, inputs, "the live attachment walker does not cover native slices")
		require.True(t, contains(nativeID, childID, collector), "empty metadata must retain the full traversal")
	})
	t.Run("unrelated equal content alias", func(t *testing.T) {
		content := digest.FromString("fixed-membership-equal-content")
		published, err := child.WithContentDigestAny(ctx, content)
		require.NoError(t, err)
		alias := func(name string, source dagql.AnyResult) dagql.AnyResult {
			frame := moduleObjectTestSyntheticCall(name, source)
			fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: true, CapabilitiesComplete: true}
			result, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{
				ResultCall: frame, IsPersistable: true,
				ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true,
			}, func(context.Context) (dagql.AnyResult, error) { return source, nil })
			require.NoError(t, err)
			record, err := cache.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			require.Equal(t, persistedRowID(t, cache, source), record.Envelope.ValueResultID)
			return result
		}
		other := alias("membership-unrelated", published)
		other, err = other.WithContentDigestAny(ctx, content)
		require.NoError(t, err)
		otherID := persistedRowID(t, cache, other)
		require.NotEqual(t, childID, otherID)
		left, err := published.(dagql.ObjectResult[*Directory]).ContentPreferredDigest(ctx)
		require.NoError(t, err)
		right, err := other.(dagql.ObjectResult[*Directory]).ContentPreferredDigest(ctx)
		require.NoError(t, err)
		require.Equal(t, left, right)
		require.False(t, contains(parentID, otherID, collector), "equal contents do not prove exact membership")
		parentAlias := alias("membership-parent-alias", parent)
		require.True(t, contains(persistedRowID(t, cache, parentAlias), childID, collector), "an alias must follow its owned value row")
	})
	t.Run("access refusal", func(t *testing.T) {
		const handle = dagql.SessionResourceHandle("fixed-membership-resource")
		require.NoError(t, cache.BindSessionResource(ctx, env.session, env.session, handle, &Secret{URIVal: "env://FIXED"}))
		secret := env.attachWithResourceHandle(t, ctx, cache, srv, "membership-secret", &Secret{URIVal: "env://FIXED"}, handle)
		protected := env.attach(t, ctx, cache, srv, "membership-protected", &ModuleObject{Module: mod, TypeDef: def, Fields: map[string]any{"secret": secret}})
		denied := *collector
		denied.session = "fixed-membership-unbound"
		protectedID, secretID := persistedRowID(t, cache, protected), persistedRowID(t, cache, secret)
		_, err := cache.LoadResultByResultID(ctx, denied.session, srv, secretID)
		require.Error(t, err)
		require.False(t, contains(protectedID, secretID, &denied))
	})
	t.Run("closed session", func(t *testing.T) {
		require.True(t, contains(parentID, childID, collector))
		require.NoError(t, cache.ReleaseSession(ctx, env.session))
		_, err := cache.LoadResultByResultID(ctx, env.session, srv, childID)
		require.Error(t, err)
		require.False(t, contains(parentID, childID, collector))
	})
}

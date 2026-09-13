package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestContainerCompletedProducerPersistsWithoutLoadingParents(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "completed-container")
	ctx, cache, srv := env.open(t)
	platform := Platform{OS: "linux", Architecture: "amd64"}
	parent := NewContainer(platform)
	parent.FS.setValue(containerPersistenceTestDirectory("producer-fs", "/selected"))
	parent.MetaSnapshot.setValue(&cacheVolumeTestImmutableRef{id: "producer-meta", snapshotID: "producer-meta"})
	parentRes := env.attach(t, ctx, cache, srv, "producer-parent", parent).(dagql.ObjectResult[*Container])
	otherParent := env.attach(t, ctx, cache, srv, "producer-other-parent", NewContainer(platform))
	parentID := persistedRowID(t, cache, parentRes)
	otherParentID := persistedRowID(t, cache, otherParent)
	child := NewContainer(platform)
	child.Lazy = &ContainerWithLabelLazy{LazyState: NewLazyState(), Parent: parentRes, Name: "retained", Value: "yes"}
	childRes := env.attach(t, ctx, cache, srv, "withLabel", child)
	pendingRecipe, err := child.Lazy.EncodePersisted(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
	require.NoError(t, err)
	require.NoError(t, cache.Evaluate(ctx, childRes))
	require.Equal(t, "yes", child.Config.Labels["retained"])
	require.Nil(t, child.lazyOpForRouting())
	require.NotNil(t, child.completedRecipe)
	rec := coreRelocationRecord(t, ctx, cache, childRes)
	var payload persistedContainerPayload
	require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &payload))
	require.JSONEq(t, string(pendingRecipe), string(payload.LazyJSON))
	fsOpens := env.manager.openCount("producer-fs")
	metaOpens := env.manager.openCount("producer-meta")

	ctx, cache, srv = env.restart(t, ctx, cache)
	assertParentsUnloaded := func() {
		t.Helper()
		found := 0
		for _, row := range cache.DebugEGraphSnapshot().Results {
			if row.SharedResultID == parentID || row.SharedResultID == otherParentID {
				require.False(t, row.HasValue, "parent %d was decoded", row.SharedResultID)
				found++
			}
		}
		require.Equal(t, 2, found)
	}
	assertParentsUnloaded()
	loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, rec.ResultID)
	require.NoError(t, err)
	restored, ok := dagql.UnwrapAs[*Container](loaded)
	require.True(t, ok)
	require.Nil(t, restored.completedRecipe)
	require.JSONEq(t, string(pendingRecipe), string(restored.completedRecipeJSON))
	assertParentsUnloaded()
	require.NoError(t, cache.EvaluateParts(ctx, loaded, ContainerPartMetadata))
	require.Equal(t, "yes", restored.Config.Labels["retained"])
	require.Equal(t, fsOpens, env.manager.openCount("producer-fs"))
	require.Equal(t, metaOpens, env.manager.openCount("producer-meta"))

	wantErr := errors.New("local snapshot unavailable")
	env.manager.beforeOpen = func(_ context.Context, id string) error {
		if id == "producer-fs" {
			return wantErr
		}
		return nil
	}
	require.ErrorIs(t, cache.EvaluateParts(ctx, loaded, ContainerPartFS), wantErr)
	assertParentsUnloaded()
	require.NoError(t, cache.EvaluateParts(ctx, loaded, ContainerPartExecMeta))
	require.Equal(t, metaOpens+1, env.manager.openCount("producer-meta"))
	_, fsReady := restored.FS.Peek()
	require.False(t, fsReady)
	env.manager.beforeOpen = nil
	require.NoError(t, cache.EvaluateParts(ctx, loaded, ContainerPartFS))
	require.Equal(t, fsOpens+2, env.manager.openCount("producer-fs"))
	require.Nil(t, restored.lazyOpForRouting())
	require.JSONEq(t, string(pendingRecipe), string(restored.completedRecipeJSON))
	enc := dagql.NewPersistEncodeContext(cache, rec.ResultID, rec.Call)
	reencoded, err := restored.EncodePersistedObject(ctx, enc)
	require.NoError(t, err)
	require.JSONEq(t, string(rec.Envelope.ObjectJSON), string(reencoded.JSON))
	assertParentsUnloaded()

	t.Run("completed producer references relocate without loading", func(t *testing.T) {
		reloc := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID, parentID: otherParentID}}
		out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
		require.NoError(t, err)
		require.Equal(t, map[string]uint64{"objectJSON.lazyJSON.parentResultID": parentID}, reloc.childIDs())
		var relocated persistedContainerPayload
		require.NoError(t, json.Unmarshal(out.Envelope.ObjectJSON, &relocated))
		var recipe persistedContainerWithLabelLazy
		require.NoError(t, json.Unmarshal(relocated.LazyJSON, &recipe))
		require.Equal(t, otherParentID, recipe.ParentResultID)
		value, err := (&Container{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
		require.NoError(t, err)
		again, err := value.(*Container).EncodePersistedObject(ctx, enc)
		require.NoError(t, err)
		require.JSONEq(t, string(out.Envelope.ObjectJSON), string(again.JSON))
		assertParentsUnloaded()
	})

	t.Run("old complete rows without producer bytes", func(t *testing.T) {
		payload.LazyJSON = nil
		oldJSON, err := json.Marshal(payload)
		require.NoError(t, err)
		value, err := (&Container{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, rec.ResultID, rec.Call), oldJSON)
		require.NoError(t, err)
		old := value.(*Container)
		require.Empty(t, old.completedRecipeJSON)
		again, err := old.EncodePersistedObject(ctx, enc)
		require.NoError(t, err)
		require.JSONEq(t, string(oldJSON), string(again.JSON))
		assertParentsUnloaded()
	})
}

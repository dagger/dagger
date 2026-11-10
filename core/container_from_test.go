package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestContainerCanUseFromContentDigest(t *testing.T) {
	t.Parallel()

	require.True(t, NewContainer(Platform{}).CanUseFromContentDigest())
	require.False(t, (&Container{}).CanUseFromContentDigest())
	require.False(t, (*Container)(nil).CanUseFromContentDigest())
}

// A scratch container restored from its record, after a restart or from
// another engine's bundle as an engine image's entries are, lets
// Container.From identify its result by content, as it did before it was
// stored; a derived one still doesn't. Otherwise every result built on a From
// of the restored container, a service's hostname among them, has another
// identity than the same calls on a fresh engine.
func TestContainerFromContentDigestSafetyIsRestored(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "from-content-digest-safety")
	ctx, cache, srv := env.open(t)
	platform := Platform{OS: "linux", Architecture: "amd64"}
	attach := func(field string, safe bool) dagql.AnyResult {
		ctr := NewContainer(platform)
		ctr.fromContentDigestSafe = safe
		ctr.FS.setValue(containerPersistenceTestDirectory(field+"-fs", "/"))
		ctr.MetaSnapshot.setValue(&cacheVolumeTestImmutableRef{id: field + "-meta", snapshotID: field + "-meta"})
		return env.attach(t, ctx, cache, srv, field, ctr)
	}
	want := map[string]bool{"scratch": true, "derived": false}
	ids := map[string]uint64{}
	foreign := map[string]dagql.PersistedRecord{}
	for field, safe := range want {
		res := attach(field, safe)
		ids[field] = persistedRowID(t, cache, res)
		rec, err := cache.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		foreign[field] = rec
	}

	t.Run("from another engine", func(t *testing.T) {
		for field, safe := range want {
			rec := foreign[field]
			normalized, err := foreignFamilyCodec("Container").NormalizeForeign(dagql.PersistedPayloadVisit{Payload: rec.Envelope.ObjectJSON, Call: rec.Call})
			require.NoError(t, err)
			value, err := (&Container{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, rec.ResultID, rec.Call), normalized.JSON)
			require.NoError(t, err)
			require.Equal(t, safe, value.(*Container).CanUseFromContentDigest(), field)
		}
	})

	t.Run("after a restart", func(t *testing.T) {
		ctx, cache, srv := env.restart(t, ctx, cache)
		for field, safe := range want {
			loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, ids[field])
			require.NoError(t, err)
			restored, ok := dagql.UnwrapAs[*Container](loaded)
			require.True(t, ok)
			require.Equal(t, safe, restored.CanUseFromContentDigest(), field)
		}
	})
}

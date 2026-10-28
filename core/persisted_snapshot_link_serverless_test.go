package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestPersistedSnapshotLinkMetadataWithoutServer(t *testing.T) {
	want := dagql.PersistedSnapshotRefLink{Role: "snapshot", RefKey: "fixture-snapshot"}
	dec := dagql.NewPersistDecodeContext(nil, 42, nil).
		WithSnapshotRoles([]dagql.PersistedSnapshotRefLink{want})

	links, err := dec.SnapshotRoles(t.Context())
	require.NoError(t, err)
	require.Equal(t, []dagql.PersistedSnapshotRefLink{want}, links)

	link, err := loadPersistedSnapshotLinkByResultID(t.Context(), dec, "directory", "snapshot")
	require.NoError(t, err)
	require.Equal(t, want, link)

	links, err = loadPersistedSnapshotLinksByResultID(t.Context(), dec, "directory")
	require.NoError(t, err)
	require.Equal(t, []dagql.PersistedSnapshotRefLink{want}, links)
}

func TestPersistedSnapshotLinkMetadataRequiresOwnerAndRole(t *testing.T) {
	t.Run("owner", func(t *testing.T) {
		dec := dagql.NewPersistDecodeContext(nil, 0, nil)
		_, err := loadPersistedSnapshotLinkByResultID(t.Context(), dec, "directory", "snapshot")
		require.EqualError(t, err, "load persisted directory snapshot link: zero storage owner")
	})
	t.Run("role", func(t *testing.T) {
		dec := dagql.NewPersistDecodeContext(nil, 42, nil).WithSnapshotRoles(nil)
		_, err := loadPersistedSnapshotLinkByResultID(t.Context(), dec, "directory", "snapshot")
		require.ErrorContains(t, err, "missing persisted snapshot link role")
	})
	t.Run("scoped role", func(t *testing.T) {
		dec := dagql.NewPersistDecodeContext(nil, 42, nil).WithSnapshotRoles([]dagql.PersistedSnapshotRefLink{
			{Role: "snapshot", RefKey: "other-snapshot", OutputPath: dagql.PersistedRefPath{}.Field("other")},
		})
		_, err := loadPersistedSnapshotLinkByResultID(t.Context(), dec, "directory", "snapshot")
		require.ErrorContains(t, err, "missing persisted snapshot link role")
	})
}

package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// A part copied from a result the container depends on is borrowed while it
// still holds the copied snapshot; one written after the copy, as a path
// writer copies and then writes, holds its own.
func TestContainerBorrowedPartLinks(t *testing.T) {
	ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	ctr.FS.setValue(partTestDirectory(&partFixtureTestRef{id: "parent-fs"}, "/"))
	mountDir := new(LazyAccessor[*Directory, *Container])
	mountDir.setValue(partTestDirectory(&partFixtureTestRef{id: "parent-mount"}, "/"))
	ctr.Mounts = []ContainerMount{{Target: "/mnt", DirectorySource: mountDir}}
	ctr.markBorrowed(ContainerPartFS)
	ctr.markBorrowed(ContainerPartMount("/mnt"))

	borrowed := func() map[string]bool {
		out := map[string]bool{}
		for _, link := range ctr.PersistedSnapshotRefLinks() {
			out[link.RefKey] = link.Borrowed
		}
		return out
	}
	require.Equal(t, map[string]bool{"parent-fs": true, "parent-mount": true}, borrowed())

	// Written after the copy: the rootfs and the mount hold new snapshots.
	ctr.FS.setValue(partTestDirectory(&partFixtureTestRef{id: "written-fs"}, "/"))
	require.Equal(t, map[string]bool{"written-fs": false, "parent-mount": true}, borrowed())
	mountDir.setValue(partTestDirectory(&partFixtureTestRef{id: "written-mount"}, "/"))
	require.Equal(t, map[string]bool{"written-fs": false, "written-mount": false}, borrowed())

	// A part with no snapshot when copied records nothing.
	empty := NewContainer(ctr.Platform)
	empty.markBorrowed(ContainerPartFS)
	empty.FS.setValue(partTestDirectory(&partFixtureTestRef{id: "later-fs"}, "/"))
	require.False(t, empty.PersistedSnapshotRefLinks()[0].Borrowed)
}

// Read by part, a container names each part's link as it does read whole,
// and every role the part links under, held or not.
func TestContainerSnapshotOwnerParts(t *testing.T) {
	ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	ctr.FS.setValue(partTestDirectory(&partFixtureTestRef{id: "fs"}, "/"))
	ctr.MetaSnapshot.setValue(&partFixtureTestRef{id: "meta"})
	dir := new(LazyAccessor[*Directory, *Container])
	dir.setValue(partTestDirectory(&partFixtureTestRef{id: "dir"}, "/"))
	file := new(LazyAccessor[*File, *Container])
	ctr.Mounts = []ContainerMount{
		{Target: "/dir", DirectorySource: dir},
		{Target: "/file", FileSource: file},
	}
	ctr.markBorrowed(ContainerPartMount("/dir"))

	parts := []dagql.PartKey{ContainerPartFS, ContainerPartExecMeta, ContainerPartMount("/dir"), ContainerPartMount("/file"), ContainerPartMetadata}
	links, roles, ok := ctr.ReadSnapshotOwnerParts(parts)
	require.True(t, ok)
	require.ElementsMatch(t, ctr.PersistedSnapshotRefLinks(), links, "all parts read by part are the container read whole")
	require.ElementsMatch(t, []string{"fs", "meta", "mount_dir:0", "mount_file:0", "mount_dir:1", "mount_file:1"}, roles)

	links, roles, ok = ctr.ReadSnapshotOwnerParts([]dagql.PartKey{ContainerPartMount("/dir")})
	require.True(t, ok)
	require.Equal(t, []dagql.PersistedSnapshotRefLink{{RefKey: "dir", Role: "mount_dir:0", Borrowed: true}}, links)
	require.Equal(t, []string{"mount_dir:0", "mount_file:0"}, roles)

	_, _, ok = ctr.ReadSnapshotOwnerParts([]dagql.PartKey{ContainerPartMount("/elsewhere")})
	require.False(t, ok, "a part the container doesn't have is read whole")
}

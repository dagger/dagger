package core

import (
	"testing"

	"github.com/stretchr/testify/require"
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

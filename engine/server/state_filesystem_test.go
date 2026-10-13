package server

import (
	"testing"

	"github.com/moby/sys/mountinfo"
	"github.com/stretchr/testify/require"
)

func TestContainingMount(t *testing.T) {
	root := &mountinfo.Info{Mountpoint: "/", Source: "root"}
	sibling := &mountinfo.Info{Mountpoint: "/var/lib/dagger", Source: "sibling"}
	state := &mountinfo.Info{Mountpoint: "/var/lib/dagger-state", Source: "state"}
	over := &mountinfo.Info{Mountpoint: "/var/lib/dagger-state", Source: "over"}

	// A mountpoint that is only a string prefix of the path doesn't contain it.
	require.Equal(t, root, containingMount([]*mountinfo.Info{root, sibling}, "/var/lib/dagger-state/worker"))
	require.Equal(t, state, containingMount([]*mountinfo.Info{root, sibling, state}, "/var/lib/dagger-state/worker"))
	require.Equal(t, sibling, containingMount([]*mountinfo.Info{root, sibling, state}, "/var/lib/dagger"))
	// The later of two mounts on one mountpoint is on top.
	require.Equal(t, over, containingMount([]*mountinfo.Info{root, state, over}, "/var/lib/dagger-state"))
	require.Nil(t, containingMount([]*mountinfo.Info{sibling}, "/srv"))
}

package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// locatePath looks at a container's mounts from the deepest up, so finding the
// first mount passes every other one. Passing a mount must not allocate:
// containers that build Go packages carry hundreds of them.
func TestLocatePathAllocsDoNotGrowWithMounts(t *testing.T) {
	allocs := func(n int) float64 {
		ctr := &Container{}
		for i := range n {
			// Longer than 32 bytes, so a concatenated prefix would not fit
			// the compiler's stack buffer.
			ctr.Mounts = append(ctr.Mounts, ContainerMount{Target: fmt.Sprintf("/go/pkg/mod/cache/download/example.com/module%04d", i)})
		}
		path := ctr.Mounts[0].Target + "/go.mod"
		return testing.AllocsPerRun(10, func() {
			mnt, sub, err := locatePath(ctr, path)
			if err != nil || mnt == nil || mnt.Target != ctr.Mounts[0].Target || sub != "go.mod" {
				panic(fmt.Sprintf("locatePath(%q) = %v, %q, %v", path, mnt, sub, err))
			}
		})
	}
	one, many := allocs(1), allocs(200)
	require.Equal(t, one, many, "finding a path past 199 other mounts allocated %.0f times, with no other mount %.0f times", many, one)
}

func TestLocatePath(t *testing.T) {
	ctr := &Container{Mounts: []ContainerMount{
		{Target: "/src"},
		{Target: "/src/sub"},
		{Target: "/cache", CacheSource: &CacheMountSource{}},
	}}
	ctr.Config.WorkingDir = "/src"

	for _, tc := range []struct {
		path, target, sub string
	}{
		{path: "/src", target: "/src", sub: "."},
		{path: "/src/a/b", target: "/src", sub: "a/b"},
		{path: "a", target: "/src", sub: "a"},
		// The deeper mount wins.
		{path: "/src/sub/c", target: "/src/sub", sub: "c"},
		// A sibling with the same prefix is not inside the mount.
		{path: "/srcx", sub: "/srcx"},
		{path: "/other", sub: "/other"},
	} {
		mnt, sub, err := locatePath(ctr, tc.path)
		require.NoError(t, err, tc.path)
		require.Equal(t, tc.sub, sub, tc.path)
		if tc.target == "" {
			require.Nil(t, mnt, tc.path)
			continue
		}
		require.NotNil(t, mnt, tc.path)
		require.Equal(t, tc.target, mnt.Target, tc.path)
		// The caller gets its own copy, not the container's mount.
		mnt.Readonly = true
		require.False(t, ctr.Mounts[0].Readonly || ctr.Mounts[1].Readonly, tc.path)
	}

	_, _, err := locatePath(ctr, "/cache/x")
	require.ErrorContains(t, err, "cannot retrieve path from cache")
}

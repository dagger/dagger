package dagger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputDir(t *testing.T) {
	st := &RouteState{
		LayoutSource: DirInput("/tmp/source", []string{}),
	}
	require.NoError(t, st.AddInput("www.source", DirInput(".", []string{})))

	route, err := NewRoute(st)
	require.NoError(t, err)

	localdirs := route.LocalDirs()
	require.Len(t, localdirs, 2)
	require.Contains(t, localdirs, ".")
	require.Contains(t, localdirs, "/tmp/source")
}

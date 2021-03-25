package dagger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputDir(t *testing.T) {
	st := &RouteState{}
	require.NoError(t, st.AddInput("www.source", DirInput(".", []string{})))

	route, err := NewRoute(st)
	require.NoError(t, err)

	localdirs := route.LocalDirs()
	require.Len(t, localdirs, 1)
	require.Equal(t, ".", localdirs["."])
}

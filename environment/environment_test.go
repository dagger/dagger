package environment

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/state"
)

func TestLocalDirs(t *testing.T) {
	st := &state.State{
		Path: "/tmp/source",
		Plan: "/tmp/source/plan",
	}
	require.NoError(t, st.SetInput("www.source", state.DirInput("/", []string{}, []string{})))

	environment, err := New(st)
	require.NoError(t, err)

	localdirs := environment.LocalDirs()
	require.Len(t, localdirs, 2)
	require.Contains(t, localdirs, "/")
	require.Contains(t, localdirs, "/tmp/source/plan")
}

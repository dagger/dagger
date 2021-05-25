package dagger

import (
	"testing"

	"dagger.io/go/dagger/state"
	"github.com/stretchr/testify/require"
)

func TestLocalDirs(t *testing.T) {
	st := &state.State{
		Path: "/tmp/source",
		Plan: "/tmp/source/plan",
	}
	require.NoError(t, st.SetInput("www.source", state.DirInput("/", []string{})))

	environment, err := NewEnvironment(st)
	require.NoError(t, err)

	localdirs := environment.LocalDirs()
	require.Len(t, localdirs, 2)
	require.Contains(t, localdirs, "/")
	require.Contains(t, localdirs, "/tmp/source/plan")
}

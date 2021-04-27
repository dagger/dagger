package dagger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputDir(t *testing.T) {
	st := &EnvironmentState{
		PlanSource: DirInput("/tmp/source", []string{}),
	}
	require.NoError(t, st.SetInput("www.source", DirInput("/", []string{})))

	environment, err := NewEnvironment(st)
	require.NoError(t, err)

	localdirs := environment.LocalDirs()
	require.Len(t, localdirs, 2)
	require.Contains(t, localdirs, "/")
	require.Contains(t, localdirs, "/tmp/source")
}

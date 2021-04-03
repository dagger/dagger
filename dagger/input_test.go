package dagger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputDir(t *testing.T) {
	st := &DeploymentState{
		PlanSource: DirInput("/tmp/source", []string{}),
	}
	require.NoError(t, st.SetInput("www.source", DirInput("/", []string{})))

	deployment, err := NewDeployment(st)
	require.NoError(t, err)

	localdirs := deployment.LocalDirs()
	require.Len(t, localdirs, 2)
	require.Contains(t, localdirs, "/")
	require.Contains(t, localdirs, "/tmp/source")
}

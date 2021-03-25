package dagger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvInputFlag(t *testing.T) {
	st := &RouteState{}
	require.NoError(t, st.AddInput("www.source", DirInput(".", []string{})))

	env, err := NewRoute(st)
	if err != nil {
		t.Fatal(err)
	}

	localdirs := env.LocalDirs()
	if len(localdirs) != 1 {
		t.Fatal(localdirs)
	}
	if dir, ok := localdirs["."]; !ok || dir != "." {
		t.Fatal(localdirs)
	}
}

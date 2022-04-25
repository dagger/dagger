package plan

import (
	"testing"

	"cuelang.org/go/cue"
	"github.com/stretchr/testify/require"
)

func TestClosestSubPath(t *testing.T) {
	rootPath := cue.MakePath(ActionSelector, cue.Str("test"))
	path1 := cue.MakePath(ActionSelector, cue.Str("test"), cue.Str("one"))
	path2 := cue.MakePath(ActionSelector, cue.Str("test"), cue.Str("two"))

	require.Equal(t, "actions.test.one", path1.String())
	require.Equal(t, "actions.test.two", path2.String())
	require.Equal(t, "actions.test", commonSubPath(rootPath, path1).String())
	require.Equal(t, "actions.test", commonSubPath(path1, path2).String())

	path3 := cue.MakePath(ActionSelector, cue.Str("test"), cue.Str("golang"), cue.Str("three"))
	path4 := cue.MakePath(ActionSelector, cue.Str("test"), cue.Str("java"), cue.Str("three"))
	require.Equal(t, "actions.test", commonSubPath(path3, path4).String())
}

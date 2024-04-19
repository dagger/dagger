package llb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelativeWd(t *testing.T) {
	st := Scratch().Dir("foo")
	assert.Equal(t, getDirHelper(t, st), "/foo")

	st = st.Dir("bar")
	assert.Equal(t, getDirHelper(t, st), "/foo/bar")

	st = st.Dir("..")
	assert.Equal(t, getDirHelper(t, st), "/foo")

	st = st.Dir("/baz")
	assert.Equal(t, getDirHelper(t, st), "/baz")

	st = st.Dir("../../..")
	assert.Equal(t, getDirHelper(t, st), "/")
}

func getDirHelper(t *testing.T, s State) string {
	t.Helper()
	v, err := s.GetDir(context.TODO())
	require.NoError(t, err)
	return v
}

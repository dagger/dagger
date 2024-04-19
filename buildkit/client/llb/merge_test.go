package llb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScratchMerge(t *testing.T) {
	t.Parallel()

	s := Merge(nil)
	require.Nil(t, s.Output())

	s = Merge([]State{})
	require.Nil(t, s.Output())

	s = Merge([]State{Scratch()})
	require.Nil(t, s.Output())

	s = Merge([]State{Scratch(), Scratch()})
	require.Nil(t, s.Output())

	input := Image("foo")
	s = Merge([]State{input})
	require.Equal(t, input.Output(), s.Output())

	s = Merge([]State{Scratch(), input, Scratch()})
	require.Equal(t, input.Output(), s.Output())

	s = Merge([]State{Scratch(), input, Image("bar")})
	require.NotEqual(t, input.Output(), s.Output())
}

package compiler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Test that a non-existing field is detected correctly
func TestFieldNotExist(t *testing.T) {
	c := &Compiler{}
	root, err := c.Compile("test.cue", `foo: "bar"`)
	require.NoError(t, err)
	require.True(t, root.Lookup("foo").Exists())
	require.False(t, root.Lookup("bar").Exists())
}

// Test that a non-existing definition is detected correctly
func TestDefNotExist(t *testing.T) {
	c := &Compiler{}
	root, err := c.Compile("test.cue", `foo: #bla: "bar"`)
	require.NoError(t, err)
	require.True(t, root.Lookup("foo.#bla").Exists())
	require.False(t, root.Lookup("foo.#nope").Exists())
}

func TestJSON(t *testing.T) {
	c := &Compiler{}
	v, err := c.Compile("", `foo: hello: "world"`)
	require.NoError(t, err)
	require.Equal(t, `{"foo":{"hello":"world"}}`, string(v.JSON()))

	// Reproduce a bug where Value.Lookup().JSON() ignores Lookup()
	require.Equal(t, `{"hello":"world"}`, string(v.Lookup("foo").JSON()))
}

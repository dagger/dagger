package state

import (
	"context"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWorkspace(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)

	// Open should fail since the directory is not initialized
	_, err = Open(ctx, root)
	require.ErrorIs(t, ErrNotInit, err)

	// Init
	workspace, err := Init(ctx, root)
	require.NoError(t, err)
	require.Equal(t, root, workspace.Path)

	// Create
	st, err := workspace.Create(ctx, "test", "", "")
	require.NoError(t, err)
	require.Equal(t, "test", st.Name)

	// Open
	workspace, err = Open(ctx, root)
	require.NoError(t, err)
	require.Equal(t, root, workspace.Path)

	// List
	envs, err := workspace.List(ctx)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	require.Equal(t, "test", envs[0].Name)

	// Get
	env, err := workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.Equal(t, "test", env.Name)

	// Save
	require.NoError(t, env.SetInput("foo", TextInput("bar")))
	require.NoError(t, workspace.Save(ctx, env))
	workspace, err = Open(ctx, root)
	require.NoError(t, err)
	env, err = workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.Contains(t, env.Inputs, "foo")
}

func TestEncryption(t *testing.T) {
	ctx := context.TODO()

	readManifest := func(st *State) *State {
		data, err := os.ReadFile(path.Join(st.Path, manifestFile))
		require.NoError(t, err)
		m := State{}
		require.NoError(t, yaml.Unmarshal(data, &m))
		return &m
	}

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	workspace, err := Init(ctx, root)
	require.NoError(t, err)

	_, err = workspace.Create(ctx, "test", "", "")
	require.NoError(t, err)

	// Set a plaintext input, make sure it is not encrypted
	st, err := workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.NoError(t, st.SetInput("plain", TextInput("plain")))
	require.NoError(t, workspace.Save(ctx, st))
	o := readManifest(st)
	require.Contains(t, o.Inputs, "plain")
	require.Equal(t, "plain", string(*o.Inputs["plain"].Text))

	// Set a secret input, make sure it's encrypted
	st, err = workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.NoError(t, st.SetInput("secret", SecretInput("secret")))
	require.NoError(t, workspace.Save(ctx, st))
	o = readManifest(st)
	require.Contains(t, o.Inputs, "secret")
	secretValue := string(*o.Inputs["secret"].Secret)
	require.NotEqual(t, "secret", secretValue)
	require.True(t, strings.HasPrefix(secretValue, "ENC["))

	// Change another input, make sure our secret didn't change
	st, err = workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.NoError(t, st.SetInput("plain", TextInput("different")))
	require.NoError(t, workspace.Save(ctx, st))
	o = readManifest(st)
	require.Contains(t, o.Inputs, "plain")
	require.Equal(t, "different", string(*o.Inputs["plain"].Text))
	require.Contains(t, o.Inputs, "secret")
	require.Equal(t, secretValue, string(*o.Inputs["secret"].Secret))
}

package main

import (
	"bytes"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestSecretScrubWriter_Write(t *testing.T) {
	fsys := fstest.MapFS{
		"mysecret": &fstest.MapFile{
			Data: []byte("my secret file"),
		},
		"subdir/alsosecret": &fstest.MapFile{
			Data: []byte("a subdir secret file"),
		},
	}
	env := []string{
		"MY_SECRET_ID=my secret value",
	}

	t.Run("scrub files and env", func(t *testing.T) {
		var buf bytes.Buffer
		w, err := NewSecretScrubWriter(&buf, fsys, env, core.SecretToScrubInfo{
			Envs:  []string{"MY_SECRET_ID"},
			Files: []string{"/mysecret", "/subdir/alsosecret"},
		})
		require.NoError(t, err)

		_, err = fmt.Fprintf(w, "I love to share my secret value to my close ones. But I keep my secret file to myself. As well as a subdir secret file.")
		require.NoError(t, err)
		want := "I love to share *** to my close ones. But I keep *** to myself. As well as ***."
		require.Equal(t, want, buf.String())
	})
	t.Run("do not scrub empty env", func(t *testing.T) {
		env := append(env, "EMPTY_SECRET_ID=")
		fsys := fstest.MapFS{
			"emptysecret": &fstest.MapFile{
				Data: []byte(""),
			},
		}

		var buf bytes.Buffer
		w, err := NewSecretScrubWriter(&buf, fsys, env, core.SecretToScrubInfo{
			Envs:  []string{"EMPTY_SECRET_ID"},
			Files: []string{"/emptysecret"},
		})
		require.NoError(t, err)

		_, err = fmt.Fprintf(w, "I love to share my secret value to my close ones. But I keep my secret file to myself.")
		require.NoError(t, err)
		want := "I love to share my secret value to my close ones. But I keep my secret file to myself."
		require.Equal(t, want, buf.String())
	})
}

func TestFilterSecretToScrub(t *testing.T) {
	secretValue := "my secret value"
	env := []string{
		fmt.Sprintf("MY_SECRET_ID=%s", secretValue),
		"PUBLIC_STUFF=so public",
	}

	secretToScrub := core.SecretToScrubInfo{
		Envs: []string{
			"MY_SECRET_ID",
		},
	}

	secrets := loadSecretsToScrubFromEnv(env, secretToScrub.Envs)
	require.NotContains(t, secrets, "PUBLIC_STUFF")
	require.Contains(t, secrets, secretValue)
}

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

var (
	//nolint:typecheck
	//go:embed testdata/id_ed25519
	sshSecretKey string

	//nolint:typecheck
	//go:embed testdata/id_ed25519.pub
	sshPublicKey string
)

func TestSecretScrubWriterWrite(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"mysecret": &fstest.MapFile{
			Data: []byte("my secret file"),
		},
		"subdir/alsosecret": &fstest.MapFile{
			Data: []byte("a subdir secret file \nwith line feed"),
		},
	}
	env := []string{
		"MY_SECRET_ID=my secret value",
	}

	t.Run("scrub files and env", func(t *testing.T) {
		var buf bytes.Buffer
		currentDirPath := "/"
		w, err := NewSecretScrubWriter(&buf, currentDirPath, fsys, env, core.SecretToScrubInfo{
			Envs:  []string{"MY_SECRET_ID"},
			Files: []string{"/mysecret", "/subdir/alsosecret"},
		})
		require.NoError(t, err)

		_, err = fmt.Fprintf(w, "I love to share my secret value to my close ones. But I keep my secret file to myself. As well as a subdir secret file \nwith line feed.")
		require.NoError(t, err)
		want := "I love to share *** to my close ones. But I keep *** to myself. As well as ***."
		require.Equal(t, want, buf.String())
	})

	t.Run("do not scrub empty env", func(t *testing.T) {
		env := append(env, "EMPTY_SECRET_ID=")
		currentDirPath := "/"
		fsys := fstest.MapFS{
			"emptysecret": &fstest.MapFile{
				Data: []byte(""),
			},
		}

		var buf bytes.Buffer
		w, err := NewSecretScrubWriter(&buf, currentDirPath, fsys, env, core.SecretToScrubInfo{
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

func TestLoadSecretsToScrubFromEnv(t *testing.T) {
	t.Parallel()
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

func TestLoadSecretsToScrubFromFiles(t *testing.T) {
	t.Parallel()
	const currentDirPath = "/mnt"
	t.Run("/mnt, fs relative, secret absolute", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"/mnt/mysecret", "/mnt/subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})

	t.Run("/mnt, fs relative, secret relative", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"mysecret", "subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})

	t.Run("/mnt, fs absolute, secret relative", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mnt/mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"mnt/subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"mnt/mysecret", "mnt/subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})
}

func TestScrubSecretWrite(t *testing.T) {
	t.Parallel()
	envMap := map[string]string{
		"secret1":      "secret1 value",
		"secret2":      "secret2",
		"sshSecretKey": sshSecretKey,
		"sshPublicKey": sshPublicKey,
	}

	env := []string{}
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	envNames := []string{
		"secret1",
		"secret2",
		"sshSecretKey",
		"sshPublicKey",
	}

	out := new(bytes.Buffer)
	w, err := NewSecretScrubWriter(out, "/", fstest.MapFS{}, env, core.SecretToScrubInfo{
		Envs:  envNames,
		Files: []string{},
	})
	require.NoError(t, err)

	t.Run("multiline secret", func(t *testing.T) {
		input := "aaa\n" + sshSecretKey + "\nbbb\nccc"
		_, err := w.Write([]byte(input))
		require.NoError(t, err)
		require.Equal(t, "aaa\n***\nbbb\nccc", out.String())
		out.Reset()

		_, err = w.Write([]byte(sshSecretKey))
		require.NoError(t, err)
		require.Equal(t, "***", out.String())
		out.Reset()
	})

	t.Run("single line secret", func(t *testing.T) {
		input := "aaa\nsecret1 value\nno secret\n"
		_, err := w.Write([]byte(input))
		require.NoError(t, err)
		require.Equal(t, "aaa\n***\nno secret\n", out.String())
		out.Reset()
	})

	t.Run("multi write", func(t *testing.T) {
		_, err := w.Write([]byte("secret1 value"))
		require.NoError(t, err)
		require.Equal(t, "***", out.String())

		_, err = w.Write([]byte("\nsecret2"))
		require.NoError(t, err)
		require.Equal(t, "***\n***", out.String())
	})

}

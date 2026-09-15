package server

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSecretSalt(t *testing.T) {
	t.Run("environment override", func(t *testing.T) {
		rootDir := t.TempDir()
		secretSaltPath := filepath.Join(rootDir, "secret-salt")
		fileSalt := bytes.Repeat([]byte{0x11}, secretSaltSize)
		require.NoError(t, os.WriteFile(secretSaltPath, fileSalt, 0600))

		envSalt := bytes.Repeat([]byte{0x22}, secretSaltSize)
		t.Setenv(secretSaltEnvName, base64.StdEncoding.EncodeToString(envSalt))

		secretSalt, err := loadSecretSalt(rootDir)
		require.NoError(t, err)
		require.Equal(t, envSalt, secretSalt)

		persistedSalt, err := os.ReadFile(secretSaltPath)
		require.NoError(t, err)
		require.Equal(t, fileSalt, persistedSalt)
	})

	for _, tc := range []struct {
		name   string
		values []string
	}{
		{
			name: "invalid base64",
			values: []string{
				"not-base64",
				base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, secretSaltSize)) + "\n",
			},
		},
		{name: "wrong decoded length", values: []string{base64.StdEncoding.EncodeToString([]byte("too short"))}},
		{name: "empty", values: []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootDir := t.TempDir()
			fileSalt := bytes.Repeat([]byte{0x11}, secretSaltSize)
			secretSaltPath := filepath.Join(rootDir, "secret-salt")
			require.NoError(t, os.WriteFile(secretSaltPath, fileSalt, 0600))

			for _, value := range tc.values {
				t.Setenv(secretSaltEnvName, value)
				_, err := loadSecretSalt(rootDir)
				require.ErrorContains(t, err, secretSaltEnvName)
			}

			persistedSalt, readErr := os.ReadFile(secretSaltPath)
			require.NoError(t, readErr)
			require.Equal(t, fileSalt, persistedSalt)
		})
	}

	t.Run("file fallback", func(t *testing.T) {
		unsetSecretSaltEnv(t)
		rootDir := t.TempDir()
		fileSalt := bytes.Repeat([]byte{0x33}, secretSaltSize)
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, "secret-salt"), fileSalt, 0600))

		secretSalt, err := loadSecretSalt(rootDir)
		require.NoError(t, err)
		require.Equal(t, fileSalt, secretSalt)
	})

	t.Run("generated file fallback", func(t *testing.T) {
		unsetSecretSaltEnv(t)
		rootDir := t.TempDir()

		secretSalt, err := loadSecretSalt(rootDir)
		require.NoError(t, err)
		require.Len(t, secretSalt, secretSaltSize)

		persistedSalt, err := os.ReadFile(filepath.Join(rootDir, "secret-salt"))
		require.NoError(t, err)
		require.Equal(t, secretSalt, persistedSalt)
	})
}

func unsetSecretSaltEnv(t *testing.T) {
	t.Helper()
	value, ok := os.LookupEnv(secretSaltEnvName)
	require.NoError(t, os.Unsetenv(secretSaltEnvName))
	t.Cleanup(func() {
		if ok {
			require.NoError(t, os.Setenv(secretSaltEnvName, value))
		} else {
			require.NoError(t, os.Unsetenv(secretSaltEnvName))
		}
	})
}

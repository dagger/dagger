package main

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
)

type SecretScrubWriter struct {
	mu           sync.Mutex
	w            io.Writer
	secretValues []string
	fs           fs.FS
}

func (w *SecretScrubWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	s := string(b)
	for _, secret := range w.secretValues {
		if secret == "" {
			continue
		}
		// FIXME: I think we can do better
		s = strings.ReplaceAll(s, secret, "***")
	}

	n, err := w.w.Write([]byte(s))
	if err != nil {
		return -1, err
	}
	if n != len(b) {
		n = len(b)
	}

	return n, err
}

// NewSecretScrubWriter replaces known secrets by "***".
// The value of the secrets referenced in secretsToScrub are loaded either
// from env or from the fs.
func NewSecretScrubWriter(w io.Writer, fsys fs.FS, env []string, secretsToScrub core.SecretToScrubInfo) (io.Writer, error) {
	secrets := loadSecretsToScrubFromEnv(env, secretsToScrub.Envs)

	fileSecrets, err := loadSecretsToScrubFromFiles(fsys, secretsToScrub.Files)
	if err != nil {
		return nil, fmt.Errorf("could not load secrets from file: %w", err)
	}
	secrets = append(secrets, fileSecrets...)

	return &SecretScrubWriter{
		fs:           fsys,
		w:            w,
		secretValues: secrets,
	}, nil
}

func loadSecretsToScrubFromEnv(env []string, secretsToScrub []string) []string {
	secrets := []string{}

	for _, envKV := range env {
		envName, envValue, ok := strings.Cut(envKV, "=")
		// no env value for this secret
		if !ok {
			continue
		}

		for _, envToScrub := range secretsToScrub {
			if envName == envToScrub {
				secrets = append(secrets, envValue)
			}
		}
	}

	return secrets
}

func loadSecretsToScrubFromFiles(fsys fs.FS, secretFilePathsToScrub []string) ([]string, error) {
	secrets := make([]string, 0, len(secretFilePathsToScrub))

	for _, fileToScrub := range secretFilePathsToScrub {
		absFileToScrub, err := filepath.Abs(fileToScrub)
		if err != nil {
			return secrets, err
		}

		// we remove the first `/` from the absolute path to  fileToScrub to work with fs.ReadFile
		secret, err := fs.ReadFile(fsys, absFileToScrub[1:])
		if err != nil {
			return nil, fmt.Errorf("secret value not available for: %w", err)
		}
		secrets = append(secrets, string(secret))
	}

	return secrets, nil
}

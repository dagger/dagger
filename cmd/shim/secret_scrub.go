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

	_, err := w.w.Write([]byte(s))
	if err != nil {
		return -1, err
	}

	return len(b), err
}

// NewSecretScrubWriter replaces known secrets by "***".
// The value of the secrets referenced in secretsToScrub are loaded either
// from env or from the fs.
func NewSecretScrubWriter(w io.Writer, currentDirPath string, fsys fs.FS, env []string, secretsToScrub core.SecretToScrubInfo) (io.Writer, error) {
	secrets := loadSecretsToScrubFromEnv(env, secretsToScrub.Envs)

	fileSecrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretsToScrub.Files)
	if err != nil {
		return nil, fmt.Errorf("could not load secrets from file: %w", err)
	}
	secrets = append(secrets, fileSecrets...)

	return &SecretScrubWriter{
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

// loadSecretsToScrubFromFiles loads secrets from file path in secretFilePathsToScrub from the fsys, accessed from the absolute currentDirPathAbs.
// It will attempt to make any file path as absolute file path by joining it with the currentDirPathAbs if need be.
func loadSecretsToScrubFromFiles(currentDirPathAbs string, fsys fs.FS, secretFilePathsToScrub []string) ([]string, error) {
	secrets := make([]string, 0, len(secretFilePathsToScrub))

	for _, fileToScrub := range secretFilePathsToScrub {
		absFileToScrub := fileToScrub
		if !filepath.IsAbs(fileToScrub) {
			absFileToScrub = filepath.Join("/", fileToScrub)
		}
		if strings.HasPrefix(fileToScrub, currentDirPathAbs) || strings.HasPrefix(fileToScrub, currentDirPathAbs[1:]) {
			absFileToScrub = strings.TrimPrefix(fileToScrub, currentDirPathAbs)
			absFileToScrub = filepath.Join("/", absFileToScrub)
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

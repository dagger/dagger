package main

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
)

// SecretScrubWriter is a writer that will scrub secretValues before writing to the underlying writer.
// It is safe to write to it concurrently.
type SecretScrubWriter struct {
	mu sync.Mutex
	w  io.Writer

	secretLines []string
}

// Write scrubs secret values from b and replace them with `***`.
func (w *SecretScrubWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	scrubbedBytes := scrubSecretBytes(w.secretLines, b)

	_, err := w.w.Write(scrubbedBytes)
	if err != nil {
		return -1, err
	}

	return len(b), err
}

func scrubSecretBytes(secretValues []string, b []byte) []byte {
	s := string(b)
	ss := strings.Split(s, "\n")

	out := make([]string, 0, len(ss))

	for _, line := range ss {
		for _, secretLine := range secretValues {
			secretLine := strings.TrimSpace(secretLine)
			if secretLine == "" {
				continue
			}
			// FIXME: I think we can do better
			line = strings.ReplaceAll(line, secretLine, "***")
		}
		out = append(out, line)
	}

	s = strings.Join(out, "\n")

	return []byte(s)
}

// NewSecretScrubWriter replaces known secrets by "***".
// The value of the secrets referenced in secretsToScrub are loaded either
// from env or from the fs accessed at currentDirPath.
func NewSecretScrubWriter(w io.Writer, currentDirPath string, fsys fs.FS, env []string, secretsToScrub core.SecretToScrubInfo) (io.Writer, error) {
	secrets := loadSecretsToScrubFromEnv(env, secretsToScrub.Envs)

	fileSecrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretsToScrub.Files)
	if err != nil {
		return nil, fmt.Errorf("could not load secrets from file: %w", err)
	}
	secrets = append(secrets, fileSecrets...)

	secretLines := splitSecretsByLine(secrets)

	return &SecretScrubWriter{
		w:           w,
		secretLines: secretLines,
	}, nil
}

func splitSecretsByLine(secrets []string) []string {
	var secretLines []string
	savedSecrets := map[string]struct{}{}
	for _, secretValue := range secrets {
		lines := strings.Split(secretValue, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}

			// avoid duplicates lines
			_, ok := savedSecrets[line]
			if ok {
				continue
			}
			savedSecrets[line] = struct{}{}

			secretLines = append(secretLines, line)
		}
	}

	// Make the secret lines ordered big first
	// it avoids scrubbing substring of a bigger secret and then
	// not scrubbing the rest of the bigger secret as it does not
	// match anymore.
	sort.SliceStable(secretLines, func(i, j int) bool {
		return len(secretLines[i]) > len(secretLines[j])
	})

	return secretLines
}

// loadSecretsToScrubFromEnv loads secrets value from env if they are in secretsToScrub.
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

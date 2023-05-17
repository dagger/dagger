package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
)

var (
	// scrubString will be used as replacement for found secrets:
	scrubString = []byte("***")
)

// SecretScrubWriter is a writer that will scrub secretValues before writing to the underlying writer.
// It is safe to write to it concurrently.
type SecretScrubWriter struct {
	mu sync.Mutex
	w  io.Writer

	// secrets stores secrets as []byte to avoid overhead when matching them:
	secrets [][]byte
}

// Write scrubs secret values from b and replace them with `***`.
func (w *SecretScrubWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	lines := bytes.Split(b, []byte("\n"))

	var written int
	for i, line := range lines {
		for _, secretBytes := range w.secrets {
			line = bytes.ReplaceAll(line, secretBytes, scrubString)
		}

		n, err := w.w.Write(line)
		if err != nil {
			return -1, err
		}
		written += n
		if i < len(lines)-1 {
			n, err := w.w.Write([]byte("\n"))
			if err != nil {
				return -1, err
			}
			written += n
		}
	}

	return written, nil
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

	secretAsBytes := make([][]byte, 0)
	for _, v := range secretLines {
		secretAsBytes = append(secretAsBytes, []byte(v))
	}

	secretScrubWriter := &SecretScrubWriter{
		w:       w,
		secrets: secretAsBytes,
	}

	return secretScrubWriter, nil
}

func splitSecretsByLine(secrets []string) []string {
	var secretLines []string
	savedSecrets := map[string]struct{}{}
	for _, secretValue := range secrets {
		lines := strings.Split(secretValue, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
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

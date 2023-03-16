package main

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dagger/dagger/core"
)

// SecretScrubWriter is a writer that will scrub secretValues before writing to the underlying writer.
// It is safe to write to it concurrently.
type SecretScrubWriter struct {
	mu sync.Mutex
	w  io.Writer

	secretValues []string
	dangling     []byte
}

// Write scrubs secret values from b and replace them with `***`.
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

func (w *SecretScrubWriter) WriteNew(b []byte) (int, error) {
	// FIXME: TDD the implementation
	return 0, nil
}

func (w *SecretScrubWriter) WriteConcourseWIP(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var buf []byte

	if b != nil {
		buf = w.cacheDangling(b)
		if buf == nil {
			return len(b), nil
		}
	} else {
		if w.dangling == nil || len(w.dangling) == 0 {
			return 0, nil
		}
		buf = w.dangling
	}

	text := string(buf)
	if b != nil {
		i := strings.LastIndex(text, "\n")
		if i >= 0 && i < len(text) {
			// Cache content after the last new-line, and proceed contents
			// before the last new-line.
			w.dangling = []byte(text[i+1:])
			text = text[:i+1]
		} else {
			// No new-line found, then cache the log.
			w.dangling = buf
			return len(b), nil
		}
	}

	for _, secret := range w.secretValues {
		if secret == "" {
			continue
		}
		// FIXME: I think we can do better
		text = strings.ReplaceAll(text, secret, "***")
	}

	_, err := w.w.Write([]byte(text))
	if err != nil {
		return -1, err
	}

	return len(b), err
}

func (w *SecretScrubWriter) cacheDangling(b []byte) []byte {
	text := append(w.dangling, b...)

	checkEncoding, _ := utf8.DecodeLastRune(text)
	if checkEncoding == utf8.RuneError {
		w.dangling = text
		return nil
	}

	w.dangling = nil
	return text
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

	return &SecretScrubWriter{
		w:            w,
		secretValues: secrets,
	}, nil
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

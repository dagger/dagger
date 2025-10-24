package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type GitCredentialSuite struct{}

func TestGitCredential(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GitCredentialSuite{})
}

// TestGitCredentialErrors verifies Git authentication for private modules across different providers
// using host-specific PATs and isolated Git credentials per test.
func (GitCredentialSuite) TestGitCredentialErrors(ctx context.Context, t *testctx.T) {
	// Decode base64-encoded PATs used in CI
	decodeAndTrimPAT := func(encoded string) (string, error) {
		decodedPAT, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("failed to decode PAT: %w", err)
		}
		return strings.TrimSpace(string(decodedPAT)), nil
	}

	// Creates isolated Git credentials per host to allow parallel test execution
	setupGitCredentials := func(host, token, workDir string) []string {
		gitConfigPath := filepath.Join(workDir, ".gitconfig")
		err := os.WriteFile(gitConfigPath, []byte(makeGitCredentials(host, "x-token-auth", token)), 0600)
		require.NoError(t, err)
		return []string{"GIT_CONFIG_GLOBAL=" + gitConfigPath}
	}

	// Wrapper to execute dagger commands running on host with custom env vars
	execWithEnv := func(ctx context.Context, t *testctx.T, workDir string, env []string, args ...string) ([]byte, error) {
		cmd := hostDaggerCommand(ctx, t, workDir, args...)

		// Start with the full environment
		currentEnv := os.Environ()

		// Filter out any Git-related environment variables (case insensitive)
		filteredEnv := make([]string, 0, len(currentEnv))
		for _, e := range currentEnv {
			key := strings.SplitN(e, "=", 2)[0]
			if !strings.Contains(strings.ToLower(key), "git") {
				filteredEnv = append(filteredEnv, e)
			}
		}

		// Add our custom Git environment variables
		gitEnv := []string{
			"GIT_CONFIG_GLOBAL=" + filepath.Join(workDir, ".gitconfig"),
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
		}

		// Combine filtered environment with our Git config and custom env
		cmd.Env = append(append(filteredEnv, gitEnv...), env...)

		output, err := cmd.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%s: %w", string(output), err)
		}
		return output, err
	}

	// Test cases for each Git provider using their respective PATs
	t.Run("github private module", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		pat := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		env := setupGitCredentials("github.com", token, workDir)
		modDir := filepath.Join(workDir, "module")
		err = os.MkdirAll(modDir, 0755)
		require.NoError(t, err)

		out, err := execWithEnv(ctx, t, modDir, env, "-m", "https://github.com/grouville/daggerverse-private.git/zip", "functions")
		require.NoError(t, err)
		require.Contains(t, string(out), "Description")
	})

	t.Run("bitbucket private module", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		pat := "QVRDVFQzeEZmR04wTHhxdWRtNVpjNFFIOE0xc3V0WWxHS2dfcjVTdVJxN0gwOVRrT0ZuUUViUDN4OURodldFQ3V1N1dzaTU5NkdBR2pIWTlhbVMzTEo5VE9OaFVFYlotUW5ZXzFmNnN3alRYRXJhUEJrcnI1NlpMLTdCeG4xMjdPYXpJRlFOMUF3VndLaWJDeW8wMm50U0JtYVA5MlRyUkMtUFN5a2sxQk4weXg1LUhjVXRqNmNVPTIwOEY2RThFCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		env := setupGitCredentials("bitbucket.org", token, workDir)
		modDir := filepath.Join(workDir, "module")
		err = os.MkdirAll(modDir, 0755)
		require.NoError(t, err)

		out, err := execWithEnv(ctx, t, modDir, env, "-m", "https://bitbucket.org/dagger-modules/private-modules-test.git", "functions")
		require.NoError(t, err)
		require.Contains(t, string(out), "Description")
	})

	t.Run("gitlab private module", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		pat := "Z2xwYXQtMGF2bWZBbHBxWENwOXpuazZfZ2JmbTg2TVFwMU9tTjRhV3BqQ3cuMDEuMTIxbWF0b2Rx"
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		env := setupGitCredentials("gitlab.com", token, workDir)
		modDir := filepath.Join(workDir, "module")
		err = os.MkdirAll(modDir, 0755)
		require.NoError(t, err)

		out, err := execWithEnv(ctx, t, modDir, env, "-m", "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", "functions")
		require.NoError(t, err)
		require.Contains(t, string(out), "Description")
	})

	t.Run("private git directory passed to module", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		// Setup Git credentials for GitHub
		pat := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		env := setupGitCredentials("github.com", token, workDir)

		// Create the root directory for the main module
		rootDir := filepath.Join(workDir, "main")
		require.NoError(t, os.MkdirAll(rootDir, 0755))

		// Create the dep directory inside the main module
		depModDir := filepath.Join(rootDir, "dep")
		require.NoError(t, os.MkdirAll(depModDir, 0755))

		// Write the dependent module's code with correct return type
		err = os.WriteFile(filepath.Join(depModDir, "main.go"), []byte(`package main

import (
    "context"
    "dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) ListFiles(ctx context.Context, dir *dagger.Directory) ([]string, error) {
    return dir.Entries(ctx)
}
`), 0644)
		require.NoError(t, err)

		// Initialize the dependent module
		_, err = hostDaggerExec(ctx, t, depModDir, "init", "--source=.", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		// Write the main module's code with matching return type
		err = os.WriteFile(filepath.Join(rootDir, "main.go"), []byte(`package main

import (
    "context"
    "dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, dir *dagger.Directory) ([]string, error) {
    return dag.Dep().ListFiles(ctx, dir)
}
`), 0644)
		require.NoError(t, err)

		// Initialize the main module
		_, err = hostDaggerExec(ctx, t, rootDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// Install the dependent module using relative path
		_, err = hostDaggerExec(ctx, t, rootDir, "install", "./dep")
		require.NoError(t, err)

		// Execute the module with a private Git repository directory
		out, err := execWithEnv(ctx, t, rootDir, env, "call", "fn", "--dir", "https://github.com/grouville/daggerverse-private.git")
		require.NoError(t, err)

		// Verify that we can list files from the private repository
		require.Contains(t, string(out), "zip")
	})

	// Verify authentication failure without credentials
	t.Run("authentication error", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		modDir := filepath.Join(workDir, "module")
		err := os.MkdirAll(modDir, 0755)
		require.NoError(t, err)

		_, err = execWithEnv(ctx, t, modDir, nil, "-m", "https://github.com/grouville/daggerverse-private.git/zip", "functions")
		require.Error(t, err)
		requireErrOut(t, err, "Authentication failed")
	})
}

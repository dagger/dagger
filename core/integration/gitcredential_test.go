package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type GitCredentialSuite struct{}

func TestGitCredential(t *testing.T) {
	testctx.Run(testCtx, t, GitCredentialSuite{}, Middleware()...)
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
		credentialsPath := filepath.Join(workDir, ".git-credentials")

		// Store auth token for specific host
		cred := fmt.Sprintf("https://x-token-auth:%s@%s\n", token, host)
		err := os.WriteFile(credentialsPath, []byte(cred), 0600)
		require.NoError(t, err)

		// Configure Git to use local credentials file for this host
		gitConfig := fmt.Sprintf(`[credential "%s"]
        helper = store --file=%s
`, host, credentialsPath)
		err = os.WriteFile(gitConfigPath, []byte(gitConfig), 0600)
		require.NoError(t, err)

		return []string{"GIT_CONFIG_GLOBAL=" + gitConfigPath}
	}

	// Wrapper to execute dagger commands running on host with custom env vars
	execWithEnv := func(ctx context.Context, t *testctx.T, workDir string, env []string, args ...string) ([]byte, error) {
		cmd := hostDaggerCommand(ctx, t, workDir, args...)
		cmd.Env = append(os.Environ(), env...)
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

		pat := "Z2xwYXQtQXlHQU4zR0xOeEhfM3VSckNzck0K"
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

	// Verify authentication failure without credentials
	t.Run("authentication error", func(ctx context.Context, t *testctx.T) {
		workDir := t.TempDir()

		modDir := filepath.Join(workDir, "module")
		err := os.MkdirAll(modDir, 0755)
		require.NoError(t, err)

		_, err = execWithEnv(ctx, t, modDir, nil, "-m", "https://github.com/grouville/daggerverse-private.git/zip", "functions")
		require.Error(t, err)
		require.ErrorContains(t, err, "Authentication failed")
	})
}

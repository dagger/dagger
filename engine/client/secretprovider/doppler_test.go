package secretprovider

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Stub the doppler CLI: echo the args to a file, then mimic `--plain` output
// (value + one newline) or the CLI's missing-secret failure.
const dopplerStub = `#!/bin/sh
printf '%s\n' "$@" > "$DOPPLER_STUB_ARGS"
for last; do :; done
case "$last" in
  MISSING) echo "Doppler Error: Could not find requested secret: MISSING" >&2; exit 1 ;;
  EMPTY) printf '\n' ;;
  PEM) printf -- '-----BEGIN KEY-----\nabc\n-----END KEY-----\n\n' ;;
  *) printf ' tok en \n' ;;
esac
`

func TestDopplerProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doppler"), []byte(dopplerStub), 0o755))
	argsFile := filepath.Join(dir, "args")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOPPLER_STUB_ARGS", argsFile)

	secret, err := dopplerProvider(t.Context(), "backend/prd/API_KEY")
	require.NoError(t, err)
	require.Equal(t, " tok en ", string(secret))
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, "secrets\nget\n--project=backend\n--config=prd\n--plain\n--silent\n--no-check-version\n--\nAPI_KEY\n", string(args))

	secret, err = dopplerProvider(t.Context(), "backend/prd/EMPTY")
	require.NoError(t, err)
	require.Empty(t, secret)

	secret, err = dopplerProvider(t.Context(), "backend/prd/PEM")
	require.NoError(t, err)
	require.Equal(t, "-----BEGIN KEY-----\nabc\n-----END KEY-----\n", string(secret))

	_, err = dopplerProvider(t.Context(), "backend/prd/MISSING")
	require.ErrorContains(t, err, "Could not find requested secret")

	require.NoError(t, os.Remove(argsFile))
	for _, key := range []string{"", "API_KEY", "backend/API_KEY", "backend//API_KEY", "/prd/API_KEY", "backend/prd/", "backend/prd/a/API_KEY"} {
		_, err := dopplerProvider(t.Context(), key)
		require.ErrorContains(t, err, "expected PROJECT/CONFIG/SECRET", key)
	}
	require.NoFileExists(t, argsFile, "invalid references must not invoke the CLI")
}

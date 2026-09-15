package engineconn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFallbackToLocalCLI(t *testing.T) {
	binName := "dagger"
	if runtime.GOOS == windowsPlatform {
		binName += ".exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)
	require.NoError(t, os.WriteFile(binPath, nil, 0o700))
	t.Setenv("PATH", filepath.Dir(binPath))

	downloadErr := fmt.Errorf("%w: download failed", errCLIReleaseUnavailable)
	var logs bytes.Buffer

	got, err := fallbackToLocalCLI(downloadErr, &logs)

	require.NoError(t, err)
	require.Equal(t, binPath, got)
	require.Contains(t, logs.String(), "version compatibility is not guaranteed")
}

func TestNoFallbackToLocalCLIForOtherErrors(t *testing.T) {
	downloadErr := errors.New("download failed")

	_, err := fallbackToLocalCLI(downloadErr, nil)

	require.ErrorIs(t, err, downloadErr)
}

func TestChecksumMapMarksUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	previousURL := OverrideChecksumsURL
	OverrideChecksumsURL = server.URL
	t.Cleanup(func() { OverrideChecksumsURL = previousURL })

	_, err := (CLIDownloader{Release: true}).checksumMap(context.Background())

	require.ErrorIs(t, err, errCLIReleaseUnavailable)
}

func TestMissingArchiveDoesNotFallback(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	previousURL := OverrideCLIArchiveURL
	OverrideCLIArchiveURL = server.URL + "/archive.tar.gz"
	t.Cleanup(func() { OverrideCLIArchiveURL = previousURL })

	_, err := (CLIDownloader{}).extract(context.Background(), io.Discard)

	require.Error(t, err)
	require.NotErrorIs(t, err, errCLIReleaseUnavailable)
}

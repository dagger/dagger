package vcs

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type resolutionTransport func(*http.Request) (*http.Response, error)

func (f resolutionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRepoDiscoveryKeepsFailureCause(t *testing.T) {
	originalClient := httpClient
	t.Cleanup(func() { httpClient = originalClient })
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		status := http.StatusText(code)
		httpClient = &http.Client{Transport: resolutionTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Status: status, Body: io.NopCloser(strings.NewReader("access unavailable"))}, nil
		})}
		_, err := RepoRootForImportPath("git.example.test/tools", false)
		require.ErrorContains(t, err, status)
		require.ErrorContains(t, err, "git.example.test/tools")
	}
	connectionErr := errors.New("connection refused")
	httpClient = &http.Client{Transport: resolutionTransport(func(*http.Request) (*http.Response, error) {
		return nil, connectionErr
	})}
	_, err := RepoRootForImportPath("git.example.test/tools", false)
	require.ErrorIs(t, err, connectionErr)
}

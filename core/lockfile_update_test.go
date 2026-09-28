package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceLockEntry(t *testing.T) {
	t.Parallel()

	_, err := updateWorkspaceLockEntry(context.Background(), nil, workspace.LookupEntry{
		Namespace: "acme",
		Operation: "resolve",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported lock entry "acme" "resolve"`)
	require.ErrorIs(t, err, errUnsupportedLockEntry)
}

func TestUpdateWorkspaceLockIgnoresUnsupportedEntries(t *testing.T) {
	t.Parallel()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		"acme",
		"resolve",
		[]any{"input"},
		"result",
	))
	require.NoError(t, lock.SetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationGitSHA,
		workspace.LookupInputs(
			[]any{"https://example.com/repo.git", "refs/heads/main"},
			workspace.LookupOption{Name: "futureOption", Value: true},
		),
		"0123456789012345678901234567890123456789",
	))

	require.NoError(t, UpdateWorkspaceLock(context.Background(), nil, lock))
	require.Len(t, lock.Entries(), 2)
}

func TestSelectWorkspaceLockEntries(t *testing.T) {
	t.Parallel()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationOCISHA,
		[]any{"docker.io/library/node:lts-alpine"},
		"sha256:"+strings.Repeat("0", 64),
	))
	require.NoError(t, lock.SetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationGitSHA,
		[]any{"github.com/dagger/dagger", "refs/tags/v1.0.0"},
		"0123456789012345678901234567890123456789",
	))
	require.NoError(t, lock.SetLookup("acme", "resolve", []any{"ignored"}, "value"))

	t.Run("all supported", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, nil)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, []string{
			"github.com/dagger/dagger@refs/tags/v1.0.0",
			"docker.io/library/node:lts-alpine",
		}, []string{
			WorkspaceLockEntrySelector(entries[0]),
			WorkspaceLockEntrySelector(entries[1]),
		})
	})

	t.Run("multiple selectors", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, []string{"node", "github.com/dagger/dagger"})
		require.NoError(t, err)
		require.Len(t, entries, 2)
	})

	t.Run("overlapping selectors", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, []string{"node", "*node*", "node"})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, workspace.LockOperationOCISHA, entries[0].Operation)
	})

	t.Run("printable selector", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, []string{"docker.io/library/node:lts-alpine"})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, workspace.LockOperationOCISHA, entries[0].Operation)
	})

	t.Run("glob crosses path separators", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, []string{"*node*"})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, workspace.LockOperationOCISHA, entries[0].Operation)
	})

	t.Run("OCI shorthand preserves tag", func(t *testing.T) {
		entries, err := SelectWorkspaceLockEntries(lock, []string{"node:lts*"})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, workspace.LockOperationOCISHA, entries[0].Operation)
	})

	t.Run("operation is not a selector", func(t *testing.T) {
		_, err := SelectWorkspaceLockEntries(lock, []string{"oci-sha"})
		require.EqualError(t, err, `lock selector "oci-sha" matched no entries`)
	})

	t.Run("unmatched", func(t *testing.T) {
		_, err := SelectWorkspaceLockEntries(lock, []string{"debian"})
		require.EqualError(t, err, `lock selector "debian" matched no entries`)
	})

	t.Run("invalid pattern", func(t *testing.T) {
		_, err := SelectWorkspaceLockEntries(lock, []string{"["})
		require.ErrorContains(t, err, `invalid lock selector "["`)
	})

	t.Run("invalid pattern after catch-all", func(t *testing.T) {
		_, err := SelectWorkspaceLockEntries(lock, []string{"*", "["})
		require.ErrorContains(t, err, `invalid lock selector "["`)
	})

	t.Run("empty selector after catch-all", func(t *testing.T) {
		_, err := SelectWorkspaceLockEntries(lock, []string{"*", ""})
		require.EqualError(t, err, "lock selector must not be empty")
	})

	t.Run("literal pattern metacharacters", func(t *testing.T) {
		literalLock := workspace.NewLock()
		require.NoError(t, literalLock.SetLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationOCISHA,
			[]any{"registry.example/image[debug]:latest"},
			"sha256:"+strings.Repeat("0", 64),
		))
		require.NoError(t, literalLock.SetLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationOCISHA,
			[]any{"registry.example/other:latest"},
			"sha256:"+strings.Repeat("1", 64),
		))
		selector := "registry.example/image[debug]:latest"
		entries, err := SelectWorkspaceLockEntries(literalLock, []string{selector})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, selector, WorkspaceLockEntrySelector(entries[0]))
	})

	t.Run("git shorthand matches branches and tags", func(t *testing.T) {
		gitLock := workspace.NewLock()
		for _, ref := range []string{"refs/heads/main", "refs/tags/main", "refs/pull/123/main"} {
			require.NoError(t, gitLock.SetLookup(
				workspace.CoreLockNamespace,
				workspace.LockOperationGitSHA,
				[]any{"https://github.com/dagger/dagger.git", ref},
				strings.Repeat("0", 40),
			))
		}

		for _, selector := range []string{"dagger@main", "dagger@m*"} {
			entries, err := SelectWorkspaceLockEntries(gitLock, []string{selector})
			require.NoError(t, err)
			require.Len(t, entries, 2)
			require.ElementsMatch(t, []string{
				"github.com/dagger/dagger@refs/heads/main",
				"github.com/dagger/dagger@refs/tags/main",
			}, []string{
				WorkspaceLockEntrySelector(entries[0]),
				WorkspaceLockEntrySelector(entries[1]),
			})
		}
	})
}

func TestUpdateVanityURLLockEntry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	sourceURL := "https://" + srv.Listener.Addr().String() + "/go"
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationVanityURL,
		[]any{sourceURL},
		"https://github.com/old/repository",
	))

	require.NoError(t, UpdateWorkspaceLock(t.Context(), nil, lock))
	resolved, ok := lock.GetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationVanityURL,
		[]any{sourceURL},
	)
	require.True(t, ok)
	require.Equal(t, "https://github.com/dagger/dagger", resolved)
}

func TestUpdateVanityURLLockEntryValidatesInputs(t *testing.T) {
	for _, inputs := range [][]any{
		nil,
		{42},
		{""},
		{"https://example.com/source", "extra"},
		workspace.LookupInputs(
			[]any{"https://example.com/source"},
			workspace.LookupOption{Name: "other", Value: true},
		),
	} {
		_, err := updateVanityURLLockEntry(t.Context(), workspace.LookupEntry{
			Operation: workspace.LockOperationVanityURL,
			Inputs:    inputs,
		})
		require.Error(t, err)
	}
}

func TestUpdateVanityURLLockEntryPreservesMappingOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		location string
		cancel   bool
	}{
		{name: "server error", status: http.StatusInternalServerError},
		{name: "not a redirect", status: http.StatusOK},
		{name: "missing location", status: http.StatusTemporaryRedirect},
		{name: "invalid location", status: http.StatusTemporaryRedirect, location: "http://example.com/repo?dagger-get=1"},
		{name: "cancelled request", status: http.StatusTemporaryRedirect, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			oldClient := daggerGetClient
			daggerGetClient = srv.Client()
			daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			defer func() { daggerGetClient = oldClient }()

			lock := workspace.NewLock()
			inputs := []any{srv.URL + "/go"}
			previous := "https://github.com/dagger/dagger@v1.2.3"
			require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL, inputs, previous))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			err := UpdateWorkspaceLock(ctx, nil, lock)
			require.ErrorContains(t, err, "no valid redirect received")
			actual, ok := lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL, inputs)
			require.True(t, ok)
			require.Equal(t, previous, actual)
		})
	}
}

func TestSelectedSHAEntry(t *testing.T) {
	t.Parallel()

	t.Run("Git", func(t *testing.T) {
		t.Parallel()

		entry, err := selectedSHAEntry(
			workspace.LookupEntry{
				Operation: workspace.LockOperationGitLatest,
				Inputs: workspace.LookupInputs(
					[]any{"https://example.com/repo.git"},
					workspace.LookupOption{Name: "tagPrefix", Value: "sdk/go"},
					workspace.LookupOption{Name: "version", Value: "v1.2"},
				),
			},
			"refs/tags/sdk/go/v1.2.3",
		)
		require.NoError(t, err)
		require.Equal(t, workspace.LockOperationGitSHA, entry.Operation)
		require.Equal(t, []any{
			"https://example.com/repo.git",
			"refs/tags/sdk/go/v1.2.3",
		}, entry.Inputs)
	})

	t.Run("OCI", func(t *testing.T) {
		t.Parallel()

		entry, err := selectedSHAEntry(
			workspace.LookupEntry{
				Operation: workspace.LockOperationOCILatest,
				Inputs: workspace.LookupInputs(
					[]any{"registry.example/acme/image"},
					workspace.LookupOption{Name: "protocol", Value: "http"},
					workspace.LookupOption{Name: "version", Value: "2"},
				),
			},
			"2.0.0",
		)
		require.NoError(t, err)
		require.Equal(t, workspace.LockOperationOCISHA, entry.Operation)
		require.Equal(t, workspace.LookupInputs(
			[]any{"registry.example/acme/image:2.0.0"},
			workspace.LookupOption{Name: "protocol", Value: "http"},
		), entry.Inputs)
	})
}

func TestUpdateGitLatestLockEntryValidatesInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		inputs  []any
		wantErr string
	}{
		{name: "missing inputs", wantErr: "invalid git-latest inputs"},
		{name: "invalid remote type", inputs: []any{42}, wantErr: "invalid git-latest remote"},
		{name: "empty remote", inputs: []any{""}, wantErr: "invalid git-latest remote"},
		{
			name: "invalid prefix type",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "tagPrefix", Value: 42},
			),
			wantErr: "invalid git-latest tagPrefix",
		},
		{
			name: "empty prefix",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "tagPrefix", Value: ""},
			),
			wantErr: "invalid git-latest tagPrefix",
		},
		{
			name: "invalid version query",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "version", Value: "v1.x"},
			),
			wantErr: `invalid git-latest version v1.x: invalid version query "v1.x"`,
		},
		{
			name: "unknown option",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "channel", Value: "beta"},
			),
			wantErr: "cannot update git-latest: unsupported option \"channel\"; " +
				"upgrade Dagger or remove the option",
		},
		{
			name:    "extra input",
			inputs:  []any{"https://example.com/repo.git", "extra"},
			wantErr: "invalid git-latest inputs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := updateGitLatestLockEntry(
				context.Background(),
				workspace.LookupEntry{Inputs: tc.inputs},
			)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestParseGitLookupInputsRejectsUnknownOption(t *testing.T) {
	t.Parallel()

	_, _, err := parseGitLookupInputs(
		workspace.LockOperationGitSHA,
		workspace.LookupInputs(
			[]any{"https://example.com/repo.git", "refs/heads/main"},
			workspace.LookupOption{Name: "depth", Value: 1},
		),
	)
	require.ErrorContains(t, err,
		`cannot update git-sha: unsupported option "depth"; `+
			`upgrade Dagger or remove the option`,
	)
	require.True(t, errors.Is(err, errUnsupportedLockEntry))
}

func TestParseOCILockInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		inputs    []any
		want      serverresolver.RegistryTransport
		wantError string
	}{
		{
			name:   "default transport",
			inputs: []any{"docker.io/library/alpine:latest"},
		},
		{
			name: "plain HTTP",
			inputs: workspace.LookupInputs(
				[]any{"registry.example/acme/image:1.0.0"},
				workspace.LookupOption{Name: "protocol", Value: "http"},
			),
			want: serverresolver.RegistryTransport{
				Protocol: serverresolver.RegistryProtocolHTTP,
			},
		},
		{
			name: "insecure HTTPS",
			inputs: workspace.LookupInputs(
				[]any{"registry.example/acme/image:1.0.0"},
				workspace.LookupOption{Name: "protocol", Value: "https"},
				workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
			),
			want: serverresolver.RegistryTransport{
				Protocol:              serverresolver.RegistryProtocolHTTPS,
				InsecureSkipTLSVerify: true,
			},
		},
		{name: "missing inputs", wantError: "invalid oci-sha inputs"},
		{
			name:      "too many inputs",
			inputs:    []any{"alpine:latest", "extra"},
			wantError: "invalid oci-sha inputs",
		},
		{name: "invalid ref", inputs: []any{42}, wantError: "invalid oci-sha ref"},
		{name: "empty ref", inputs: []any{""}, wantError: "invalid oci-sha ref"},
		{name: "untagged ref", inputs: []any{"alpine"}, wantError: "invalid oci-sha untagged ref"},
		{
			name: "invalid protocol",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "protocol", Value: "ftp"},
			),
			wantError: "invalid oci-sha registry protocol",
		},
		{
			name: "unknown option",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "other", Value: true},
			),
			wantError: "cannot update oci-sha: unsupported option \"other\"; " +
				"upgrade Dagger or remove the option",
		},
		{
			name: "insecure HTTP",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "protocol", Value: "http"},
				workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
			),
			wantError: "invalid oci-sha registry transport options",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseOCILockInputs(workspace.LockOperationOCISHA, tc.inputs, false)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.inputs[0], got.ref)
			require.Equal(t, tc.want, got.registryTransport)
		})
	}
}

func TestParseOCILatestLockInputs(t *testing.T) {
	t.Parallel()

	inputs := workspace.LookupInputs(
		[]any{"docker.io/library/alpine"},
		workspace.LookupOption{Name: "protocol", Value: "https"},
		workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
		workspace.LookupOption{Name: "version", Value: "v3.20"},
	)
	got, err := parseOCILockInputs(workspace.LockOperationOCILatest, inputs, true)
	require.NoError(t, err)
	require.Equal(t, serverresolver.RegistryTransport{
		Protocol:              serverresolver.RegistryProtocolHTTPS,
		InsecureSkipTLSVerify: true,
	}, got.registryTransport)
	require.Equal(t, "v3.20", got.version)

	_, err = parseOCILockInputs(
		workspace.LockOperationOCILatest,
		[]any{"docker.io/library/alpine:latest"},
		true,
	)
	require.ErrorContains(t, err, "invalid oci-latest tagged ref")

	_, err = parseOCILockInputs(
		workspace.LockOperationOCILatest,
		workspace.LookupInputs(
			[]any{"docker.io/library/alpine"},
			workspace.LookupOption{Name: "channel", Value: "beta"},
		),
		true,
	)
	require.ErrorContains(t, err,
		`cannot update oci-latest: unsupported option "channel"; `+
			`upgrade Dagger or remove the option`,
	)
}

package core

import (
	"context"
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
	)
	got, err := parseOCILockInputs(workspace.LockOperationOCILatest, inputs, true)
	require.NoError(t, err)
	require.Equal(t, serverresolver.RegistryTransport{
		Protocol:              serverresolver.RegistryProtocolHTTPS,
		InsecureSkipTLSVerify: true,
	}, got.registryTransport)

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

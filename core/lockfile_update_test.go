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

func TestUpdateGitLatestLockEntryValidatesInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		inputs  []any
		wantErr string
	}{
		{name: "missing inputs", wantErr: "invalid git.latest inputs"},
		{name: "invalid remote type", inputs: []any{42}, wantErr: "invalid git.latest remote"},
		{name: "empty remote", inputs: []any{""}, wantErr: "invalid git.latest remote"},
		{name: "extra input", inputs: []any{"https://example.com/repo.git", false}, wantErr: "invalid git.latest inputs"},
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

func TestParseContainerFromLockInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		inputs    []any
		want      serverresolver.RegistryTransport
		wantError string
	}{
		{
			name:   "default transport",
			inputs: []any{"docker.io/library/alpine:latest", "linux/amd64"},
		},
		{
			name:   "plain HTTP",
			inputs: []any{"registry.example/acme/image:1.0.0", "linux/arm64", "http"},
			want: serverresolver.RegistryTransport{
				Protocol: serverresolver.RegistryProtocolHTTP,
			},
		},
		{
			name: "insecure HTTPS",
			inputs: []any{
				"registry.example/acme/image:1.0.0",
				"linux/amd64",
				"https",
				"insecureSkipTLSVerify",
			},
			want: serverresolver.RegistryTransport{
				Protocol:              serverresolver.RegistryProtocolHTTPS,
				InsecureSkipTLSVerify: true,
			},
		},
		{name: "missing inputs", wantError: "invalid container.from inputs"},
		{
			name:      "too many inputs",
			inputs:    []any{"alpine:latest", "linux/amd64", "https", "insecureSkipTLSVerify", "extra"},
			wantError: "invalid container.from inputs",
		},
		{name: "invalid ref", inputs: []any{42, "linux/amd64"}, wantError: "invalid container.from ref"},
		{name: "empty ref", inputs: []any{"", "linux/amd64"}, wantError: "invalid container.from ref"},
		{name: "invalid platform", inputs: []any{"alpine:latest", 42}, wantError: "invalid container.from platform"},
		{name: "invalid protocol type", inputs: []any{"alpine:latest", "linux/amd64", 42}, wantError: "invalid container.from registry protocol"},
		{name: "invalid protocol", inputs: []any{"alpine:latest", "linux/amd64", "ftp"}, wantError: "invalid container.from registry protocol"},
		{name: "invalid option", inputs: []any{"alpine:latest", "linux/amd64", "https", "other"}, wantError: "invalid container.from registry transport option"},
		{name: "insecure HTTP", inputs: []any{"alpine:latest", "linux/amd64", "http", "insecureSkipTLSVerify"}, wantError: "invalid container.from registry transport options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseContainerFromLockInputs(lockContainerFromOperation, tc.inputs)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.inputs[0], got.ref)
			require.Equal(t, tc.inputs[1], got.platform)
			require.Equal(t, tc.want, got.registryTransport)
		})
	}
}

func TestContainerFromLatestInputsRequireBareRepository(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"docker.io/library/alpine:latest",
		"docker.io/library/alpine:3.22",
		"docker.io/library/alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		_, err := updateContainerFromLatestLockEntry(
			context.Background(),
			nil,
			workspace.LookupEntry{
				Inputs: []any{ref, "linux/amd64"},
			},
		)
		require.ErrorContains(t, err, "expected an image repository without a tag or digest")
	}
}

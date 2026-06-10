package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSDKResolve(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "full ref with slash passes through",
			input: "github.com/dagger/go-sdk",
			want:  "github.com/dagger/go-sdk",
		},
		{
			name:  "third-party full ref passes through",
			input: "github.com/myorg/forked-go-sdk",
			want:  "github.com/myorg/forked-go-sdk",
		},
		{
			name:  "full ref with version passes through",
			input: "github.com/dagger/go-sdk@v1.2.3",
			want:  "github.com/dagger/go-sdk@v1.2.3",
		},
		{
			name:  "canonical name resolves to repo",
			input: "go-sdk",
			want:  "github.com/dagger/go-sdk",
		},
		{
			name:  "alias resolves to repo",
			input: "go",
			want:  "github.com/dagger/go-sdk",
		},
		{
			name:  "second alias resolves to repo",
			input: "golang",
			want:  "github.com/dagger/go-sdk",
		},
		{
			name:  "python alias",
			input: "py",
			want:  "github.com/dagger/python-sdk",
		},
		{
			name:  "typescript alias",
			input: "ts",
			want:  "github.com/dagger/typescript-sdk",
		},
		{
			name:    "unknown name errors",
			input:   "nonexistent-sdk",
			wantErr: "not found in registry",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sdkResolve(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLoadSDKRegistry(t *testing.T) {
	entries, err := loadSDKRegistry()
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	// Sanity-check: every entry has a name and a repo.
	for _, e := range entries {
		require.NotEmpty(t, e.Name, "entry missing name")
		require.NotEmpty(t, e.Repo, "entry %q missing repo", e.Name)
	}
}

func TestConventionalSDKModuleName(t *testing.T) {
	for _, tt := range []struct {
		ref  string
		want string
	}{
		{"github.com/dagger/go-sdk", "go-sdk"},
		{"github.com/dagger/python-sdk", "python-sdk"},
		{"github.com/dagger/go-sdk@v1.2.3", "go-sdk"},
		{"go-sdk", "go-sdk"},
		{"github.com/myorg/sub/path/go-sdk", "go-sdk"},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			require.Equal(t, tt.want, conventionalSDKModuleName(tt.ref))
		})
	}
}

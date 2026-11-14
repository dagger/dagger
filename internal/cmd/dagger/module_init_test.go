package daggercmd

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
			name:  "repo basename compatibility fallback resolves to repo",
			input: "go-sdk",
			want:  "github.com/dagger/go-sdk",
		},
		{
			name:  "canonical short name resolves to repo",
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
			name:  "dang bundled sdk",
			input: "dang",
			want:  "github.com/dagger/dang-sdk",
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

func TestSDKResolveInstall(t *testing.T) {
	for _, tt := range []struct {
		name            string
		input           string
		wantRef         string
		wantInstallName string
		wantSDKName     string
		wantErr         string
	}{
		{
			name:            "registry name resolves repo, install name, and sdk alias",
			input:           "go",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "dagger-go-sdk",
			wantSDKName:     "go",
		},
		{
			name:            "registry alias resolves repo, install name, and canonical sdk alias",
			input:           "golang",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "dagger-go-sdk",
			wantSDKName:     "go",
		},
		{
			name:            "repo basename compatibility fallback resolves repo, install name, and sdk alias",
			input:           "go-sdk",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "dagger-go-sdk",
			wantSDKName:     "go",
		},
		{
			name:            "dang resolves repo, install name, and sdk alias",
			input:           "dang",
			wantRef:         "github.com/dagger/dang-sdk",
			wantInstallName: "dagger-dang-sdk",
			wantSDKName:     "dang",
		},
		{
			name:    "full ref keeps generic install naming",
			input:   "github.com/acme/custom-go-sdk",
			wantRef: "github.com/acme/custom-go-sdk",
		},
		{
			name:    "full ref with version keeps generic install naming",
			input:   "github.com/dagger/go-sdk@v1.2.3",
			wantRef: "github.com/dagger/go-sdk@v1.2.3",
		},
		{
			name:    "unknown name errors",
			input:   "nonexistent-sdk",
			wantErr: "not found in registry",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gotRef, gotInstallName, gotSDKName, err := sdkResolveInstall(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantRef, gotRef)
			require.Equal(t, tt.wantInstallName, gotInstallName)
			require.Equal(t, tt.wantSDKName, gotSDKName)
		})
	}
}

func TestLoadSDKRegistry(t *testing.T) {
	entries, err := loadSDKRegistry()
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, e := range entries {
		require.NotEmpty(t, e.Name, "entry missing name")
		require.NotContains(t, e.Name, "-sdk", "entry %q should use the user-facing SDK name", e.Name)
		require.NotEmpty(t, e.Description, "entry %q missing description", e.Name)
		require.NotEmpty(t, e.Repo, "entry %q missing repo", e.Name)
	}
}

func TestParseSDKRegistry(t *testing.T) {
	entries, err := parseSDKRegistry([]byte(`[
		{"name": "go", "description": "Official Dagger SDK for Go", "repo": "github.com/dagger/go-sdk", "aliases": ["golang"]}
	]`))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "go", entries[0].Name)
	require.Equal(t, "Official Dagger SDK for Go", entries[0].Description)
	require.Equal(t, []string{"golang"}, entries[0].Aliases)
}

// Conventional SDK name derivation is shared with the engine in core/workspace.

package daggercmd

import (
	"bytes"
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
		wantErr         string
	}{
		{
			name:            "registry name resolves repo and short install name",
			input:           "go",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "go",
		},
		{
			name:            "registry alias resolves repo and canonical short install name",
			input:           "golang",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "go",
		},
		{
			name:            "repo basename compatibility fallback resolves repo and short install name",
			input:           "go-sdk",
			wantRef:         "github.com/dagger/go-sdk",
			wantInstallName: "go",
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
			gotRef, gotInstallName, err := sdkResolveInstall(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantRef, gotRef)
			require.Equal(t, tt.wantInstallName, gotInstallName)
		})
	}
}

func TestLoadSDKRegistry(t *testing.T) {
	entries, err := loadSDKRegistry()
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, e := range entries {
		require.NotEmpty(t, e.Name, "entry missing name")
		require.NotContains(t, e.Name, "-sdk", "entry %q should use the user-facing install name", e.Name)
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

func TestSearchSDKRegistry(t *testing.T) {
	reg := []sdkEntry{
		{Name: "python", Description: "Official Dagger SDK for Python", Repo: "github.com/dagger/python-sdk", Aliases: []string{"py"}},
		{Name: "go", Description: "Official Dagger SDK for Go", Repo: "github.com/dagger/go-sdk", Aliases: []string{"golang"}},
		{Name: "typescript", Description: "Official Dagger SDK for TypeScript", Repo: "github.com/dagger/typescript-sdk", Aliases: []string{"ts"}},
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all sorted by name", "", []string{"go", "python", "typescript"}},
		{"name substring", "type", []string{"typescript"}},
		{"description match", "python", []string{"python"}},
		{"alias match", "golang", []string{"go"}},
		{"repo basename match", "typescript-sdk", []string{"typescript"}},
		{"no match", "nonexistent", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchSDKRegistry(reg, tt.query)
			var names []string
			for _, entry := range got {
				names = append(names, entry.Name)
			}
			require.Equal(t, tt.want, names)
		})
	}
}

func TestPrintSDKSearchResults(t *testing.T) {
	entries := []sdkEntry{
		{Name: "go", Description: "Official Dagger SDK for Go", Repo: "github.com/dagger/go-sdk", Aliases: []string{"golang"}},
		{Name: "java", Description: "Official Dagger SDK for Java", Repo: "github.com/dagger/java-sdk"},
	}

	var buf bytes.Buffer
	require.NoError(t, printSDKSearchResults(&buf, entries))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "ALIASES")
	require.Contains(t, out, "go")
	require.Contains(t, out, "Official Dagger SDK for Go")
	require.Contains(t, out, "golang")
	require.Contains(t, out, "java")
	require.Contains(t, out, "\nRun 'dagger sdk install <NAME>' to install an SDK.\n")
}

// Conventional SDK short-name derivation is now in core/workspace as
// ConventionalSDKShortName (shared with the engine's migration code). Tests
// for it live there.

func TestModuleInitCommandShape(t *testing.T) {
	cmd, _, err := moduleCmd.Find([]string{"init"})
	require.NoError(t, err)
	require.Same(t, moduleInitCmd, cmd)
	require.Equal(t, "init <sdk> <name>", cmd.Use)
	require.Nil(t, cmd.Flags().Lookup("sdk"))
	require.NotNil(t, cmd.PersistentFlags().Lookup("path"))
	require.Contains(t, cmd.Long, "to add more choices")
}

package provenance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", "/"}, {".", "/"}, {"/docs/./intro.md", "/docs/intro.md"}, {"docs//api", "/docs/api"}} {
		got, err := Path(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, input := range []string{"../outside", "docs/../other", "docs\\file"} {
		_, err := Path(input)
		require.Error(t, err)
	}
}

func TestAffectedByPath(t *testing.T) {
	regions := []Selector{{Path: "/docs", Include: []string{"**/*.md"}, Exclude: []string{"generated/**"}, Kind: WorkspaceRead}}
	for _, tc := range []struct {
		path string
		want bool
	}{{"docs", true}, {"/", true}, {"docs/intro.md", true}, {"docs/generated", false}, {"docs/generated/api.md", false}, {"src", false}, {"docs-other", false}, {"docs/possible-directory", true}} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := AffectedByPath(regions, tc.path, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAffectedByFile(t *testing.T) {
	regions := []Selector{{Path: "docs", Include: []string{"**/*.md"}, Exclude: []string{"generated/**"}, Kind: WorkspaceRead}}
	for _, tc := range []struct {
		path string
		want bool
	}{{"docs/intro.md", true}, {"docs/image.png", false}, {"docs/generated/api.md", false}, {"src/main.go", false}} {
		got, err := AffectedByFile(regions, tc.path, nil)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}
func TestKindsAndUnknownInputs(t *testing.T) {
	regions := []Selector{{Path: "/docs", Kind: WorkspaceRead}, {Path: "/", Kind: ModuleSource, Conservative: true}}
	got, err := AffectedByPath(regions, "src/main.go", nil)
	require.NoError(t, err)
	require.True(t, got)
	got, err = AffectedByPath(regions, "src/main.go", []string{WorkspaceRead})
	require.NoError(t, err)
	require.False(t, got)
	_, err = AffectedByPath(regions, "docs", []string{"TYPO"})
	require.Error(t, err)
	_, err = AffectedByPath(regions, "../outside", nil)
	require.Error(t, err)
	got, err = AffectedByPath(nil, "docs", nil)
	require.NoError(t, err)
	require.False(t, got)
	_, err = AffectedByPath([]Selector{{Path: "docs", Include: []string{"["}, Kind: WorkspaceRead}}, "docs", nil)
	require.Error(t, err)
}
func TestNormalizeDoesNotMutate(t *testing.T) {
	input := []Selector{{Path: "/docs", Include: []string{"b", "a", "a"}, Kind: WorkspaceRead}, {Path: "/docs", Include: []string{"a", "b"}, Kind: WorkspaceRead}}
	got := Normalize(input)
	require.Len(t, got, 1)
	require.Equal(t, []string{"a", "b"}, got[0].Include)
	require.Equal(t, []string{"b", "a", "a"}, input[0].Include)
	require.NotNil(t, Normalize(nil))
}

package provenance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", "/"}, {".", "/"}, {"/docs/./intro.md", "/docs/intro.md"}, {"docs//api", "/docs/api"}, {"docs/../other", "/other"}, {"/../other", "/other"}, {`docs\file`, "/docs/file"}} {
		got, err := Path(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, input := range []string{"../outside", "docs/../../outside", "docs\x00/file"} {
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
	require.Len(t, got, 2)
	require.Equal(t, []string{"a", "b"}, got[0].Include)
	require.Equal(t, []string{"b", "a", "a"}, input[0].Include)
	require.NotNil(t, Normalize(nil))
}

func TestFileMatchingUsesCopyFilterRules(t *testing.T) {
	for _, tc := range []struct {
		name, file       string
		include, exclude []string
		want             bool
	}{
		{"new matching file", "docs/new/deep.md", []string{"docs/**/*.md"}, nil, true},
		{"deleted nonmatching file", "docs/deleted.png", []string{"docs/**/*.md"}, nil, false},
		{"literal directory includes descendants", "docs/guide/intro.md", []string{"docs"}, nil, true},
		{"literal exclusion removes descendants", "docs/generated/api.md", []string{"docs"}, []string{"docs/generated"}, false},
		{"exclude reinclude", "docs/generated/keep.md", []string{"docs"}, []string{"docs/generated", "!docs/generated/keep.md"}, true},
		{"include negation", "docs/generated/api.md", []string{"docs", "!docs/generated"}, nil, false},
		{"include reinclude", "docs/generated/keep.md", []string{"docs", "!docs/generated", "docs/generated/keep.md"}, nil, true},
		{"single star parent match", "docs/part/deep/intro.md", []string{"docs/*"}, nil, true},
		{"basename is not global", "docs/intro.md", []string{"*.md"}, nil, false},
		{"brace is literal", "docs/a.md", []string{"docs/{a,b}.md"}, nil, false},
		{"root coordinates", "/docs/../src/main.go", []string{"src/**"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectors := []Selector{{Path: "/", Include: tc.include, Exclude: tc.exclude, Kind: WorkspaceRead}}
			got, err := AffectedByFile(selectors, tc.file, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSubtreeOverlapHonorsFilters(t *testing.T) {
	for _, tc := range []struct {
		name, target     string
		include, exclude []string
		want             bool
	}{
		{"disjoint include prefix", "src", []string{"docs/**/*.md"}, nil, false},
		{"deleted selected subtree", "docs/guides", []string{"docs/**/*.md"}, nil, true},
		{"ancestor of literal input", "docs", []string{"docs/deep/guide.md"}, nil, true},
		{"sibling of literal input", "docs/images", []string{"docs/deep/guide.md"}, nil, false},
		{"literal exclusion", "docs/generated", []string{"docs"}, []string{"docs/generated"}, false},
		{"glob exclusion", "docs/generated", []string{"docs"}, []string{"**/generated/**"}, false},
		{"exclude exception outside subtree", "docs/generated", []string{"docs"}, []string{"docs/generated", "!docs/kept"}, false},
		{"exclude exception inside subtree", "docs/generated", []string{"docs"}, []string{"docs/generated", "!docs/generated/keep.md"}, true},
		{"include negation", "docs/generated", []string{"docs", "!docs/generated"}, nil, false},
		{"include reinclude", "docs/generated", []string{"docs", "!docs/generated", "docs/generated/keep.md"}, nil, true},
		{"do not strip two trailing globs", "docs", []string{"docs"}, []string{"docs/*/**"}, true},
		{"unknown wildcard overlap stays selected", "docs/possible-directory", []string{"docs/**/*.md"}, nil, true},
		{"root query", "/", []string{"docs/deep/guide.md"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AffectedByPath([]Selector{{Path: "/", Include: tc.include, Exclude: tc.exclude, Kind: WorkspaceRead}}, tc.target, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizationPreservesNegationOrder(t *testing.T) {
	selectors := []Selector{{Path: "docs/../", Include: []string{"docs", "!docs/generated", "docs/generated/keep.md"}, Exclude: []string{"**", "!docs/**"}, Kind: WorkspaceRead}}
	normalized := Normalize(selectors)
	require.Equal(t, "/", normalized[0].Path)
	for _, file := range []string{"docs/guide.md", "docs/generated/api.md", "docs/generated/keep.md", "src/main.go"} {
		before, err := AffectedByFile(selectors, file, nil)
		require.NoError(t, err)
		after, err := AffectedByFile(normalized, file, nil)
		require.NoError(t, err)
		require.Equal(t, before, after, file)
	}
	require.Equal(t, []string{"docs", "!docs/generated", "docs/generated/keep.md"}, normalized[0].Include)
	require.Equal(t, []string{"**", "!docs/**"}, normalized[0].Exclude)
}

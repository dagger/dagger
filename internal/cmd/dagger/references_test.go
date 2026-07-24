package daggercmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
)

func TestParseReferenceTokens(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"review @~/foo/bar.txt please", []string{"~/foo/bar.txt"}},
		{"@/etc/hosts and @./rel.go", []string{"/etc/hosts", "./rel.go"}},
		// trailing sentence punctuation is stripped
		{"see @~/notes.", []string{"~/notes"}},
		{"look at @~/a.txt, @~/b.txt;", []string{"~/a.txt", "~/b.txt"}},
		// quoted tokens (quotes stripped; whitespace still delimits words)
		{`open @"foo.txt"`, []string{`foo.txt`}},
		// non-references are ignored
		{"email me@example.com", nil},
		{"a bare @ is ignored", nil},
		{"no references here", nil},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			require.Equal(t, tc.want, parseReferenceTokens(tc.line))
		})
	}
}

func TestReferenceMountRel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Under home → relative to home.
	require.Equal(t, "foo/bar.txt", referenceMountRel(filepath.Join(home, "foo", "bar.txt")))
	require.Equal(t, "proj", referenceMountRel(filepath.Join(home, "proj")))

	// Outside home → basename only.
	require.Equal(t, "hosts", referenceMountRel("/etc/hosts"))
}

func TestExpandReferencePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := expandReferencePath("~/foo/bar.txt")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "foo", "bar.txt"), got)

	got, err = expandReferencePath("~")
	require.NoError(t, err)
	require.Equal(t, home, got)

	got, err = expandReferencePath("/etc/hosts")
	require.NoError(t, err)
	require.Equal(t, "/etc/hosts", got)
}

func TestCompleteReferencePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "apex.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "beta"), 0o755))

	toLabels := func(frag string) []string {
		res := completeReferencePath(frag)
		out := make([]string, 0, len(res))
		for _, it := range res {
			out = append(out, it.Label)
		}
		return out
	}

	// Listing the directory omits dotfiles and marks directories with a slash.
	all := toLabels(dir + "/")
	require.Contains(t, all, "@"+dir+"/alpha.txt")
	require.Contains(t, all, "@"+dir+"/apex.go")
	require.Contains(t, all, "@"+dir+"/beta/")
	require.NotContains(t, all, "@"+dir+"/.hidden")
	require.True(t, sortedStrings(all), "completions should be sorted: %v", all)

	// A prefix narrows results.
	ap := toLabels(dir + "/a")
	require.ElementsMatch(t, []string{"@" + dir + "/alpha.txt", "@" + dir + "/apex.go"}, ap)

	// An explicit dot shows dotfiles.
	dot := toLabels(dir + "/.")
	require.Contains(t, dot, "@"+dir+"/.hidden")

	// A directory completion carries the display slash but the inserted label
	// includes the full @-prefixed path.
	res := completeReferencePath(dir + "/be")
	require.Len(t, res, 1)
	require.Equal(t, "@"+dir+"/beta/", res[0].Label)
	require.Equal(t, "beta/", res[0].DisplayLabel)
}

func TestAutoCompleteReferencePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("x"), 0o644))

	h := newShellCallHandler(nil, &idtui.FrontendMock{})
	require.NoError(t, h.registerCommands())
	h.mode = modePrompt

	input := "please read @" + dir + "/r"
	res := h.AutoComplete(input, len(input))
	require.Equal(t, strings.Index(input, "@"), res.ReplaceFrom)
	require.Len(t, res.Items, 1)
	require.Equal(t, "@"+dir+"/readme.md", res.Items[0].Label)
}

func TestReferenceAnnotation(t *testing.T) {
	out := referenceAnnotation([]referenceInfo{
		{original: "~/foo/bar.txt", mount: ".refs/foo/bar.txt", isDir: false},
		{original: "~/proj", mount: ".refs/proj", isDir: true},
	})
	require.Contains(t, out, "~/foo/bar.txt → .refs/foo/bar.txt")
	require.Contains(t, out, "~/proj (directory) → .refs/proj")
}

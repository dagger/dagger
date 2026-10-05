package gitutil

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRevision(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		base  string
		steps []RevisionStep
	}{
		{expr: "HEAD", base: "HEAD"},
		{expr: "main", base: "main"},
		{expr: "refs/heads/feature/x", base: "refs/heads/feature/x"},
		{expr: "abc1234", base: "abc1234"},
		{expr: "HEAD~", base: "HEAD", steps: []RevisionStep{{'~', 1}}},
		{expr: "HEAD~0", base: "HEAD", steps: []RevisionStep{{'~', 0}}},
		{expr: "HEAD~3", base: "HEAD", steps: []RevisionStep{{'~', 3}}},
		{expr: "HEAD^", base: "HEAD", steps: []RevisionStep{{'^', 1}}},
		{expr: "HEAD^^", base: "HEAD", steps: []RevisionStep{{'^', 1}, {'^', 1}}},
		{expr: "HEAD^0", base: "HEAD", steps: []RevisionStep{{'^', 0}}},
		{expr: "HEAD^2", base: "HEAD", steps: []RevisionStep{{'^', 2}}},
		{expr: "main~2", base: "main", steps: []RevisionStep{{'~', 2}}},
		{expr: "abc1234~2", base: "abc1234", steps: []RevisionStep{{'~', 2}}},
		{expr: "HEAD~2^2~1", base: "HEAD", steps: []RevisionStep{{'~', 2}, {'^', 2}, {'~', 1}}},
		{expr: "refs/tags/v1.0.0~~12^", base: "refs/tags/v1.0.0", steps: []RevisionStep{{'~', 1}, {'~', 12}, {'^', 1}}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			rev, err := ParseRevision(tc.expr)
			require.NoError(t, err)
			require.Equal(t, tc.expr, rev.Expr)
			require.Equal(t, tc.base, rev.Base)
			require.Equal(t, tc.steps, rev.Steps)
			require.Equal(t, len(tc.steps) > 0, rev.HasSuffix())
		})
	}
}

func TestParseRevisionRejectsUnsupportedSyntax(t *testing.T) {
	for expr, reason := range map[string]string{
		"HEAD^{tree}":               "^{...} peeling",
		"v1.0.0^{}":                 "^{...} peeling",
		"main@{1}":                  "@{...}",
		"@{upstream}":               "@{...}",
		"main@{u}~1":                "@{...}",
		"HEAD:go.mod":               ":path",
		":/fix bug":                 ":path",
		"main..feature":             "ranges",
		"a...b":                     "ranges",
		"HEAD^!":                    "^! is not supported",
		"HEAD^@":                    "^@ is not supported",
		"HEAD^-":                    "^- is not supported",
		"^main":                     "exclusions",
		"~1":                        "missing ref name or commit",
		"@":                         "@ as a shorthand",
		"@~1":                       "@ as a shorthand",
		"HEAD~x":                    `unexpected "x" after "HEAD~"`,
		"HEAD~1x":                   `unexpected "x" after "HEAD~1"`,
		"HEAD^2foo":                 `unexpected "foo" after "HEAD^2"`,
		"HEAD~99999999999999999999": "invalid count",
	} {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseRevision(expr)
			require.ErrorIs(t, err, ErrInvalidRevision)
			require.ErrorContains(t, err, reason)
			require.ErrorContains(t, err, "supported forms are")
			require.ErrorContains(t, err, expr, "error must name the expression")
		})
	}
}

func TestRevisionGenerations(t *testing.T) {
	for expr, want := range map[string]int{
		"HEAD":       0,
		"HEAD~0":     0,
		"HEAD^0":     0,
		"HEAD~3":     3,
		"HEAD^2":     1,
		"HEAD~2^2~1": 4,
	} {
		rev, err := ParseRevision(expr)
		require.NoError(t, err)
		require.Equal(t, want, rev.Generations(), expr)
	}
	rev := Revision{Steps: []RevisionStep{{'~', math.MaxInt}, {'~', 1}}}
	require.Equal(t, math.MaxInt, rev.Generations(), "saturates rather than overflowing")
}

// revisionRepo builds this history, with deterministic SHAs:
//
//	root - a - b ------ merge - head   (main)
//	        \          /
//	         side1 - side2              (side)
func revisionRepo(t *testing.T) (shortSHARepo, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := shortSHARepo{t: t, dir: t.TempDir()}
	f.git("init", "-q", "-b", "main")
	commits := map[string]string{}
	commit := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(f.dir, name), []byte(name+"\n"), 0o600))
		f.git("add", name)
		f.git("commit", "-q", "-m", name)
		commits[name] = f.git("rev-parse", "HEAD")
	}
	commit("root")
	commit("a")
	f.git("checkout", "-q", "-b", "side")
	commit("side1")
	commit("side2")
	f.git("checkout", "-q", "main")
	commit("b")
	f.git("merge", "-q", "--no-ff", "-m", "merge", "side")
	commits["merge"] = f.git("rev-parse", "HEAD")
	commit("head")
	f.git("tag", "-a", "-m", "annotated", "v1", commits["merge"])
	commits["v1"] = f.git("rev-parse", "v1")
	return f, commits
}

func TestWalkRevision(t *testing.T) {
	ctx := context.Background()
	f, c := revisionRepo(t)
	cli := NewGitCLI(WithDir(f.dir))

	walk := func(expr, baseSHA string) (string, error) {
		rev, err := ParseRevision(expr)
		require.NoError(t, err)
		return cli.WalkRevision(ctx, rev, baseSHA)
	}

	for _, tc := range []struct {
		expr, base, want string
	}{
		{"HEAD~0", "head", "head"},
		{"HEAD^0", "head", "head"},
		{"HEAD~", "head", "merge"},
		{"HEAD~1", "head", "merge"},
		{"HEAD^", "head", "merge"},
		{"HEAD^^", "head", "b"},
		{"HEAD~3", "head", "a"},
		{"HEAD~4", "head", "root"},
		{"main^2", "merge", "side2"},
		{"main^1", "merge", "b"},
		{"HEAD^^2", "head", "side2"},
		{"HEAD~1^2~1", "head", "side1"},
		{"HEAD^2~2", "merge", "a"},
		{"side~2", "side2", "a"},
		{"v1^2", "v1", "side2"}, // annotated tag object is peeled first
	} {
		t.Run(tc.expr+"@"+tc.base, func(t *testing.T) {
			sha, err := walk(tc.expr, c[tc.base])
			require.NoError(t, err)
			require.Equal(t, c[tc.want], sha)
			// cross-check against git itself (the fixture is not shallow)
			rev, err := ParseRevision(tc.expr)
			require.NoError(t, err)
			require.Equal(t, f.git("rev-parse", c[tc.base]+tc.expr[len(rev.Base):]+"^{commit}"), sha)
		})
	}

	t.Run("abbreviated base", func(t *testing.T) {
		// the schema expands abbreviated SHAs before walking
		rev, err := ParseRevision(c["head"][:7] + "~2")
		require.NoError(t, err)
		base, err := cli.ResolveShortSHA(ctx, rev.Base)
		require.NoError(t, err)
		sha, err := cli.WalkRevision(ctx, rev, base)
		require.NoError(t, err)
		require.Equal(t, c["b"], sha)
	})

	t.Run("walking past the root", func(t *testing.T) {
		_, err := walk("HEAD~5", c["head"])
		require.ErrorContains(t, err, `resolve "HEAD~5": HEAD~4 (`+c["root"]+`) is a root commit, cannot walk 1 more first parent(s) past it`)

		_, err = walk("main~2^", c["a"])
		require.ErrorContains(t, err, `resolve "main~2^": main~1 (`+c["root"]+`) is a root commit`)

		_, err = walk("root^", c["root"])
		require.ErrorContains(t, err, `resolve "root^": root (`+c["root"]+`) has 0 parent(s), cannot select parent 1`)
	})

	t.Run("parent beyond count", func(t *testing.T) {
		_, err := walk("HEAD^2", c["head"])
		require.ErrorContains(t, err, `resolve "HEAD^2": HEAD (`+c["head"]+`) has 1 parent(s), cannot select parent 2`)

		_, err = walk("HEAD~1^3", c["head"])
		require.ErrorContains(t, err, `resolve "HEAD~1^3": HEAD~1 (`+c["merge"]+`) has 2 parent(s), cannot select parent 3`)
	})

	t.Run("missing base commit", func(t *testing.T) {
		_, err := walk("HEAD~1", "0123456789012345678901234567890123456789")
		require.ErrorIs(t, err, ErrRevisionHistoryUnavailable)
		require.ErrorContains(t, err, `resolve "HEAD~1"`)
	})
}

func TestWalkRevisionShallowBoundary(t *testing.T) {
	ctx := context.Background()
	f, c := revisionRepo(t)

	shallow := shortSHARepo{t: t, dir: t.TempDir()}
	shallow.git("clone", "-q", "--depth=2", "--branch=main", "file://"+f.dir, ".")
	// git itself treats the shallow boundary (the merge) as a root commit
	require.Equal(t, c["merge"], shallow.git("rev-list", "--max-parents=0", "HEAD"))

	cli := NewGitCLI(WithDir(shallow.dir))
	rev, err := ParseRevision("HEAD~2")
	require.NoError(t, err)
	sha, err := cli.WalkRevision(ctx, rev, c["head"])
	require.NoError(t, err)
	require.Equal(t, c["b"], sha, "the boundary's real parents are still read")

	for _, expr := range []string{"HEAD~3", "HEAD~1^2~1", "HEAD~10"} {
		rev, err := ParseRevision(expr)
		require.NoError(t, err)
		_, err = cli.WalkRevision(ctx, rev, c["head"])
		require.ErrorIs(t, err, ErrRevisionHistoryUnavailable, expr)
		require.ErrorContains(t, err, `resolve "`+expr+`"`)
	}
}

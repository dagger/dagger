package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltest.WithTracing[*testing.T](
			oteltest.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}

type DaggerCMDSuite struct {
}

func TestDaggerCMD(tt *testing.T) {
	testctx.New(tt, Middleware()...).RunTests(DaggerCMDSuite{})
}

func (DaggerCMDSuite) TestShellAutocomplete(ctx context.Context, t *testctx.T) {
	// each cmdline is a prompt input
	// the contents of the angle brackets are the word we want to complete -
	// everything before the $ sign is already written, and one of the response
	// options should include the contents after the $

	cmdlines := []string{
		// top-level function
		`<con$tainer >`,
		`<$container >`,
		`  <$container >`,
		`<con$tainer > "alpine:latest"`,
		`<con$tainer >| directory`,

		// top-level deps
		`<a$lpine >`,

		// stdlib fallback
		`<dir$ectory >`,
		`directory | <with$-new-file >`,

		// chaining
		`container | <$directory >`,
		`container | <$with-directory >`,
		`container | <dir$ectory >`,
		`container | <with-$directory >`,
		`container | directory "./path" | <f$ile >`,

		// subshells
		`container | with-directory $(<$container >)`,
		`container | with-directory $(<con$tainer >)`,
		`container | with-directory $(container | <$directory >)`,
		`container | with-directory $(container | <dir$ectory >)`,
		`container | with-directory $(container | <$directory >`,
		`container | with-directory $(container | <dir$ectory >`,

		// args
		`container <--$packages >`,
		`container <--$packages > | directory`,
		`container | directory <--$expand >`,

		// .deps builtin
		`.deps | <$alpine >`,
		`.deps | <a$lpine >`,

		// .stdlib builtin
		`.stdlib | <$container >`,
		`.stdlib | <con$tainer >`,
		`.stdlib | container <--$platform >`,
		`.stdlib | container | <dir$ectory >`,

		// .core builtin
		`.core | <con$tainer >`,
		`.core | container <--$platform >`,
		`.core | container | <dir$ectory >`,

		// FIXME: avoid inserting extra spaces
		// `<contain$er> `,
	}

	wd, err := os.Getwd()
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join(wd, "../../modules"))))
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	os.Chdir(dir)
	t.Cleanup(func() {
		os.Chdir(wd)
	})
	t.Setenv("DAGGER_MODULE", "./wolfi")

	client, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	handler := &shellCallHandler{
		dag:   client,
		debug: debug,
	}
	require.NoError(t, handler.RunAll(ctx, nil))
	autoComplete := shellAutoComplete{handler}

	for _, cmdline := range cmdlines {
		t.Run(cmdline, func(ctx context.Context, t *testctx.T) {
			start := strings.IndexRune(cmdline, '<')
			end := strings.IndexRune(cmdline, '>')
			if start == -1 || end == -1 || !(start < end) {
				require.FailNow(t, "invalid cmdline: could not find <expr>")
			}
			inprogress, rest, ok := strings.Cut(cmdline[start+1:end], "$")
			if !ok {
				require.FailNow(t, "invalid cmdline: no token '$' in <expr>")
			}
			expected := strings.TrimSpace(inprogress + rest)

			cmdline := cmdline[:start] + inprogress + cmdline[end+1:]
			cursor := start + len(inprogress)

			_, comp := autoComplete.Do([][]rune{[]rune(cmdline)}, 0, cursor)
			require.NotNil(t, comp)
			require.Equal(t, 1, comp.NumCategories())
			candidates := make([]string, 0, comp.NumEntries(0))
			for i := 0; i < comp.NumEntries(0); i++ {
				entry := comp.Entry(0, i)
				t.Logf("entry %d: %s (%q)", i, entry.Title(), entry.Description())
				candidates = append(candidates, entry.Title())
			}
			require.Contains(t, candidates, expected)
		})
	}
}

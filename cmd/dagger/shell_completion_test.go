package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type DaggerCMDSuite struct{}

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
		// FIXME: should not have any completions
		// `container <$>`,
		`container <$container>`,
		`container | with-directory $(container <$>`,
		// FIXME: should not have any completions
		// `dir=$(container <$>`,
		`dir=$(container <$container>`,

		// flags
		`container <--$packages >`,
		`container <--$packages > | directory`,
		`container | directory <--$expand >`,

		// TODO: These have been hidden. Uncomment when stable, or put them
		// bethind a feature flag so they can be tested even if hidden.

		// // .deps builtin
		// `.deps | <$alpine >`,
		// `.deps | <a$lpine >`,
		//
		// // .stdlib builtin
		// `.stdlib | <$container >`,
		// `.stdlib | <con$tainer >`,
		// `.stdlib | container <--$platform >`,
		// `.stdlib | container | <dir$ectory >`,
		//
		// // .core builtin
		// `.core | <con$tainer >`,
		// `.core | container <--$platform >`,
		// `.core | container | <dir$ectory >`,

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

	handler := newShellCallHandler(client, &idtui.FrontendMock{})
	handler.debug = debugFlag

	require.NoError(t, handler.RunAll(ctx, nil))
	autoComplete := shellAutoComplete{handler}

	for _, cmdline := range cmdlines {
		t.Run(cmdline, func(ctx context.Context, t *testctx.T) {
			start := strings.IndexRune(cmdline, '<')
			end := strings.IndexRune(cmdline, '>')
			if start == -1 || end == -1 || start >= end {
				require.FailNow(t, "invalid cmdline: could not find <expr>")
			}
			inprogress, rest, ok := strings.Cut(cmdline[start+1:end], "$")
			if !ok {
				require.FailNow(t, "invalid cmdline: no token '$' in <expr>")
			}
			expected := strings.TrimSpace(inprogress + rest)

			cmdline := cmdline[:start] + inprogress + cmdline[end+1:]
			cursor := start + len(inprogress)

			result := autoComplete.Complete(cmdline, cursor)
			if expected == "" {
				require.Empty(t, result.Items)
			} else {
				require.NotEmpty(t, result.Items)
				candidates := make([]string, 0, len(result.Items))
				for _, item := range result.Items {
					t.Logf("item: %s", item.Label)
					candidates = append(candidates, item.Label)
				}
				require.Contains(t, candidates, expected)
			}
		})
	}
}

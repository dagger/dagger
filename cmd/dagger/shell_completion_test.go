package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestShellAutocomplete(t *testing.T) {
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
		`<.dep$s >`,
		`<$.deps >`,
		`.deps | <$alpine >`,
		`.deps | <a$lpine >`,

		// .stdlib builtin
		`<.std$lib >`,
		`<$.stdlib >`,
		`.stdlib | <$container >`,
		`.stdlib | <con$tainer >`,
		`.stdlib | container <--$platform >`,
		`.stdlib | container | <dir$ectory >`,

		// .core builtin
		`<.co$re >`,
		`<$.core >`,
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

	ctx := context.TODO()

	client, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	handler := &shellCallHandler{
		dag:    client,
		stdin:  nil,
		stdout: io.Discard,
		stderr: io.Discard,
		debug:  debug,
	}
	require.NoError(t, handler.RunAll(ctx, nil))
	autoComplete := shellAutoComplete{handler}

	for _, cmdline := range cmdlines {
		t.Run(cmdline, func(t *testing.T) {
			start := strings.IndexRune(cmdline, '<')
			end := strings.IndexRune(cmdline, '>')
			if start == -1 || end == -1 || !(start < end) {
				require.FailNow(t, "invalid cmdline: could not find <expr>")
			}
			inprogress, expected, ok := strings.Cut(cmdline[start+1:end], "$")
			if !ok {
				require.FailNow(t, "invalid cmdline: no token '$' in <expr>")
			}

			cmdline := cmdline[:start] + inprogress + cmdline[end+1:]
			cursor := start + len(inprogress)

			results, length := autoComplete.Do([]rune(cmdline), cursor)
			sresults := make([]string, 0, len(results))
			for _, result := range results {
				sresults = append(sresults, string(result))
			}
			require.Contains(t, sresults, expected)
			require.Equal(t, len(inprogress), length)
		})
	}
}

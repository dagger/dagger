package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/dagger/dagger/engine/ripgrep"
	"github.com/stretchr/testify/require"
)

// stubRg is an rg that prints a match line forever, as `rg --json .` over an
// endless tree would, and ignores its arguments.
func stubRg(t *testing.T, filesOnly bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	line := `{"type":"match","data":{"path":{"text":"a.txt"},"lines":{"text":"abc\n"},"line_number":1,"absolute_offset":0,"submatches":[{"match":{"text":"a"},"start":0,"end":1}]}}`
	if filesOnly {
		line = "a.txt"
	}
	path := filepath.Join(t.TempDir(), "rg")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nwhile :; do printf '%s\\n' '"+line+"'; done\n"), 0o755))
	return path
}

// A search matching without end fails fast with a budget error, or returns
// its limit, and either way stops rg: RunRipgrep returning at all shows it
// does, as the stub never exits on its own.
func TestRunRipgrepBudget(t *testing.T) {
	ctx := context.Background()

	t.Run("lines", func(t *testing.T) {
		opts := &SearchOpts{Pattern: "."}
		_, err := opts.RunRipgrep(ctx, exec.Command(stubRg(t, false)), false)
		require.ErrorContains(t, err, "search matched more than 10000 lines")
		require.True(t, ripgrep.IsBudgetError(err))
	})

	t.Run("files", func(t *testing.T) {
		opts := &SearchOpts{Pattern: ".", FilesOnly: true}
		_, err := opts.RunRipgrep(ctx, exec.Command(stubRg(t, true)), false)
		require.ErrorContains(t, err, "search matched more than 10000 files")
	})

	t.Run("limit", func(t *testing.T) {
		limit := 3
		opts := &SearchOpts{Pattern: ".", Limit: &limit}
		results, err := opts.RunRipgrep(ctx, exec.Command(stubRg(t, false)), true)
		require.NoError(t, err)
		require.Len(t, results, 3)
		require.Equal(t, "abc\n", results[0].MatchedLines)
		require.Equal(t, []*SearchSubmatch{{Text: "a", Start: 0, End: 1}}, results[0].Submatches)
	})

	t.Run("a limit above the default budget", func(t *testing.T) {
		limit := ripgrep.DefaultMaxResults + 10
		opts := &SearchOpts{Pattern: ".", Limit: &limit}
		results, err := opts.RunRipgrep(ctx, exec.Command(stubRg(t, false)), false)
		require.NoError(t, err)
		require.Len(t, results, limit)
	})

	t.Run("bytes", func(t *testing.T) {
		limit := 1 << 30
		opts := &SearchOpts{Pattern: ".", Limit: &limit, Budget: &ripgrep.Limits{MaxBytes: 1 << 20}}
		_, err := opts.RunRipgrep(ctx, exec.Command(stubRg(t, false)), false)
		require.ErrorContains(t, err, "search results exceed 1 MiB")
	})
}

func TestRunRipgrepUnbounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	n := ripgrep.DefaultMaxResults + 1
	path := filepath.Join(t.TempDir(), "rg")
	line := `{"type":"match","data":{"path":{"text":"a.txt"},"lines":{"text":"abc\n"},"line_number":1,"absolute_offset":0,"submatches":[]}}`
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt "+strconv.Itoa(n)+" ]; do printf '%s\\n' '"+line+"'; i=$((i+1)); done\n"), 0o755))
	opts := &SearchOpts{Pattern: "abc", Budget: &ripgrep.Limits{}}
	results, err := opts.RunRipgrep(context.Background(), exec.Command(path), false)
	require.NoError(t, err)
	require.Len(t, results, n)
}

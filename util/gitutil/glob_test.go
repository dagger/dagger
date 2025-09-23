package gitutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
)

func TestGlob(t *testing.T) {
	refs := []string{
		"refs/heads/main",
		"refs/tags/v0.18.17",
		"refs/tags/sdk/go/v0.18.17",
		"refs/tags/sdk/python/v0.18.17",
		"refs/tags/v0.18.18",
		"refs/tags/sdk/go/v0.18.18",
		"refs/tags/sdk/python/v0.18.18",
	}

	// create a fake git repo and use git's own globbing to verify our results
	tmpDir := t.TempDir()
	r, err := git.PlainInit(tmpDir, false)
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(tmpDir+"/file.txt", []byte("hello"), 0o644))
	_, err = w.Add(".")
	require.NoError(t, err)
	head, err := w.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "dagger",
			Email: "hello@dagger.io",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
	for _, ref := range refs {
		err := r.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(ref), head))
		require.NoError(t, err)
	}
	err = r.Storer.RemoveReference(plumbing.Master)
	require.NoError(t, err)

	tests := []struct {
		pattern string
		matches []string
	}{
		{
			// full branch
			pattern: "refs/heads/main",
			matches: []string{
				"refs/heads/main",
			},
		},
		{
			// shorter branch
			pattern: "heads/main",
			matches: []string{
				"refs/heads/main",
			},
		},
		{
			// short branch
			pattern: "main",
			matches: []string{
				"refs/heads/main",
			},
		},

		{
			// full tag
			pattern: "refs/tags/v0.18.17",
			matches: []string{
				"refs/tags/v0.18.17",
			},
		},
		{
			// shorter tag
			pattern: "tags/v0.18.17",
			matches: []string{
				"refs/tags/v0.18.17",
			},
		},
		{
			// short tag
			pattern: "v0.18.17",
			matches: []string{
				"refs/tags/v0.18.17",
				"refs/tags/sdk/go/v0.18.17",
				"refs/tags/sdk/python/v0.18.17",
			},
		},

		{
			// wildcard tag
			pattern: "refs/tags/v*",
			matches: []string{
				"refs/tags/v0.18.17",
				"refs/tags/v0.18.18",
			},
		},
		{
			pattern: "refs/tags/sdk/go/*",
			matches: []string{
				"refs/tags/sdk/go/v0.18.17",
				"refs/tags/sdk/go/v0.18.18",
			},
		},
		{
			pattern: "refs/tags/sdk/*/v0.18.17",
			matches: []string{
				"refs/tags/sdk/go/v0.18.17",
				"refs/tags/sdk/python/v0.18.17",
			},
		},

		{
			// anything
			pattern: "refs/tags/sdk/?ython/v0.18.1?",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
				"refs/tags/sdk/python/v0.18.18",
			},
		},

		{
			// wildcard in middle
			pattern: "refs/tags/*/v0.18.17",
			matches: []string{
				"refs/tags/sdk/go/v0.18.17",
				"refs/tags/sdk/python/v0.18.17",
			},
		},
		{
			// wildcard in component
			pattern: "refs/tags/sdk/pyt*",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
				"refs/tags/sdk/python/v0.18.18",
			},
		},
		{
			// wildcard in component
			pattern: "refs/ta*/v0.18.17",
			matches: []string{
				"refs/tags/v0.18.17",
				"refs/tags/sdk/go/v0.18.17",
				"refs/tags/sdk/python/v0.18.17",
			},
		},

		{
			// class
			pattern: "refs/tags/sdk/python/v0.18.1[78]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
				"refs/tags/sdk/python/v0.18.18",
			},
		},
		{
			// class range
			pattern: "refs/tags/sdk/python/v0.18.[0-9][7-7]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
			},
		},
		{
			// class negation
			pattern: "refs/tags/sdk/python/v0.18.1[!7]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.18",
			},
		},
		{
			// alternative class negation
			pattern: "refs/tags/sdk/python/v0.18.1[^7]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.18",
			},
		},

		{
			// character class
			pattern: "refs/tags/sdk/pytho[[:alpha:]]/v0.18.17",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
			},
		},
		{
			// character class
			pattern: "refs/tags/sdk/python/v0.18.[0-9][[:digit:]]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
				"refs/tags/sdk/python/v0.18.18",
			},
		},
		{
			// inverse character class
			pattern: "refs/tags/sdk/python/v0.18.[0-9][![:alpha:]]",
			matches: []string{
				"refs/tags/sdk/python/v0.18.17",
				"refs/tags/sdk/python/v0.18.18",
			},
		},
	}

	for _, test := range tests {
		patterns := []string{test.pattern}
		if double := strings.ReplaceAll(test.pattern, "*", "**"); double != test.pattern {
			patterns = append(patterns, double)
		}
		for _, pattern := range patterns {
			// first verify using our own glob implementation
			t.Run(pattern, func(t *testing.T) {
				var got []string
				for _, ref := range refs {
					match, err := gitTailMatch(pattern, ref)
					require.NoError(t, err)
					if match {
						got = append(got, ref)
					}
				}
				require.ElementsMatch(t, test.matches, got)
			})

			// also verify using git itself (sanity check)
			t.Run(pattern+" (git)", func(t *testing.T) {
				cmd := exec.Command("git", "ls-remote", "file://"+tmpDir)
				if pattern != "" {
					cmd.Args = append(cmd.Args, "--", pattern)
				}
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "git ls-remote failed: %s", out)

				var got []string
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				for _, line := range lines {
					_, r, ok := strings.Cut(line, "\t")
					if !ok {
						continue
					}
					got = append(got, r)
				}
				require.ElementsMatch(t, test.matches, got)
			})
		}
	}
}

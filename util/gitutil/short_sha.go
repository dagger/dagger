package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

var (
	// ErrShortSHANotFound reports that no locally available commit matches an
	// abbreviated commit SHA.
	ErrShortSHANotFound = errors.New("no commit matches short SHA")
	// ErrShortSHAAmbiguous reports that an abbreviated commit SHA matches
	// more than one commit.
	ErrShortSHAAmbiguous = errors.New("ambiguous short SHA")
)

// ResolveShortSHA expands an abbreviated commit SHA (a lowercase hex prefix,
// per IsCommitSHAPrefix) to the full SHA of the single matching commit in the
// repository's object database, the way `git rev-parse` resolves an
// abbreviated object name in a commit-ish context.
//
// Objects that are not commit-ish are ignored; an annotated tag matching the
// prefix is peeled to the commit it points at. A prefix matching more than
// one commit returns ErrShortSHAAmbiguous, reporting how many commits
// matched. Resolution only sees locally available objects: it never contacts
// a remote, so a prefix of a commit that was never fetched returns
// ErrShortSHANotFound.
func (cli *GitCLI) ResolveShortSHA(ctx context.Context, prefix string) (string, error) {
	if !IsCommitSHAPrefix(prefix) {
		return "", fmt.Errorf("invalid short commit SHA %q (expected 4-40 hex characters)", prefix)
	}
	out, err := cli.Run(ctx, "rev-parse", "--disambiguate="+prefix)
	if err != nil {
		return "", fmt.Errorf("disambiguate short SHA %q: %w", prefix, err)
	}
	seen := make(map[string]struct{})
	var commits []string
	for _, candidate := range bytes.Fields(out) {
		// Keep only commit-ish matches: peel annotated tags to the commit
		// they point at, and skip blobs and trees entirely.
		peeled, err := cli.New(WithIgnoreError()).Run(ctx, "rev-parse", "--verify", "--quiet", string(candidate)+"^{commit}")
		if err != nil {
			return "", err
		}
		sha := string(bytes.TrimSpace(peeled))
		if !IsCommitSHA(sha) {
			continue
		}
		if _, ok := seen[sha]; ok {
			// e.g. an annotated tag and its target commit both match the
			// prefix: they name the same commit, so this is not ambiguous
			continue
		}
		seen[sha] = struct{}{}
		commits = append(commits, sha)
	}
	switch len(commits) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrShortSHANotFound, prefix)
	case 1:
		return commits[0], nil
	default:
		return "", fmt.Errorf("%w: %q matches %d commits (%s, %s, ...)", ErrShortSHAAmbiguous, prefix, len(commits), commits[0], commits[1])
	}
}

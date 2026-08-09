package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// ErrNothingToCommit is returned when the changeset handed to
// WorkspaceCommitChangeset contains no change that git would record.
var ErrNothingToCommit = errors.New("nothing to commit")

// WorkspaceCommitOpts describes the commit WorkspaceCommitChangeset creates.
// Every field that feeds the commit object is supplied by the caller, so the
// resulting commit hash is a pure function of the repository tree, the
// changeset, and these options.
type WorkspaceCommitOpts struct {
	// Message is the commit message.
	Message string
	// Date is the RFC3339 author *and* committer date. Required: a commit that
	// read the wall clock would not be reproducible.
	Date string
	// AuthorName and AuthorEmail are the author *and* committer identity.
	AuthorName  string
	AuthorEmail string
}

// WorkspaceCommitChangeset stages a commit inside a scratch copy of repoDir
// (which must contain a real .git directory) and returns the resulting
// repository tree. The changeset's content is applied to the work tree and only
// the paths it touches are added to the index, so anything else left dirty in
// the work tree stays uncommitted.
func WorkspaceCommitChangeset(
	ctx context.Context,
	repoDir dagql.ObjectResult[*Directory],
	scoped *Changeset,
	opts WorkspaceCommitOpts,
) (*Directory, error) {
	if opts.Date == "" {
		return nil, fmt.Errorf("commit date is required")
	}
	if opts.AuthorName == "" {
		opts.AuthorName = "Dagger"
	}
	if opts.AuthorEmail == "" {
		opts.AuthorEmail = "dagger@localhost"
	}

	content, err := scoped.content(ctx)
	if err != nil {
		return nil, fmt.Errorf("changeset content: %w", err)
	}
	stagePaths := commitStagePaths(content.paths)
	if len(stagePaths) == 0 {
		return nil, ErrNothingToCommit
	}

	env := []string{
		"GIT_AUTHOR_NAME=" + opts.AuthorName,
		"GIT_AUTHOR_EMAIL=" + opts.AuthorEmail,
		"GIT_COMMITTER_NAME=" + opts.AuthorName,
		"GIT_COMMITTER_EMAIL=" + opts.AuthorEmail,
		"GIT_AUTHOR_DATE=" + opts.Date,
		"GIT_COMMITTER_DATE=" + opts.Date,
	}

	return withGitMergeWorkspace(ctx, repoDir, "Workspace.withCommit", func(ws *gitMergeWorkspace) error {
		if _, err := os.Stat(filepath.Join(ws.workDir, ".git")); err != nil {
			return fmt.Errorf("workspace commit requires a git repository at the workspace root: %w", err)
		}
		if err := ws.applyContent(ctx, content); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		// Stage only the scoped paths. The work tree may legitimately carry
		// other uncommitted changes — everything outside this commit's scope —
		// and a bare `git add -A` would sweep them in.
		for _, batch := range batchPathSpecs(stagePaths) {
			args := append([]string{"add", "-A", "--"}, batch...)
			if _, err := runGitEnv(ctx, ws.workDir, env, args...); err != nil {
				return err
			}
		}
		staged, err := runGitEnv(ctx, ws.workDir, env, "diff", "--cached", "--name-only")
		if err != nil {
			return err
		}
		if strings.TrimSpace(staged) == "" {
			return ErrNothingToCommit
		}
		if _, err := runGitEnv(ctx, ws.workDir, env,
			"-c", "commit.gpgsign=false",
			"commit", "--no-verify", "--no-gpg-sign", "-m", opts.Message,
		); err != nil {
			return err
		}
		return normalizeGitDirAfterCommit(ctx, ws.workDir)
	})
}

// WorkspaceRepoHeadSHA resolves HEAD in a mounted copy of a repository tree.
func WorkspaceRepoHeadSHA(ctx context.Context, repoDir dagql.ObjectResult[*Directory]) (string, error) {
	var sha string
	_, err := withGitMergeWorkspace(ctx, repoDir, "Workspace git head", func(ws *gitMergeWorkspace) error {
		out, err := runGitEnv(ctx, ws.workDir, nil, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		sha = strings.TrimSpace(out)
		return nil
	})
	if err != nil {
		return "", err
	}
	return sha, nil
}

// WorkspaceRepoContainsCommits reports, for each hash, whether the repository
// tree's HEAD already has that commit in its history. Every hash is probed
// inside ONE mount, because mounting is the expensive part and the probes
// themselves are a single `git merge-base --is-ancestor` each.
//
// An unknown or unreadable hash reads as absent, which is the safe answer: the
// caller falls through to content-level classification rather than silently
// skipping work it cannot prove is already here.
func WorkspaceRepoContainsCommits(
	ctx context.Context,
	repoDir dagql.ObjectResult[*Directory],
	shas []string,
) (map[string]bool, error) {
	contains := make(map[string]bool, len(shas))
	if len(shas) == 0 {
		return contains, nil
	}
	_, err := withGitMergeWorkspace(ctx, repoDir, "Workspace repo contains commits", func(ws *gitMergeWorkspace) error {
		for _, sha := range shas {
			if sha == "" {
				continue
			}
			if _, done := contains[sha]; done {
				continue
			}
			_, err := runGitEnv(ctx, ws.workDir, nil, "merge-base", "--is-ancestor", sha, "HEAD")
			contains[sha] = err == nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return contains, nil
}

// WorkspaceStagedCommitsRef is the ref a staged-commit bundle records for its
// tip. Bundles can only carry commits under a ref name, and the client fetches
// that name back out, so both ends agree on this one.
const WorkspaceStagedCommitsRef = "refs/dagger/staged-commits"

// WorkspaceStagedCommitsBundle packs the commits between baseSHA (exclusive)
// and targetSHA (inclusive) from a staged repository tree into a git bundle,
// returning its bytes. The bundle is how staged commits travel to the user's
// checkout: the client's own git fetches it and fast-forwards, which is the
// only way to update a checkout git itself understands — reflogs, index, work
// tree, and worktree/submodule layouts included.
func WorkspaceStagedCommitsBundle(
	ctx context.Context,
	repoDir dagql.ObjectResult[*Directory],
	targetSHA string,
	baseSHA string,
) ([]byte, error) {
	if targetSHA == "" {
		return nil, fmt.Errorf("bundle target commit is required")
	}
	tmpDir, err := os.MkdirTemp("", "dagger-workspace-bundle")
	if err != nil {
		return nil, fmt.Errorf("create bundle temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bundlePath := filepath.Join(tmpDir, "staged.bundle")

	_, err = withGitMergeWorkspace(ctx, repoDir, "Workspace staged commits bundle", func(ws *gitMergeWorkspace) error {
		// The tip has to be reachable through a ref for the bundle to record
		// it under a name the client can fetch; the staged branch ref is not
		// necessarily the one HEAD points at in every layout, so name it
		// explicitly.
		if _, err := runGitEnv(ctx, ws.workDir, nil, "update-ref", WorkspaceStagedCommitsRef, targetSHA); err != nil {
			return err
		}
		args := []string{"bundle", "create", bundlePath, WorkspaceStagedCommitsRef}
		if baseSHA != "" {
			// Exactly the staged commits: everything the checkout already has
			// is a prerequisite, not payload.
			args = append(args, "--not", baseSHA)
		}
		if _, err := runGitEnv(ctx, ws.workDir, nil, args...); err != nil {
			return err
		}
		heads, err := runGitEnv(ctx, ws.workDir, nil, "bundle", "list-heads", bundlePath)
		if err != nil {
			return err
		}
		if !strings.Contains(heads, WorkspaceStagedCommitsRef) {
			return fmt.Errorf("git bundle does not record %s: %s", WorkspaceStagedCommitsRef, strings.TrimSpace(heads))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return os.ReadFile(bundlePath)
}

// normalizeGitDirAfterCommit strips the parts of .git that a commit writes with
// nondeterministic content, so two identical commits produce byte-identical
// repository trees:
//
//   - the index records each file's stat data (inode, ctime, device), none of
//     which survives snapshotting; `git read-tree HEAD` rewrites it from the
//     committed tree with that stat data zeroed. It is not simply deleted:
//     without an index, a later `git add -A -- <path>` would build a tree
//     containing only that path.
//   - COMMIT_EDITMSG and the reflogs are scratch state git does not need to
//     read back, and reflogs additionally accumulate per-operation entries.
func normalizeGitDirAfterCommit(ctx context.Context, workDir string) error {
	if _, err := runGitEnv(ctx, workDir, nil, "read-tree", "HEAD"); err != nil {
		return fmt.Errorf("normalize git index: %w", err)
	}
	gitDir := filepath.Join(workDir, ".git")
	for _, p := range []string{"COMMIT_EDITMSG", "logs"} {
		if err := os.RemoveAll(filepath.Join(gitDir, p)); err != nil {
			return fmt.Errorf("normalize .git/%s: %w", p, err)
		}
	}
	return nil
}

// commitStagePaths flattens a changeset's paths into the pathspecs to hand to
// `git add`. Directory entries carry a trailing slash, which git accepts but
// which is dropped for consistency; renames need no special handling since
// ChangesetPaths records both their old and new names.
func commitStagePaths(paths *ChangesetPaths) []string {
	all := slices.Concat(paths.Added, paths.Modified, paths.AllRemoved)
	seen := make(map[string]struct{}, len(all))
	out := make([]string, 0, len(all))
	for _, p := range all {
		p = strings.TrimSuffix(p, "/")
		if p == "" || p == "." {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// batchPathSpecs splits pathspecs into groups that fit in a single argv.
func batchPathSpecs(specs []string) [][]string {
	var (
		batches [][]string
		current []string
		size    int
	)
	for _, spec := range specs {
		if len(current) > 0 && size+len(spec)+1 > maxGitPathSpecBytes {
			batches = append(batches, current)
			current, size = nil, 0
		}
		current = append(current, spec)
		size += len(spec) + 1
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

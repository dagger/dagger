package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql"
)

// ErrNothingToCommit is returned when the changeset handed to
// GitCommitChangeset contains no change that git would record.
var ErrNothingToCommit = errors.New("nothing to commit")

// GitCommitOpts describes the commit GitCommitChangeset creates.
// Every field that feeds the commit object is supplied by the caller, so the
// resulting commit hash is a pure function of the repository tree, the
// changeset, and these options.
type GitCommitOpts struct {
	// Message is the commit message.
	Message string
	// Date is the RFC3339 author date and default committer date. A commit that
	// read the wall clock would not be reproducible.
	Date string
	// AuthorName and AuthorEmail also supply defaults for committer identity.
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	CommitterDate  string
	AllowEmpty     bool
	// Signoff adds a Signed-off-by trailer using the author identity.
	Signoff bool
}

// GitCommitChangeset records already-reconciled changes in a scratch copy of
// repoDir, which must contain a clean checkout and a real .git directory. Its
// caller handles three-way merging and path selection before this operation.
func GitCommitChangeset(
	ctx context.Context,
	repoDir dagql.ObjectResult[*Directory],
	scoped *Changeset,
	opts GitCommitOpts,
) (*Directory, error) {
	if _, err := time.Parse(time.RFC3339, opts.Date); err != nil {
		return nil, fmt.Errorf("commit date must be RFC3339: %w", err)
	}
	if strings.TrimSpace(opts.Message) == "" || strings.ContainsRune(opts.Message, 0) {
		return nil, fmt.Errorf("commit message must be nonempty and contain no NUL")
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
	if len(stagePaths) == 0 && !opts.AllowEmpty {
		return nil, ErrNothingToCommit
	}

	if opts.CommitterName == "" {
		opts.CommitterName = opts.AuthorName
	}
	if opts.CommitterEmail == "" {
		opts.CommitterEmail = opts.AuthorEmail
	}
	if opts.CommitterDate == "" {
		opts.CommitterDate = opts.Date
	}
	env := []string{
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_AUTHOR_NAME=" + opts.AuthorName,
		"GIT_AUTHOR_EMAIL=" + opts.AuthorEmail,
		"GIT_COMMITTER_NAME=" + opts.CommitterName,
		"GIT_COMMITTER_EMAIL=" + opts.CommitterEmail,
		"GIT_AUTHOR_DATE=" + opts.Date,
		"GIT_COMMITTER_DATE=" + opts.CommitterDate,
	}

	return withGitMergeWorkspace(ctx, repoDir, "GitRef.withCommit", func(ws *gitMergeWorkspace) error {
		if _, err := os.Stat(filepath.Join(ws.workDir, ".git")); err != nil {
			return fmt.Errorf("commit requires a git repository at the directory root: %w", err)
		}
		if err := ws.applyContent(ctx, content); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		// Stage only the scoped paths. The work tree may legitimately carry
		// other uncommitted changes — everything outside this commit's scope —
		// and a bare `git add -A` would sweep them in.
		for _, batch := range batchPathSpecs(stagePaths) {
			args := append([]string{"add", "-A", "--"}, batch...)
			if _, err := runWorkspaceCommitGit(ctx, ws.workDir, env, args...); err != nil {
				return err
			}
		}
		staged, err := runWorkspaceCommitGit(ctx, ws.workDir, env, "diff", "--cached", "--name-only")
		if err != nil {
			return err
		}
		if strings.TrimSpace(staged) == "" && !opts.AllowEmpty {
			return ErrNothingToCommit
		}
		args := []string{
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null",
			"commit", "--allow-empty", "--no-verify", "--no-gpg-sign", "--cleanup=verbatim", "-m", opts.Message,
		}
		if opts.Signoff {
			// git commit --signoff uses the committer, which may differ from
			// the author. Supply the author trailer explicitly instead.
			args = append(args, "--trailer", fmt.Sprintf("Signed-off-by: %s <%s>", opts.AuthorName, opts.AuthorEmail))
		}
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, env, args...); err != nil {
			return err
		}
		return normalizeGitDirAfterCommit(ctx, ws.workDir)
	})
}

func normalizeGitDirAfterCommit(ctx context.Context, workDir string) error {
	if _, err := runWorkspaceCommitGit(ctx, workDir, nil, "read-tree", "HEAD"); err != nil {
		return fmt.Errorf("normalize git index: %w", err)
	}
	gitDir := filepath.Join(workDir, ".git")
	// ORIG_HEAD is written by reset; removing it keeps the tree reproducible.
	for _, p := range []string{"COMMIT_EDITMSG", "ORIG_HEAD", "logs"} {
		if err := os.RemoveAll(filepath.Join(gitDir, p)); err != nil {
			return fmt.Errorf("normalize .git/%s: %w", p, err)
		}
	}
	return nil
}

// commitStagePaths flattens a changeset's paths into the pathspecs to hand to
// `git add`. Directory entries are omitted: Git records files, not empty
// directories, and AllRemoved includes every deleted file. ChangesetPaths
// records both sides of renames.
func commitStagePaths(paths *ChangesetPaths) []string {
	all := slices.Concat(paths.Added, paths.Modified, paths.AllRemoved)
	seen := make(map[string]struct{}, len(all))
	out := make([]string, 0, len(all))
	for _, p := range all {
		if strings.HasSuffix(p, "/") {
			continue
		}
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

// runWorkspaceCommitGit layers explicit commit inputs over the hermetic Git environment.
func runWorkspaceCommitGit(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	cmd := gitCmd(ctx, dir, args...)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

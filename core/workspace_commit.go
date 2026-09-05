package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	// Paths are literal workspace-root-relative paths; empty selects everything.
	Paths []string
}

// WorkspaceCommitChangeset stages a commit inside a scratch copy of repoDir
// (which must contain a real .git directory) and returns the resulting
// repository tree. The changeset's content is applied to the work tree and only
// the selected paths it touches are added to the index. The complete working
// tree stays intact so unselected changes can remain as an overlay on new HEAD.
func WorkspaceCommitChangeset(
	ctx context.Context,
	repoDir dagql.ObjectResult[*Directory],
	scoped *Changeset,
	opts WorkspaceCommitOpts,
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
	for newPath, oldPath := range content.paths.Renamed {
		if commitPathSelected(newPath, opts.Paths) != commitPathSelected(oldPath, opts.Paths) {
			return nil, fmt.Errorf("paths would split the rename %q -> %q; include both paths or neither", oldPath, newPath)
		}
	}
	stagePaths := slices.DeleteFunc(commitStagePaths(content.paths), func(p string) bool {
		return !commitPathSelected(p, opts.Paths)
	})
	if len(stagePaths) == 0 {
		return nil, ErrNothingToCommit
	}

	env := []string{
		"GIT_LITERAL_PATHSPECS=1",
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
			if _, err := runWorkspaceCommitGit(ctx, ws.workDir, env, args...); err != nil {
				return err
			}
		}
		staged, err := runWorkspaceCommitGit(ctx, ws.workDir, env, "diff", "--cached", "--name-only")
		if err != nil {
			return err
		}
		if strings.TrimSpace(staged) == "" {
			return ErrNothingToCommit
		}
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, env,
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null",
			"commit", "--no-verify", "--no-gpg-sign", "--cleanup=verbatim", "-m", opts.Message,
		); err != nil {
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
	for _, p := range []string{"COMMIT_EDITMSG", "logs"} {
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

func commitPathSelected(p string, scopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	p = path.Clean(p)
	for _, scope := range scopes {
		if scope == "." || p == scope || strings.HasPrefix(p, scope+"/") {
			return true
		}
	}
	return false
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

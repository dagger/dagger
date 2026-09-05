package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

const MaxWorkspacePullCommits = 1000
const workspacePullSelectionWindow = 10000
const WorkspacePullTimeout = 2 * time.Minute
const workspacePullOutputLimit = 16 << 20

type WorkspacePullOpts struct {
	Commits                       []string
	MaxCommits                    int
	CommitterName, CommitterEmail string
}

func (opts WorkspacePullOpts) Validate() error {
	if opts.MaxCommits < 1 || opts.MaxCommits > MaxWorkspacePullCommits {
		return fmt.Errorf("maxCommits must be between 1 and %d", MaxWorkspacePullCommits)
	}
	if len(opts.Commits) > opts.MaxCommits {
		return fmt.Errorf("selected commits exceed maxCommits (%d)", opts.MaxCommits)
	}
	seen := map[string]bool{}
	for _, sha := range opts.Commits {
		if !workspacePullSHA(sha) {
			return fmt.Errorf("commits must contain full lowercase commit hashes, got %q", sha)
		}
		if seen[sha] {
			return fmt.Errorf("duplicate selected commit %s", sha)
		}
		seen[sha] = true
	}
	return nil
}

func workspacePullSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil && sha == strings.ToLower(sha)
}

type WorkspacePullPick struct {
	SHA           string
	Status        WorkspaceCommitPickStatus
	Reason        WorkspaceCommitPickReason
	ConflictPaths []string
}

// WorkspacePullCommits uses the same speculative fold for planning and apply.
// All Git writes are confined to a scratch snapshot; even a late conflict
// leaves both input workspaces intact. Only source HEAD is fetched, not its
// working tree or overlay. The result is a clean committed repository.
func WorkspacePullCommits(ctx context.Context, base dagql.ObjectResult[*Directory], source *GitRef, dirty *Changeset, opts WorkspacePullOpts, apply bool) (*Directory, []WorkspacePullPick, error) {
	if err := opts.Validate(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, WorkspacePullTimeout)
	defer cancel()
	paths, err := dirty.ComputePaths(ctx)
	if err != nil {
		return nil, nil, err
	}
	dirtyPaths := pullDirtyPaths(paths)
	var picks []WorkspacePullPick
	dir, err := withGitMergeWorkspace(ctx, base, "Workspace pull commits", func(ws *gitMergeWorkspace) error {
		err := source.Repo.Self().Backend.mount(ctx, 0, false, []GitRefBackend{source.Backend}, func(git *gitutil.GitCLI) error {
			url, err := git.URL(ctx)
			if err != nil {
				return err
			}
			_, err = runWorkspacePullGit(ctx, ws.workDir, nil, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", url, source.Ref.SHA)
			return err
		})
		if err != nil {
			return fmt.Errorf("fetch source commits: %w", err)
		}
		picks, err = foldWorkspacePull(ctx, ws.workDir, source.Ref.SHA, dirtyPaths, opts)
		if err != nil {
			return err
		}
		if apply {
			var conflicts []error
			for _, pick := range picks {
				if pick.Status == WorkspaceCommitConflict {
					conflicts = append(conflicts, fmt.Errorf("commit %s: %s conflict on %s", pick.SHA, pick.Reason, strings.Join(pick.ConflictPaths, ", ")))
				}
			}
			if len(conflicts) > 0 {
				return fmt.Errorf("cannot pull commits: %w", errors.Join(conflicts...))
			}
		}
		return normalizeGitDirAfterCommit(ctx, ws.workDir)
	})
	return dir, picks, err
}

func pullGitList(ctx context.Context, dir string, limit int, revisions ...string) ([]string, error) {
	args := append([]string{"rev-list", "--topo-order", "--max-count=" + strconv.Itoa(limit+1)}, revisions...)
	out, err := runWorkspacePullGit(ctx, dir, nil, args...)
	if err != nil {
		return nil, err
	}
	shas := strings.Fields(out)
	if len(shas) > limit {
		return nil, fmt.Errorf("pull history exceeds maxCommits (%d); narrow the source ref or increase maxCommits (maximum %d)", limit, MaxWorkspacePullCommits)
	}
	slices.Reverse(shas)
	return shas, nil
}

func pullSelectedCommits(ctx context.Context, dir, source, target string, opts WorkspacePullOpts) ([]string, error) {
	if len(opts.Commits) == 0 {
		return pullGitList(ctx, dir, opts.MaxCommits, source, "^"+target)
	}
	// Explicit selection is also bounded: never walk arbitrarily far back to
	// find a requested hash. A nearer source ref lets callers select older work.
	out, err := runWorkspacePullGit(ctx, dir, nil, "rev-list", "--topo-order", "--max-count="+strconv.Itoa(workspacePullSelectionWindow), source)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, sha := range opts.Commits {
		wanted[sha] = true
	}
	var selected []string
	for _, sha := range strings.Fields(out) {
		if wanted[sha] {
			selected = append(selected, sha)
			delete(wanted, sha)
		}
	}
	if len(wanted) != 0 {
		return nil, fmt.Errorf("selected commits are not within the source's latest %d commits; use a nearer source ref", workspacePullSelectionWindow)
	}
	slices.Reverse(selected)
	return selected, nil
}

func pullIsAncestor(ctx context.Context, dir, ancestor, tip string) (bool, error) {
	_, err := runWorkspacePullGit(ctx, dir, nil, "merge-base", "--is-ancestor", ancestor, tip)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// foldWorkspacePull advances a clean speculative HEAD after each pickable
// commit. Dirty paths remain a separate, invariant set and cannot be swept into
// someone else's commit. Conflicting candidates do not advance the fold.
func foldWorkspacePull(ctx context.Context, dir, source string, dirty []string, opts WorkspacePullOpts) ([]WorkspacePullPick, error) {
	targetOut, err := runWorkspacePullGit(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(targetOut)
	selected, err := pullSelectedCommits(ctx, dir, source, target, opts)
	if err != nil {
		return nil, err
	}
	picks := make([]WorkspacePullPick, len(selected))
	var newCommits []string
	for i, sha := range selected {
		picks[i] = WorkspacePullPick{SHA: sha, Status: WorkspaceCommitPickable, Reason: WorkspaceCommitPickReasonNone, ConflictPaths: []string{}}
		present, err := pullIsAncestor(ctx, dir, sha, target)
		if err != nil {
			return nil, err
		}
		if present {
			picks[i].Status = WorkspaceCommitPicked
		} else {
			newCommits = append(newCommits, sha)
		}
	}
	if len(newCommits) == 0 {
		return picks, nil
	}
	// A selected prefix (including merge ancestry) can fast-forward too, but
	// only when it includes every new ancestor of the selected tip.
	ffTip := newCommits[len(newCommits)-1]
	fastForward, err := pullIsAncestor(ctx, dir, target, ffTip)
	if err != nil {
		return nil, err
	}
	if fastForward {
		rangeSHAs, err := pullGitList(ctx, dir, opts.MaxCommits, ffTip, "^"+target)
		if err != nil {
			return nil, err
		}
		a, b := slices.Clone(rangeSHAs), slices.Clone(newCommits)
		slices.Sort(a)
		slices.Sort(b)
		fastForward = slices.Equal(a, b)
	}
	origins, patchIDs := map[string]bool{}, map[string]bool{}
	if !fastForward {
		local, err := pullGitList(ctx, dir, opts.MaxCommits, target, "^"+source)
		if err != nil {
			return nil, err
		}
		for _, sha := range local {
			message, err := runWorkspacePullGit(ctx, dir, nil, "show", "-s", "--format=%B", sha)
			if err != nil {
				return nil, err
			}
			for _, origin := range pullCommitOrigins(message) {
				origins[origin] = true
			}
			id, err := workspacePullPatchID(ctx, dir, sha)
			if err != nil {
				return nil, err
			}
			if id != "" {
				patchIDs[id] = true
			}
		}
	}
	for i := range picks {
		pick := &picks[i]
		if pick.Status == WorkspaceCommitPicked {
			continue
		}
		if fastForward {
			paths, err := pullCommitPaths(ctx, dir, pick.SHA)
			if err != nil {
				return nil, err
			}
			if conflicts := pullOverlappingPaths(paths, dirty); len(conflicts) > 0 {
				pick.Status, pick.Reason, pick.ConflictPaths = WorkspaceCommitConflict, WorkspaceCommitPickReasonDirty, conflicts
			}
			continue
		}
		if err := foldWorkspacePullCommit(ctx, dir, pick, dirty, opts, origins, patchIDs); err != nil {
			return nil, err
		}
	}
	if fastForward && !slices.ContainsFunc(picks, func(p WorkspacePullPick) bool { return p.Status == WorkspaceCommitConflict }) {
		if _, err := runWorkspacePullGit(ctx, dir, nil, "reset", "--hard", ffTip); err != nil {
			return nil, err
		}
	}
	return picks, nil
}

func foldWorkspacePullCommit(ctx context.Context, dir string, pick *WorkspacePullPick, dirty []string, opts WorkspacePullOpts, origins, patchIDs map[string]bool) error {
	sha := pick.SHA
	if origins[sha] {
		pick.Status = WorkspaceCommitPicked
		return nil
	}
	out, err := runWorkspacePullGit(ctx, dir, nil, "show", "-s", "--format=%P%x00%an%x00%ae%x00%aI%x00%cI%x00%B", sha)
	if err != nil {
		return err
	}
	meta := strings.SplitN(out, "\x00", 6)
	if len(meta) != 6 {
		return fmt.Errorf("invalid metadata for source commit %s", sha)
	}
	for _, origin := range pullCommitOrigins(meta[5]) {
		if origins[origin] {
			pick.Status = WorkspaceCommitPicked
			return nil
		}
		// Origin objects need not be present in the joined repositories.
		if _, err := runWorkspacePullGit(ctx, dir, nil, "cat-file", "-e", origin+"^{commit}"); err != nil {
			continue
		}
		present, err := pullIsAncestor(ctx, dir, origin, "HEAD")
		if err != nil {
			return err
		}
		if present {
			pick.Status = WorkspaceCommitPicked
			return nil
		}
	}
	if len(strings.Fields(meta[0])) > 1 {
		return fmt.Errorf("cannot cherry-pick merge commit %s without a mainline; pull its complete ancestry as a fast-forward or integrate it with git", sha)
	}
	id, err := workspacePullPatchID(ctx, dir, sha)
	if err != nil {
		return err
	}
	if id != "" && patchIDs[id] {
		pick.Status = WorkspaceCommitRedundant
		return nil
	}
	paths, err := pullCommitPaths(ctx, dir, sha)
	if err != nil {
		return err
	}
	if conflicts := pullOverlappingPaths(paths, dirty); len(conflicts) > 0 {
		pick.Status, pick.Reason, pick.ConflictPaths = WorkspaceCommitConflict, WorkspaceCommitPickReasonDirty, conflicts
		return nil
	}
	_, pickErr := runWorkspacePullGit(ctx, dir, nil, "cherry-pick", "--no-commit", sha)
	if pickErr != nil {
		conflicts, err := runWorkspacePullGit(ctx, dir, nil, "diff", "--name-only", "--diff-filter=U", "-z")
		if err != nil {
			return err
		}
		pick.ConflictPaths = splitPullPaths(conflicts)
		if len(pick.ConflictPaths) == 0 {
			return fmt.Errorf("cherry-pick %s: %w", sha, pickErr)
		}
		pick.Status, pick.Reason = WorkspaceCommitConflict, WorkspaceCommitPickReasonContent
		_, err = runWorkspacePullGit(ctx, dir, nil, "reset", "--hard", "HEAD")
		return err
	}
	changed, err := runWorkspacePullGit(ctx, dir, nil, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	if changed == "" {
		pick.Status = WorkspaceCommitRedundant
		return nil
	}
	name, email := opts.CommitterName, opts.CommitterEmail
	if name == "" {
		name = "Dagger"
	}
	if email == "" {
		email = "dagger@localhost"
	}
	env := []string{"GIT_AUTHOR_NAME=" + meta[1], "GIT_AUTHOR_EMAIL=" + meta[2], "GIT_AUTHOR_DATE=" + meta[3], "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email, "GIT_COMMITTER_DATE=" + meta[4]}
	message := strings.TrimRight(meta[5], "\n") + "\n\n(cherry picked from commit " + sha + ")\n"
	if _, err := runWorkspacePullGit(ctx, dir, env, "commit", "--no-verify", "--no-gpg-sign", "--cleanup=verbatim", "-m", message); err != nil {
		return err
	}
	origins[sha] = true
	for _, origin := range pullCommitOrigins(meta[5]) {
		origins[origin] = true
	}
	if id != "" {
		patchIDs[id] = true
	}
	return nil
}

func pullCommitOrigins(message string) []string {
	var origins []string
	for _, line := range strings.Split(message, "\n") {
		sha, ok := strings.CutPrefix(line, "(cherry picked from commit ")
		if !ok {
			continue
		}
		sha, ok = strings.CutSuffix(sha, ")")
		if ok && workspacePullSHA(sha) {
			origins = append(origins, sha)
		}
	}
	return origins
}

func pullCommitPaths(ctx context.Context, dir, sha string) ([]string, error) {
	out, err := runWorkspacePullGit(ctx, dir, nil, "diff-tree", "--root", "-m", "--no-commit-id", "--no-renames", "--name-only", "-r", "-z", sha, "--")
	return splitPullPaths(out), err
}

func splitPullPaths(out string) []string {
	paths := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	paths = slices.DeleteFunc(paths, func(p string) bool { return p == "" })
	slices.Sort(paths)
	return slices.Compact(paths)
}

func pullOverlappingPaths(touched, dirty []string) []string {
	var conflicts []string
	for _, p := range touched {
		for _, d := range dirty {
			if p == d || strings.HasPrefix(p, d+"/") || strings.HasPrefix(d, p+"/") {
				conflicts = append(conflicts, p)
				break
			}
		}
	}
	slices.Sort(conflicts)
	return slices.Compact(conflicts)
}

// Keep empty-directory changes too: overlaying one onto an incoming file would
// hide committed content. Ignore structural parent entries when a more precise
// child path is available, so unrelated edits under that parent can still pull.
func pullDirtyPaths(paths *ChangesetPaths) []string {
	all := slices.Concat(paths.Added, paths.Modified, paths.AllRemoved)
	slices.Sort(all)
	all = slices.Compact(all)
	var dirty []string
	for i, p := range all {
		if strings.HasSuffix(p, "/") && i+1 < len(all) && strings.HasPrefix(all[i+1], p) {
			continue
		}
		p = strings.TrimSuffix(p, "/")
		if p != "" && p != "." {
			dirty = append(dirty, p)
		}
	}
	return dirty
}

func runWorkspacePullGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	operation := args[0]
	args = append([]string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "rerere.enabled=false", "-c", "submodule.recurse=false"}, args...)
	cmd := gitCmd(ctx, dir, args...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr workspacePullOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return "", fmt.Errorf("git %s: pull command output exceeds %d bytes", operation, workspacePullOutputLimit)
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", operation, err, stderr.String())
	}
	return stdout.String(), nil
}

type workspacePullOutput struct {
	buf      bytes.Buffer
	exceeded bool
}

func (out *workspacePullOutput) String() string { return out.buf.String() }

func (out *workspacePullOutput) Write(p []byte) (int, error) {
	if len(p) > workspacePullOutputLimit-out.buf.Len() {
		out.exceeded = true
		return 0, fmt.Errorf("pull command output limit exceeded")
	}
	return out.buf.Write(p)
}

// Stream patches to patch-id, rather than buffering potentially large binary
// diffs in the engine. The outer pull deadline bounds both subprocesses.
func workspacePullPatchID(ctx context.Context, dir, sha string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	show := gitCmd(ctx, dir, "--no-replace-objects", "show", "--format=", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", sha, "--")
	pipe, err := show.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr workspacePullOutput
	show.Stderr = &stderr
	patch := gitCmd(ctx, dir, "patch-id", "--stable")
	patch.Stdin = pipe
	if err := show.Start(); err != nil {
		return "", err
	}
	out, patchErr := patch.Output()
	if patchErr != nil {
		cancel()
	}
	showErr := show.Wait()
	if patchErr != nil {
		return "", fmt.Errorf("patch-id for %s: %w", sha, patchErr)
	}
	if showErr != nil {
		return "", fmt.Errorf("show patch for %s: %w: %s", sha, showErr, stderr.String())
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

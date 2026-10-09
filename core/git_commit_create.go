package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	if len(stagePaths) == 0 && len(commitRemovedDirs(content.paths)) == 0 && !opts.AllowEmpty {
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
		if err := stageCheckoutChanges(func(args ...string) (string, error) {
			return runWorkspaceCommitGit(ctx, ws.workDir, env, args...)
		}, content.paths); err != nil {
			return err
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

// GitCommitChangesetNativeBase proves same-base provenance without evaluating or
// hashing either directory. Only a direct Git tree recipe qualifies: subdirectory
// selection, filters and arbitrary equal-looking directories must use the merge
// path. Repository recipe identity deliberately retains authorization scope.
func GitCommitChangesetNativeBase(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes *Changeset) (bool, error) {
	reason, err := gitCommitChangesetNativeBaseReason(ctx, parent, changes)
	return reason == "", err
}

// gitCommitChangesetNativeBaseReason is GitCommitChangesetNativeBase with the
// fixed code of the first provenance check that failed ("" when eligible), so
// fast paths can record why they were skipped.
func gitCommitChangesetNativeBaseReason(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes *Changeset) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if changes == nil || changes.Before.Self() == nil {
		return "no-before", nil
	}
	ref := parent.Self()
	if ref == nil || ref.Ref == nil || ref.Repo.Self() == nil || len(ref.Ref.SHA) != 40 || !IsFullGitSHA(ref.Ref.SHA) {
		return "parent-ref", nil
	}
	if ref.Ref.Name != "" && ref.Ref.Name != ref.Ref.SHA && !strings.HasPrefix(ref.Ref.Name, "refs/heads/") {
		return "parent-ref-kind", nil
	}
	switch ref.Backend.(type) {
	case *LocalGitRef, *RemoteGitRef:
	default:
		return "parent-backend", nil
	}
	lazy, ok := changes.Before.Self().Lazy.(*DirectoryGitTreeLazy)
	if !ok || lazy.Ref.Self() == nil || lazy.Ref.Self().Ref == nil {
		return "before-not-git-tree", nil
	}
	base := lazy.Ref.Self()
	if base.Repo.Self() == nil {
		return "before-not-git-tree", nil
	}
	if lazy.KeepGitDir || !lazy.DiscardGitDir && !base.Repo.Self().DiscardGitDir {
		return "before-keeps-git-dir", nil
	}
	if base.Ref.SHA != ref.Ref.SHA {
		return "before-commit-mismatch", nil
	}
	baseRepo, err := base.Repo.RecipeDigest(ctx)
	if err != nil {
		return "", err
	}
	parentRepo, err := ref.Repo.RecipeDigest(ctx)
	if err != nil {
		return "", err
	}
	if baseRepo != parentRepo {
		return "before-repository-mismatch", nil
	}
	return "", nil
}

var errNativeCommitUnsupported = errors.New("unsupported native git commit")

// Reasons are fixed codes, so a conservative fallback is easy to tell apart
// from a successful transaction, or from a real failure, on the native span.
type nativeCommitUnsupportedReason string

func (reason nativeCommitUnsupportedReason) Error() string { return string(reason) }
func (reason nativeCommitUnsupportedReason) Is(target error) bool {
	return target == errNativeCommitUnsupported
}

// nativeFallback is the policy shared by every native fast path (commit,
// workspace reconciliation, incremental checkout). Each is an optimization over
// a complete legacy path, so ANY failure falls back to that path: unanticipated
// repository states, missing objects and internal timeouts included. The
// caller's own cancellation surfaces; an error that merely wraps a deadline
// from some internal context still falls back. ErrNothingToCommit and a native
// merge conflict are results, not failures: the legacy path would reach the
// same answer only after restaging everything, so they are returned as-is. The
// full error, paths included, is recorded on span as attr, so fallbacks stay
// visible and debuggable. Callers release anything they produced before
// discarding the error.
func nativeFallback(ctx context.Context, span trace.Span, attr string, err error) bool {
	var conflict nativeMergeConflict
	if err == nil || ctx.Err() != nil || errors.Is(err, ErrNothingToCommit) || errors.As(err, &conflict) {
		return false
	}
	span.SetAttributes(attribute.String(attr, err.Error()))
	// The profile marker, e.g. git.native_merge.fallback[<reason>] for
	// dagger.git.native_merge.fallback_reason.
	recordFallbackMarker(ctx, strings.TrimSuffix(strings.TrimPrefix(attr, "dagger."), "_reason"), err)
	return true
}

// maxNativeSnapshotDepth bounds the lower layers a native fast path stacks a
// new snapshot on. Each native commit is a COW child of its parent repository
// snapshot and each incremental checkout a child of its parent tree, so a long
// session would otherwise approach overlayfs' limit (500 lower layers). The
// legacy paths start from a fresh snapshot, resetting the chain.
const maxNativeSnapshotDepth = 64

// checkNativeSnapshotDepth measures the chain from the overlay mount of the
// new child snapshot. Non-overlay snapshotters have no such limit.
func checkNativeSnapshotDepth(m *mount.Mount) error {
	if m == nil {
		return nil
	}
	for _, opt := range m.Options {
		if lower, ok := strings.CutPrefix(opt, "lowerdir="); ok && strings.Count(lower, ":")+1 > maxNativeSnapshotDepth {
			return nativeCommitUnsupportedReason("snapshot-depth")
		}
	}
	return nil
}

// nativeCommitFallback reports whether every leaf of err is an explicit
// unsupported marker, distinguishing an expected ineligibility from a failure
// in helpers that classify rather than fail. Mount cleanup joins errors with the
// operation's error; errors.Is alone would hide a real unmount failure.
func nativeCommitFallback(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case interface{ Unwrap() []error }:
		children := err.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !nativeCommitFallback(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return nativeCommitFallback(err.Unwrap())
	case nativeCommitUnsupportedReason:
		return true
	default:
		return err == errNativeCommitUnsupported
	}
}

// GitCommitChangesetNative records a same-base changeset using sparse staging,
// without checking out the parent tree or copying its history for the commit.
// Computing a not-yet-evaluated Directory changeset can still materialize its
// inputs; this does not replace the Directory diff backend.
// The returned Directory selects a bare
// Git directory in a child snapshot: snapshot ancestry pins and shares all
// existing objects, and only newly written objects consume a new layer. No
// mutable repository, index, ref, or mount-path alternate is shared with callers.
//
// The bool is false when the caller must use the checkout path: unsupported
// provenance/storage/semantics, or any failure of the native transaction (see
// nativeFallback). Only the caller's own cancellation is returned as an error.
// Currently supported: complete snapshot-owned SHA-1 branches/commit IDs, or
// owned shallow history with an exact remote anchor capability, without
// alternates or linked worktrees. Remote inputs first acquire an owned closure
// through their exact repository recipe. Ordinary files/symlinks include Git
// attributes and ignore rules. Changes at or inside a gitlink, or to
// .gitmodules, use the existing checkout path (see stageNativeChanges).
func GitCommitChangesetNative(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes *Changeset, opts GitCommitOpts) (_ *Directory, supported bool, rerr error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ctx, span := Tracer(ctx).Start(ctx, "git native commit transaction", telemetry.Internal())
	defer func() {
		if nativeFallback(ctx, span, "dagger.git.native.fallback_reason", rerr) {
			supported, rerr = false, nil
		}
		span.SetAttributes(attribute.Bool("dagger.git.native.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	reason, err := gitCommitChangesetNativeBaseReason(ctx, parent, changes)
	if err != nil {
		return nil, false, err
	}
	if reason != "" {
		// Recorded on the span by nativeFallback like any other fallback.
		return nil, false, nativeCommitUnsupportedReason(reason)
	}
	if err := normalizeNativeCommitOpts(&opts); err != nil {
		return nil, true, err
	}
	content, err := changes.content(ctx)
	if err != nil {
		return nil, true, fmt.Errorf("changeset content: %w", err)
	}
	local, err := nativeCommitRepository(ctx, parent)
	if err != nil {
		return nil, true, err
	}
	var gitSubdir string
	dir, err := withGitMergeWorkspace(ctx, local.Directory, "GitRef native commit transaction", func(ws *gitMergeWorkspace) error {
		if err := checkNativeSnapshotDepth(ws.mount); err != nil {
			return err
		}
		gitDir, err := local.nativeGitDir(ctx, ws.workDir)
		if err != nil {
			return err
		}
		gitSubdir, err = filepath.Rel(ws.workDir, gitDir)
		if err != nil {
			return err
		}
		return local.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
			objects, err := source.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "objects")
			if err != nil {
				return err
			}
			return withNativeCommitIndex(ctx, gitDir, strings.TrimSuffix(string(objects), "\n"), parent.Self().Ref, content.paths, opts, func(work string) error {
				return (&gitMergeWorkspace{root: work, dir: "/", workDir: work}).applyContent(ctx, content)
			})
		})
	})
	err = errors.Join(err, ctx.Err())
	if err != nil && dir != nil {
		// A failure or cancellation observed after snapshot commit still owns
		// that snapshot. Release it before returning an error or falling back.
		err = errors.Join(err, dir.OnRelease(context.WithoutCancel(ctx)))
	}
	if err != nil {
		return nil, true, err
	}
	selector, _ := dir.Dir.Peek()
	dir.SetPath(path.Join(selector, filepath.ToSlash(gitSubdir)))
	return dir, true, nil
}

// nativeCommitGitDir accepts only an object database owned by this snapshot.
// In particular a borrowed/partial database must not become a durable output.
func nativeCommitGitDir(ctx context.Context, root string) (string, error) {
	return nativeCommitGitDirWithShallow(ctx, root, false)
}

func nativeCommitGitDirWithShallow(ctx context.Context, root string, allowShallow bool) (string, error) {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Lstat(gitDir)
	if errors.Is(err, os.ErrNotExist) {
		gitDir = root // bare repository
	} else if err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", nativeCommitUnsupportedReason("git-directory-layout") // gitfile or symlink
	}
	for _, entry := range []struct{ path, reason string }{
		{"commondir", "linked-worktree"},
		{"shallow", "shallow-history"},
		{"objects/info/alternates", "object-alternates"},
		{"objects/info/http-alternates", "object-alternates"},
	} {
		data, err := os.ReadFile(filepath.Join(gitDir, entry.path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if len(data) != 0 && (!allowShallow || entry.path != "shallow") {
			return "", nativeCommitUnsupportedReason(entry.reason)
		}
	}
	info, err = os.Lstat(filepath.Join(gitDir, "objects"))
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", nativeCommitUnsupportedReason("object-directory-layout")
	}
	promisors, err := filepath.Glob(filepath.Join(gitDir, "objects", "pack", "*.promisor"))
	if err != nil {
		return "", err
	}
	if len(promisors) != 0 {
		return "", nativeCommitUnsupportedReason("partial-repository")
	}
	format, err := runWorkspaceCommitGit(ctx, root, []string{"GIT_NO_LAZY_FETCH=1"}, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(format) != "sha1" {
		return "", nativeCommitUnsupportedReason("object-format")
	}
	config, err := runWorkspaceCommitGit(ctx, root, nil, "config", "--local", "--null", "--list")
	if err != nil {
		return "", err
	}
	for _, setting := range strings.Split(config, "\x00") {
		key, value, _ := strings.Cut(setting, "\n")
		// Publication writes loose refs and replaces the source config with a
		// fresh files-backend config. Keeping a reftable while discarding its
		// extension would lose every inherited ref (and may fail to write HEAD).
		if key == "extensions.refstorage" && value != "files" {
			return "", nativeCommitUnsupportedReason("ref-storage")
		}
		if key == "extensions.partialclone" || (strings.HasPrefix(key, "remote.") && strings.HasSuffix(key, ".promisor")) {
			return "", nativeCommitUnsupportedReason("partial-repository")
		}
	}
	return gitDir, nil
}

// withNativeCommitIndex uses a private metadata directory and a sparse staging
// worktree. The only parent blobs checked out are attributes/ignore files on
// changed paths' ancestor chains; unchanged source blobs are never read.
// parentObjects must be mounted read-only
// for this entire call. New objects go into scratch first: Git may freshen the
// mtime of an existing pack in its primary object store, copying up a whole
// inherited pack if pointed at the writable COW child directly.
func withNativeCommitIndex(ctx context.Context, gitDir, parentObjects string, ref *gitutil.Ref, paths *ChangesetPaths, opts GitCommitOpts, apply func(string) error) error {
	parent := ref.SHA
	branchName := ref.Name
	// The schema's full-SHA and abbreviated-SHA resolvers retain the resolved
	// SHA as Name. It is still a detached commit, not a branch to advance.
	if branchName == parent && IsFullGitSHA(branchName) {
		branchName = ""
	}
	if branchName != "" && !strings.HasPrefix(branchName, "refs/heads/") {
		return nativeCommitUnsupportedReason("ref-kind")
	}
	if err := normalizeNativeCommitOpts(&opts); err != nil {
		return err
	}
	paths = paths.withoutGitMeta()
	if len(commitStagePaths(paths)) == 0 && len(commitRemovedDirs(paths)) == 0 && !opts.AllowEmpty {
		return ErrNothingToCommit
	}
	scratch, err := os.MkdirTemp("", "dagger-git-commit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	work := filepath.Join(scratch, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		return err
	}
	meta := filepath.Join(scratch, "repo")
	if _, err := runWorkspaceCommitGit(ctx, scratch, nil, "init", "--bare", "--template=", "--object-format=sha1", "--ref-format=files", meta); err != nil {
		return err
	}
	if err := copyGitShallowBoundary(filepath.Dir(parentObjects), meta); err != nil {
		return err
	}
	remotes, upstream, err := readGitRemoteSelectionForRef(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(gitDir)), branchName)
	if err != nil {
		return err
	}
	metadataGit := gitutil.NewGitCLI(gitutil.WithGitDir(meta))
	for _, remote := range remotes {
		if err := writeGitCheckoutRemote(ctx, metadataGit, remote); err != nil {
			return err
		}
	}
	env := []string{
		"GIT_DIR=" + meta, "GIT_WORK_TREE=" + work,
		"GIT_INDEX_FILE=" + filepath.Join(scratch, "index"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(parentObjects),
		"GIT_LITERAL_PATHSPECS=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_AUTHOR_NAME=" + opts.AuthorName, "GIT_AUTHOR_EMAIL=" + opts.AuthorEmail,
		"GIT_COMMITTER_NAME=" + opts.CommitterName, "GIT_COMMITTER_EMAIL=" + opts.CommitterEmail,
		"GIT_AUTHOR_DATE=" + opts.Date, "GIT_COMMITTER_DATE=" + opts.CommitterDate,
	}
	run := func(args ...string) (string, error) { return runWorkspaceCommitGit(ctx, work, env, args...) }
	if err := stageNativeChanges(run, parent, paths, true, func() error { return apply(work) }); err != nil {
		return err
	}
	tree, err := run("write-tree")
	if err != nil {
		return err
	}
	baseTree, err := run("rev-parse", parent+"^{tree}")
	if err != nil {
		return err
	}
	if tree == baseTree && !opts.AllowEmpty {
		return ErrNothingToCommit
	}
	message := opts.Message
	if opts.Signoff {
		// Use Git's own trailer parser, matching commit --trailer, including
		// duplicate handling and whitespace before the trailer block. commit
		// --trailer passes --no-divider: a "---" line in the message is text,
		// not a patch divider to insert the trailer above.
		messageFile := filepath.Join(scratch, "message")
		if err := os.WriteFile(messageFile, []byte(message), 0600); err != nil {
			return err
		}
		message, err = run("interpret-trailers", "--no-divider", "--trailer", fmt.Sprintf("Signed-off-by: %s <%s>", opts.AuthorName, opts.AuthorEmail), messageFile)
		if err != nil {
			return err
		}
	}
	sha, err := run("commit-tree", strings.TrimSpace(tree), "-p", parent, "-m", message)
	if err != nil {
		return err
	}
	// Copy only loose objects created by this transaction. Existing objects
	// remain in the snapshot's lower layers; no alternates file is persisted.
	if err := copyNativeCommitObjects(ctx, filepath.Join(meta, "objects"), filepath.Join(gitDir, "objects")); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := publishNativeCommit(gitDir, meta, branchName, sha, run); err != nil {
		return err
	}
	// Publication installs the final HEAD and configuration. Capture their
	// baseline now so the new commit keeps its parent's remote selection.
	return writeGitRemoteSelection(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(gitDir)), remotes, upstream)
}

func normalizeNativeCommitOpts(opts *GitCommitOpts) error {
	if _, err := time.Parse(time.RFC3339, opts.Date); err != nil {
		return fmt.Errorf("commit date must be RFC3339: %w", err)
	}
	if strings.TrimSpace(opts.Message) == "" || strings.ContainsRune(opts.Message, 0) {
		return fmt.Errorf("commit message must be nonempty and contain no NUL")
	}
	if opts.AuthorName == "" {
		opts.AuthorName = "Dagger"
	}
	if opts.AuthorEmail == "" {
		opts.AuthorEmail = "dagger@localhost"
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
	return nil
}

func publishNativeCommit(gitDir, meta, branchName, sha string, run func(...string) (string, error)) error {
	// Publish only inside the private COW child. No source ref or index is
	// modified. Unlike a fresh fetch, this prototype retains unrelated source
	// refs/tags rather than traversing history to reproduce fetch's tag pruning.
	// Advance the selected branch, or detach HEAD for a commit-ID parent.
	// Root all publication writes in the private child. Metadata is replaced
	// rather than truncated so even inherited hardlinks cannot be modified.
	root, err := os.OpenRoot(gitDir)
	if err != nil {
		return err
	}
	defer root.Close()
	head := strings.TrimSpace(sha) + "\n"
	if branchName != "" {
		if _, err := run("check-ref-format", branchName); err != nil {
			return err
		}
		if err := nativeCommitWriteFile(root, filepath.FromSlash(branchName), []byte(head)); err != nil {
			return err
		}
		head = "ref: " + branchName + "\n"
	}
	if err := nativeCommitWriteFile(root, "HEAD", []byte(head)); err != nil {
		return err
	}
	config, err := os.ReadFile(filepath.Join(meta, "config"))
	if err != nil {
		return err
	}
	if err := nativeCommitWriteFile(root, "config", config); err != nil {
		return err
	}
	// hooks go too: the engine never runs them (every hook-triggering command
	// disables them), and the result selects the Git directory itself, so a
	// hook symlinked into the source worktree would escape it and fail
	// withContents' self-containment check.
	for _, name := range []string{"index", "logs", "hooks", "ORIG_HEAD", "COMMIT_EDITMSG"} {
		if err := root.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

// stageNativeChanges seeds a private index from parent and stages only the
// supplied delta. The caller supplies an empty worktree and operation-local
// metadata/object storage. Only ancestor attribute/ignore blobs are hydrated.
//
// Gitlinks matter only where the delta reaches them: a staged path at or under
// a gitlink is a submodule change, which falls back. A gitlink inside a
// directory a file replaced is removed by `git add`, as on a full checkout.
//
// With removeGitlinks, a gitlink whose directory the changeset removed (an
// uninitialized submodule's empty directory, or a whole submodule checkout) is
// removed too, as `git add -A` would on a full checkout: changesets report
// that only as directory entries, which are otherwise never staged. Removals
// beneath it are submodule content, never in the index. A gitlink whose path
// holds added or modified files again still falls back. Without
// removeGitlinks (the workspace merge, which reconciles such directories from
// the raw deltas) the gitlink is left alone unless a staged path reaches it.
func stageNativeChanges(run func(...string) (string, error), parent string, paths *ChangesetPaths, removeGitlinks bool, apply func() error) error {
	if _, err := run("read-tree", parent); err != nil {
		return err
	}
	stage, err := newNativeStagePaths(paths, removeGitlinks)
	if err != nil {
		return err
	}
	hydrate, gitlinks, err := stage.classify(run, parent)
	if err != nil {
		return err
	}
	for _, batch := range batchPathSpecs(hydrate) {
		if _, err := run(append([]string{"checkout-index", "--force", "--"}, batch...)...); err != nil {
			return err
		}
	}
	if err := apply(); err != nil {
		return err
	}
	// Remove first, including file/directory replacements. git add only sees
	// added/modified files, so a sparse worktree cannot delete untouched files.
	removed := slices.Concat(commitStagePaths(&ChangesetPaths{AllRemoved: paths.AllRemoved}), gitlinks)
	for _, batch := range batchPathSpecs(removed) {
		if _, err := run(append([]string{"update-index", "--force-remove", "--"}, batch...)...); err != nil {
			return err
		}
	}
	added := commitStagePaths(&ChangesetPaths{Added: paths.Added, Modified: paths.Modified})
	for _, batch := range batchPathSpecs(added) {
		if _, err := run(append([]string{"add", "-A", "--"}, batch...)...); err != nil {
			return err
		}
	}
	return nil
}

// nativeStagePaths are the parent tree paths stageNativeChanges inspects.
type nativeStagePaths struct {
	// touched: staged paths and their ancestors, where a gitlink means a
	// staged change inside (or of) a submodule.
	touched map[string]bool
	// controls: ancestor attribute/ignore files to hydrate.
	controls map[string]bool
	// removedDirs: directories the changeset removed (removeGitlinks only).
	removedDirs map[string]bool
	// filled: added or modified paths and their ancestors (removeGitlinks
	// only).
	filled map[string]bool
}

func validNativeStagePath(p string) bool {
	return path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && !gitMetaPath(p)
}

func newNativeStagePaths(paths *ChangesetPaths, removeGitlinks bool) (*nativeStagePaths, error) {
	s := &nativeStagePaths{
		touched:     map[string]bool{},
		controls:    map[string]bool{},
		removedDirs: map[string]bool{},
		filled:      map[string]bool{},
	}
	for _, p := range commitStagePaths(paths) {
		if !validNativeStagePath(p) {
			return nil, fmt.Errorf("invalid commit path %q", p)
		}
		if path.Base(p) == ".gitmodules" {
			return nil, nativeCommitUnsupportedReason("gitmodules-change")
		}
		s.touched[p] = true
		for dir := path.Dir(p); ; dir = path.Dir(dir) {
			s.controls[path.Join(dir, ".gitattributes")] = true
			s.controls[path.Join(dir, ".gitignore")] = true
			if dir == "." {
				break
			}
			s.touched[dir] = true
		}
	}
	if !removeGitlinks {
		return s, nil
	}
	for _, p := range commitRemovedDirs(paths) {
		if !validNativeStagePath(p) {
			return nil, fmt.Errorf("invalid commit path %q", p)
		}
		s.removedDirs[p] = true
	}
	for _, p := range commitStagePaths(&ChangesetPaths{Added: paths.Added, Modified: paths.Modified}) {
		for ; p != "." && !s.filled[p]; p = path.Dir(p) {
			s.filled[p] = true
		}
	}
	return s, nil
}

// classify lists the inspected paths in parent, returning the control files
// to hydrate and the removed gitlinks to remove.
//
// ls-tree matches each pathspec as a prefix: once it descends into an
// ancestor, it lists every direct child of that directory. Only exact names
// are classified, so unrelated sibling gitlinks never matter.
func (s *nativeStagePaths) classify(run func(...string) (string, error), parent string) (hydrate, gitlinks []string, _ error) {
	inspect := make([]string, 0, len(s.touched)+len(s.removedDirs)+len(s.controls))
	for _, set := range []map[string]bool{s.touched, s.removedDirs, s.controls} {
		for p := range set {
			inspect = append(inspect, p)
		}
	}
	slices.Sort(inspect)
	inspect = slices.Compact(inspect)
	for _, batch := range batchPathSpecs(inspect) {
		out, err := run(append([]string{"ls-tree", "-z", parent, "--"}, batch...)...)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range strings.Split(out, "\x00") {
			mode, rest, ok := strings.Cut(entry, " ")
			if !ok {
				continue
			}
			_, name, ok := strings.Cut(rest, "\t")
			if !ok {
				return nil, nil, fmt.Errorf("invalid ls-tree entry")
			}
			switch {
			case mode == "160000" && s.removedDirs[name] && !s.filled[name]:
				gitlinks = append(gitlinks, name)
			case mode == "160000" && (s.touched[name] || s.removedDirs[name]):
				return nil, nil, nativeCommitUnsupportedReason("gitlink-change")
			case s.controls[name] && (mode == "100644" || mode == "100755"):
				hydrate = append(hydrate, name)
			}
		}
	}
	return hydrate, gitlinks, nil
}

// nativeCommitMkdirParents refuses inherited symlink directories, even ones
// pointing elsewhere inside the root: refs must not redirect into objects.
func nativeCommitMkdirParents(root *os.Root, name string) error {
	parent := filepath.Dir(name)
	if parent == "." {
		return nil
	}
	prefix := ""
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(prefix, 0755); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !info.IsDir() {
			return nativeCommitUnsupportedReason("unsafe-write-path")
		}
	}
	return nil
}

func nativeCommitWriteFile(root *os.Root, name string, data []byte) error {
	if err := nativeCommitMkdirParents(root, name); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	return errors.Join(err, file.Close())
}

// copyNativeCommitObjects walks only the new operation-local objects, never the
// inherited object database: loose objects, plus the self-contained packs git
// add streams blobs >= core.bigFileThreshold into. Pack names are content
// hashes. O_EXCL avoids freshening an existing object or pack, even when
// content deduplication made an insertion redundant.
func copyNativeCommitObjects(ctx context.Context, source, dest string) error {
	var newObjects, newObjectBytes int64
	defer func() {
		// These are compressed loose-object and transaction-pack file bytes
		// copied into the child, not logical blob bytes or physical snapshot
		// disk allocation. new_objects counts loose objects only.
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.Int64("dagger.git.native.new_objects", newObjects),
			attribute.Int64("dagger.git.native.new_object_bytes", newObjectBytes),
		)
	}()
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	return filepath.WalkDir(source, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		pack := nativeCommitTransactionPack(parts, entry)
		if !pack && (len(parts) != 2 || len(parts[0]) != 2 || !IsFullGitSHA(strings.Join(parts, "")) || !entry.Type().IsRegular()) {
			return fmt.Errorf("unexpected transaction object %q", rel)
		}
		if err := nativeCommitMkdirParents(root, rel); err != nil {
			return err
		}
		out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
		if errors.Is(err, os.ErrExist) {
			info, err := root.Lstat(rel)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nativeCommitUnsupportedReason("unsafe-write-path")
			}
			return nil
		}
		if err != nil {
			return err
		}
		in, err := os.Open(name)
		if err != nil {
			return errors.Join(err, out.Close())
		}
		n, err := io.Copy(out, in)
		newObjectBytes += n
		err = errors.Join(err, in.Close(), out.Close())
		if err == nil && !pack {
			newObjects++
		}
		return err
	})
}

// nativeCommitTransactionPack matches pack/pack-<sha>.{pack,idx,rev}: a pack
// git wrote for this transaction, with its index and reverse index.
func nativeCommitTransactionPack(parts []string, entry os.DirEntry) bool {
	if len(parts) != 2 || parts[0] != "pack" || !entry.Type().IsRegular() {
		return false
	}
	name, ok := strings.CutPrefix(parts[1], "pack-")
	if !ok {
		return false
	}
	ext := path.Ext(name)
	switch ext {
	case ".pack", ".idx", ".rev":
	default:
		return false
	}
	sha := strings.TrimSuffix(name, ext)
	return len(sha) == 40 && IsFullGitSHA(sha)
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

// commitRemovedDirs lists the directories a changeset removed, without their
// trailing slash. Git records none of them, except a gitlink: the directory a
// submodule is checked out in.
func commitRemovedDirs(paths *ChangesetPaths) []string {
	var out []string
	for _, p := range paths.AllRemoved {
		if dir, ok := strings.CutSuffix(p, "/"); ok && dir != "" && dir != "." {
			out = append(out, dir)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// stageCheckoutChanges is the checkout path's staging, in a full checkout of
// the parent with the changeset applied. Only the scoped paths are staged: the
// work tree may legitimately carry other uncommitted changes, everything
// outside this commit's scope, and a bare `git add -A` would sweep them in.
//
// Like the native staging, a removed submodule directory removes its gitlink,
// unless files were added or modified at or beneath it again; paths beneath
// it are submodule content, not in this index.
func stageCheckoutChanges(run func(...string) (string, error), paths *ChangesetPaths) error {
	removed := map[string]bool{}
	for _, dir := range commitRemovedDirs(paths) {
		removed[dir] = true
	}
	for _, p := range commitStagePaths(&ChangesetPaths{Added: paths.Added, Modified: paths.Modified}) {
		for ; p != "." && p != "/"; p = path.Dir(p) {
			delete(removed, p)
		}
	}
	candidates := make([]string, 0, len(removed))
	for dir := range removed {
		candidates = append(candidates, dir)
	}
	slices.Sort(candidates)
	var gitlinks []string
	isGitlink := map[string]bool{}
	for _, batch := range batchPathSpecs(candidates) {
		out, err := run(append([]string{"ls-files", "--stage", "-z", "--"}, batch...)...)
		if err != nil {
			return err
		}
		for _, entry := range splitOnNul([]byte(out)) {
			header, name, _ := strings.Cut(entry, "\t")
			if strings.HasPrefix(header, "160000 ") && removed[name] && !isGitlink[name] {
				isGitlink[name] = true
				gitlinks = append(gitlinks, name)
			}
		}
	}
	for _, batch := range batchPathSpecs(gitlinks) {
		if _, err := run(append([]string{"update-index", "--force-remove", "--"}, batch...)...); err != nil {
			return err
		}
	}
	stage := slices.DeleteFunc(commitStagePaths(paths), func(p string) bool {
		for dir := path.Dir(p); len(isGitlink) > 0 && dir != "." && dir != "/"; dir = path.Dir(dir) {
			if isGitlink[dir] {
				return true
			}
		}
		return false
	})
	for _, batch := range batchPathSpecs(stage) {
		if _, err := run(append([]string{"add", "-A", "--"}, batch...)...); err != nil {
			return err
		}
	}
	return nil
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
	return runWorkspaceCommitGitInput(ctx, dir, extraEnv, nil, args...)
}

func runWorkspaceCommitGitInput(ctx context.Context, dir string, extraEnv []string, stdin io.Reader, args ...string) (string, error) {
	var stdout bytes.Buffer
	err := runWorkspaceCommitGitStream(ctx, dir, extraEnv, stdin, &stdout, args...)
	return stdout.String(), err
}

// runWorkspaceCommitGitStream is runWorkspaceCommitGitInput writing stdout to
// a stream, for output too large to buffer.
func runWorkspaceCommitGitStream(ctx context.Context, dir string, extraEnv []string, stdin io.Reader, stdout io.Writer, args ...string) (rerr error) {
	// Callers may supply -c key=value before the verb. Never include those
	// values, pathspecs, commit messages, or identity inputs in the span name.
	commandArgs := args
	for len(commandArgs) > 0 {
		if len(commandArgs) >= 2 && commandArgs[0] == "-c" {
			commandArgs = commandArgs[2:]
		} else if commandArgs[0] == "--no-literal-pathspecs" {
			commandArgs = commandArgs[1:]
		} else {
			break
		}
	}
	operation := "command"
	if len(commandArgs) > 0 {
		operation = commandArgs[0]
	}
	ctx, span := Tracer(ctx).Start(ctx, "git "+operation, telemetry.Internal())
	// A profile op per command, e.g. git.read-tree, under the caller's phase.
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindIO, "git."+operation, wcprof.OpOpts{})
	defer func() {
		op.EndErr(rerr)
		var spanErr error
		if rerr != nil {
			spanErr = fmt.Errorf("git %s failed", operation)
		}
		telemetry.EndWithCause(span, &spanErr)
	}()
	cmd := gitCmd(ctx, dir, args...)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		// No ignored paths is a successful eligibility check, not a failed
		// Git operation. Preserve real process failures and cancellation.
		var exit *exec.ExitError
		if operation == "check-ignore" && ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("git %s: %w: %s", gitErrorArgs(args), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// maxGitErrorArgsBytes bounds the command line quoted in a git error.
const maxGitErrorArgsBytes = 512

// gitErrorArgs keeps a failed command recognizable without quoting a whole
// pathspec batch (up to maxGitPathSpecBytes) or a long commit message in the
// error, and in the fallback reason recorded from it.
func gitErrorArgs(args []string) string {
	if i := slices.Index(args, "--"); i >= 0 && len(args) > i+2 {
		args = append(slices.Clone(args[:i+2]), fmt.Sprintf("(+%d more paths)", len(args)-i-2))
	}
	s := fmt.Sprint(args)
	if len(s) > maxGitErrorArgsBytes {
		s = strings.ToValidUTF8(s[:maxGitErrorArgsBytes], "") + "…"
	}
	return s
}

const persistedDirectoryLazyKindGitCommit = "gitCommit"

// DirectoryGitCommitLazy produces the Git storage of GitRef.withCommit: the
// parent's repository with Changes committed under Opts. Identity and dates
// are explicit, so a rerun creates the same commit.
type DirectoryGitCommitLazy struct {
	LazyState
	Parent  dagql.ObjectResult[*GitRef]
	Changes dagql.ObjectResult[*Changeset]
	Opts    GitCommitOpts
}

type persistedDirectoryGitCommitLazy struct {
	ParentResultID  uint64        `json:"parentResultID"`
	ChangesResultID uint64        `json:"changesResultID"`
	Opts            GitCommitOpts `json:"opts"`
}

func (lazy *DirectoryGitCommitLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return evaluateDirectoryOutput(ctx, &lazy.LazyState, "GitRef.__withCommitDirectory", dir, func(ctx context.Context) (*Directory, error) {
		return gitCommitDirectory(ctx, lazy.Parent, lazy.Changes, lazy.Opts)
	})
}

// gitCommitDirectory commits changes on parent. Same-base edits can update an
// isolated Git index directly. Local storage shares its existing objects
// through snapshot ancestry; remote inputs first promote a private authorized
// closure, never a retained checkout. Divergent and unsupported inputs, and
// any native failure, use the general reconciliation.
func gitCommitDirectory(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes dagql.ObjectResult[*Changeset], opts GitCommitOpts) (*Directory, error) {
	if dir, supported, err := GitCommitChangesetNative(ctx, parent, changes.Self(), opts); err != nil || supported {
		return dir, err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var tree dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, parent, &tree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.NewBoolean(true)}}}); err != nil {
		return nil, err
	}
	beforeID, err := changes.Self().Before.ID()
	if err != nil {
		return nil, err
	}
	var ours dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, tree, &ours, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*Directory](beforeID)}}}); err != nil {
		return nil, err
	}
	changesID, err := changes.ID()
	if err != nil {
		return nil, err
	}
	var merged dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, ours, &merged, dagql.Selector{Field: "withChangeset", Args: []dagql.NamedInput{
		{Name: "changes", Value: dagql.NewID[*Changeset](changesID)}, {Name: "onConflict", Value: FailOnMergeConflict},
	}}); err != nil {
		return nil, fmt.Errorf("apply commit changes: %w", err)
	}
	treeID, err := tree.ID()
	if err != nil {
		return nil, err
	}
	var applied dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, merged.Self().After, &applied, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*Directory](treeID)}}}); err != nil {
		return nil, err
	}
	var base dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, parent, &base,
		dagql.Selector{Field: "asWorkspace"},
		dagql.Selector{Field: "git"},
		dagql.Selector{Field: "__checkout"},
	); err != nil {
		return nil, err
	}
	return GitCommitChangeset(ctx, base, applied.Self(), opts)
}

func (lazy *DirectoryGitCommitLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachLazyInput(attach, lazy.Parent, "DirectoryGitCommitLazy.Parent")
	if err != nil {
		return nil, err
	}
	changes, err := attachLazyInput(attach, lazy.Changes, "DirectoryGitCommitLazy.Changes")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Changes = changes
	return []dagql.AnyResult{parent, changes}, nil
}

func (lazy *DirectoryGitCommitLazy) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(enc, lazy.Parent, "git commit parent")
	if err != nil {
		return nil, err
	}
	changesID, err := encodePersistedObjectRef(enc, lazy.Changes, "git commit changes")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryGitCommitLazy{ParentResultID: parentID, ChangesResultID: changesID, Opts: lazy.Opts})
}

func decodeDirectoryGitCommitLazy(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (Lazy[*Directory], error) {
	var persisted persistedDirectoryGitCommitLazy
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git commit lazy: %w", err)
	}
	parent, err := loadPersistedObjectResultByResultID[*GitRef](ctx, dec, persisted.ParentResultID, "git commit parent")
	if err != nil {
		return nil, err
	}
	changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dec, persisted.ChangesResultID, "git commit changes")
	if err != nil {
		return nil, err
	}
	return &DirectoryGitCommitLazy{LazyState: NewLazyState(), Parent: parent, Changes: changes, Opts: persisted.Opts}, nil
}

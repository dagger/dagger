package core

import (
	"bytes"
	"context"
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

	"github.com/dagger/dagger/dagql"
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

// GitCommitChangesetNativeBase proves same-base provenance without evaluating or
// hashing either directory. Only a direct Git tree recipe qualifies: subdirectory
// selection, filters and arbitrary equal-looking directories must use the merge
// path. Repository recipe identity deliberately retains authorization scope.
func GitCommitChangesetNativeBase(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes *Changeset) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ref := parent.Self()
	if ref == nil || ref.Ref == nil || ref.Repo.Self() == nil || len(ref.Ref.SHA) != 40 || !IsFullGitSHA(ref.Ref.SHA) || changes == nil || changes.Before.Self() == nil {
		return false, nil
	}
	if _, ok := ref.Backend.(*LocalGitRef); !ok {
		return false, nil
	}
	lazy, ok := changes.Before.Self().Lazy.(*DirectoryGitTreeLazy)
	if !ok || lazy.Ref.Self() == nil || lazy.Ref.Self().Ref == nil {
		return false, nil
	}
	base := lazy.Ref.Self()
	if base.Repo.Self() == nil {
		return false, nil
	}
	if !(lazy.DiscardGitDir || base.Repo.Self().DiscardGitDir) || base.Ref.SHA != ref.Ref.SHA {
		return false, nil
	}
	baseRepo, err := base.Repo.RecipeDigest(ctx)
	if err != nil {
		return false, err
	}
	parentRepo, err := ref.Repo.RecipeDigest(ctx)
	if err != nil {
		return false, err
	}
	return baseRepo == parentRepo, nil
}

var errNativeCommitUnsupported = errors.New("unsupported native git commit")

// Reasons are fixed codes only, never paths, refs or repository configuration.
// Keep them on the native span so a conservative fallback is distinguishable
// from a successful transaction without exposing private source metadata.
type nativeCommitUnsupportedReason string

func (reason nativeCommitUnsupportedReason) Error() string { return string(reason) }
func (reason nativeCommitUnsupportedReason) Is(target error) bool {
	return target == errNativeCommitUnsupported
}

// nativeCommitFallback accepts an error only when every leaf is an explicit
// unsupported marker. Mount cleanup joins errors with the operation's error;
// errors.Is alone would hide a real unmount failure joined to a fallback reason.
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
// The bool is false only for explicitly unsupported provenance/storage/semantics.
// Errors (including cancellation and missing objects) must not trigger fallback.
// Currently supported: complete local SHA-1 branches/commit IDs without
// alternates or linked worktrees, and ordinary files/symlinks including Git
// attributes and ignore rules. Changes touching gitlinks or .gitmodules use the
// existing checkout path.
func GitCommitChangesetNative(ctx context.Context, parent dagql.ObjectResult[*GitRef], changes *Changeset, opts GitCommitOpts) (_ *Directory, supported bool, rerr error) {
	ok, err := GitCommitChangesetNativeBase(ctx, parent, changes)
	if err != nil || !ok {
		return nil, false, err
	}
	ctx, span := Tracer(ctx).Start(ctx, "git native commit transaction", telemetry.Internal())
	defer func() {
		span.SetAttributes(attribute.Bool("dagger.git.native.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	content, err := changes.content(ctx)
	if err != nil {
		return nil, true, fmt.Errorf("changeset content: %w", err)
	}
	local := parent.Self().Backend.(*LocalGitRef)
	var gitSubdir string
	dir, err := withGitMergeWorkspace(ctx, local.repo.Directory, "GitRef native commit transaction", func(ws *gitMergeWorkspace) error {
		gitDir, err := nativeCommitGitDir(ctx, ws.workDir)
		if err != nil {
			return err
		}
		gitSubdir, err = filepath.Rel(ws.workDir, gitDir)
		if err != nil {
			return err
		}
		return local.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
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
		// A cancellation observed after snapshot commit still owns that
		// snapshot. Do not abandon it when returning an error or fallback.
		err = errors.Join(err, dir.OnRelease(context.WithoutCancel(ctx)))
	}
	if nativeCommitFallback(err) {
		var reason nativeCommitUnsupportedReason
		if errors.As(err, &reason) {
			span.SetAttributes(attribute.String("dagger.git.native.fallback_reason", string(reason)))
		}
		return nil, false, nil
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
		if len(data) != 0 {
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
		key, _, _ := strings.Cut(setting, "\n")
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
	paths = paths.withoutGitMeta()
	stagePaths := commitStagePaths(paths)
	if len(stagePaths) == 0 && !opts.AllowEmpty {
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
	if _, err := runWorkspaceCommitGit(ctx, scratch, nil, "init", "--bare", "--template=", "--object-format=sha1", meta); err != nil {
		return err
	}
	remotes, err := readGitConfigRemotes(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(gitDir)))
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		if err := writeGitCheckoutRemote(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(meta)), remote); err != nil {
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
	if err := stageNativeChanges(run, parent, paths, func() error { return apply(work) }); err != nil {
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
		// duplicate handling and whitespace before the trailer block.
		messageFile := filepath.Join(scratch, "message")
		if err := os.WriteFile(messageFile, []byte(message), 0600); err != nil {
			return err
		}
		message, err = run("interpret-trailers", "--trailer", fmt.Sprintf("Signed-off-by: %s <%s>", opts.AuthorName, opts.AuthorEmail), messageFile)
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
	for _, name := range []string{"index", "logs", "ORIG_HEAD", "COMMIT_EDITMSG"} {
		if err := root.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

// stageNativeChanges seeds a private index from parent and stages only the
// supplied delta. The caller supplies an empty worktree and operation-local
// metadata/object storage. Only ancestor attribute/ignore blobs are hydrated.
func stageNativeChanges(run func(...string) (string, error), parent string, paths *ChangesetPaths, apply func() error) error {
	if _, err := run("read-tree", parent); err != nil {
		return err
	}
	controls := map[string]bool{}
	ancestors := map[string]bool{}
	for _, p := range commitStagePaths(paths) {
		if path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || gitMetaPath(p) {
			return fmt.Errorf("invalid commit path %q", p)
		}
		if path.Base(p) == ".gitmodules" {
			return nativeCommitUnsupportedReason("gitmodules-change")
		}
		ancestors[p] = true
		for dir := path.Dir(p); ; dir = path.Dir(dir) {
			controls[path.Join(dir, ".gitattributes")] = true
			controls[path.Join(dir, ".gitignore")] = true
			if dir == "." {
				break
			}
			ancestors[dir] = true
		}
	}
	for p := range controls {
		ancestors[p] = true
	}
	var inspect []string
	for p := range ancestors {
		inspect = append(inspect, p)
	}
	slices.Sort(inspect)
	for _, batch := range batchPathSpecs(inspect) {
		out, err := run(append([]string{"ls-tree", "-z", parent, "--"}, batch...)...)
		if err != nil {
			return err
		}
		for _, entry := range strings.Split(out, "\x00") {
			mode, rest, ok := strings.Cut(entry, " ")
			if !ok {
				continue
			}
			_, name, ok := strings.Cut(rest, "\t")
			if !ok {
				return fmt.Errorf("invalid ls-tree entry")
			}
			if mode == "160000" {
				return nativeCommitUnsupportedReason("gitlink-change")
			}
			if controls[name] && (mode == "100644" || mode == "100755") {
				if _, err := run("checkout-index", "--force", "--", name); err != nil {
					return err
				}
			}
		}
	}
	if err := apply(); err != nil {
		return err
	}
	// Remove first, including file/directory replacements. git add only sees
	// added/modified files, so a sparse worktree cannot delete untouched files.
	removed := commitStagePaths(&ChangesetPaths{AllRemoved: paths.AllRemoved})
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

// copyNativeCommitObjects walks only the new operation-local loose objects,
// never the inherited object database. O_EXCL avoids freshening an existing
// object, even when content deduplication made an insertion redundant.
func copyNativeCommitObjects(ctx context.Context, source, dest string) error {
	var newObjects, newObjectBytes int64
	defer func() {
		// These are compressed loose-object file bytes copied into the child,
		// not logical blob bytes or physical snapshot disk allocation.
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
		if len(parts) != 2 || len(parts[0]) != 2 || !IsFullGitSHA(strings.Join(parts, "")) || !entry.Type().IsRegular() {
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
		if err == nil {
			newObjects++
		}
		return err
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
func runWorkspaceCommitGit(ctx context.Context, dir string, extraEnv []string, args ...string) (_ string, rerr error) {
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
	defer func() {
		var spanErr error
		if rerr != nil {
			spanErr = fmt.Errorf("git %s failed", operation)
		}
		telemetry.EndWithCause(span, &spanErr)
	}()
	cmd := gitCmd(ctx, dir, args...)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// No ignored paths is a successful eligibility check, not a failed
		// Git operation. Preserve real process failures and cancellation.
		var exit *exec.ExitError
		if operation == "check-ignore" && ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
			return stdout.String(), nil
		}
		return stdout.String(), fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func NewChangeset(ctx context.Context, before, after dagql.ObjectResult[*Directory]) (*Changeset, error) {
	return &Changeset{
		Before:    before,
		After:     after,
		pathsOnce: &sync.Once{},
	}, nil
}

type ChangesetPaths struct {
	Added      []string
	Modified   []string
	Removed    []string
	AllRemoved []string
}

// ComputePaths computes the added, modified, and removed paths using git diff.
// This must be called from a dagql resolver context where buildkit session is available.
func (ch *Changeset) ComputePaths(ctx context.Context) (*ChangesetPaths, error) {
	ch.pathsOnce.Do(func() {
		ch.cachedPaths, ch.pathsErr = ch.computePathsOnce(ctx)
	})
	return ch.cachedPaths, ch.pathsErr
}

func (ch *Changeset) computePathsOnce(ctx context.Context) (*ChangesetPaths, error) {
	if ch.Before.ID().Digest() == ch.After.ID().Digest() {
		return &ChangesetPaths{}, nil
	}

	var result *ChangesetPaths
	err := ch.withMountedDirs(ctx, func(beforeDir, afterDir string) error {
		fileChanges, err := compareDirectories(ctx, beforeDir, afterDir)
		if err != nil {
			return err
		}

		beforeDirs, err := listSubdirectories(beforeDir)
		if err != nil {
			return fmt.Errorf("list before directories: %w", err)
		}
		afterDirs, err := listSubdirectories(afterDir)
		if err != nil {
			return fmt.Errorf("list after directories: %w", err)
		}
		addedDirs, removedDirs := diffStringSlices(beforeDirs, afterDirs)

		allRemoved := slices.Concat(fileChanges.Removed, removedDirs)

		result = &ChangesetPaths{
			Added:      slices.Concat(fileChanges.Added, addedDirs),
			Modified:   fileChanges.Modified,
			Removed:    collapseChildPaths(allRemoved),
			AllRemoved: allRemoved,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// withMountedDirs mounts the before and after directories and calls fn with their paths.
func (ch *Changeset) withMountedDirs(ctx context.Context, fn func(beforeDir, afterDir string) error) error {
	beforeRef, err := getRefOrEvaluate(ctx, ch.Before.Self())
	if err != nil {
		return fmt.Errorf("evaluate before: %w", err)
	}

	afterRef, err := getRefOrEvaluate(ctx, ch.After.Self())
	if err != nil {
		return fmt.Errorf("evaluate after: %w", err)
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return fmt.Errorf("no buildkit session group in context")
	}

	return MountRef(ctx, beforeRef, bkSessionGroup, func(beforeMount string, _ *mount.Mount) error {
		beforeDir, err := containerdfs.RootPath(beforeMount, ch.Before.Self().Dir)
		if err != nil {
			return err
		}

		return MountRef(ctx, afterRef, bkSessionGroup, func(afterMount string, _ *mount.Mount) error {
			afterDir, err := containerdfs.RootPath(afterMount, ch.After.Self().Dir)
			if err != nil {
				return err
			}

			return fn(beforeDir, afterDir)
		}, mountRefAsReadOnly)
	}, mountRefAsReadOnly)
}

type Changeset struct {
	Before dagql.ObjectResult[*Directory] `field:"true" doc:"The older/lower snapshot to compare against."`
	After  dagql.ObjectResult[*Directory] `field:"true" doc:"The newer/upper snapshot."`

	// used for JSON deserialization, since we can't directly load IDs into
	// objects in UnmarshalJSON
	decoded *changesetJSONEnvelope

	pathsOnce   *sync.Once
	cachedPaths *ChangesetPaths
	pathsErr    error
}

// changesetJSONEnvelope is used for JSON serialization of Changeset
type changesetJSONEnvelope struct {
	BeforeID dagql.ID[*Directory] `json:"beforeId"`
	AfterID  dagql.ID[*Directory] `json:"afterId"`
}

// MarshalJSON implements custom JSON marshaling that stores directory IDs
func (ch *Changeset) MarshalJSON() ([]byte, error) {
	return json.Marshal(changesetJSONEnvelope{
		BeforeID: dagql.NewID[*Directory](ch.Before.ID()),
		AfterID:  dagql.NewID[*Directory](ch.After.ID()),
	})
}

// UnmarshalJSON implements custom JSON unmarshaling that stores IDs for later resolution
func (ch *Changeset) UnmarshalJSON(data []byte) error {
	var env changesetJSONEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	ch.decoded = &env
	ch.pathsOnce = &sync.Once{}
	return nil
}

// ResolveRefs loads the Before/After ObjectResults from stored IDs.
// This must be called after JSON unmarshaling to fully reconstruct the Changeset.
func (ch *Changeset) ResolveRefs(ctx context.Context, srv *dagql.Server) error {
	if ch.decoded == nil {
		return nil
	}
	var err error
	ch.Before, err = ch.decoded.BeforeID.Load(ctx, srv)
	if err != nil {
		return fmt.Errorf("load before: %w", err)
	}
	ch.After, err = ch.decoded.AfterID.Load(ctx, srv)
	if err != nil {
		return fmt.Errorf("load after: %w", err)
	}
	ch.decoded = nil
	return nil
}

// changesetPathSets contains pre-computed sets for efficient O(1) path lookups
type changesetPathSets struct {
	added    map[string]struct{}
	modified map[string]struct{}
	removed  map[string]struct{}
}

// pathSets creates lookup maps for this changeset's paths
func (ch *ChangesetPaths) pathSets() changesetPathSets {
	sets := changesetPathSets{
		added:    make(map[string]struct{}, len(ch.Added)),
		modified: make(map[string]struct{}, len(ch.Modified)),
		removed:  make(map[string]struct{}, len(ch.Removed)),
	}
	for _, p := range ch.Added {
		sets.added[p] = struct{}{}
	}
	for _, p := range ch.Modified {
		sets.modified[p] = struct{}{}
	}
	for _, p := range ch.Removed {
		sets.removed[p] = struct{}{}
	}
	return sets
}

func (*Changeset) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Changeset",
		NonNull:   true,
	}
}

func (*Changeset) TypeDescription() string {
	return "A comparison between two directories representing changes that can be applied."
}

var _ Evaluatable = (*Changeset)(nil)

func (ch *Changeset) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
}

var _ HasPBDefinitions = (*Changeset)(nil)

func (ch *Changeset) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	beforeDefs, err := ch.Before.Self().PBDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	afterDefs, err := ch.After.Self().PBDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	return append(beforeDefs, afterDefs...), nil
}

const ChangesetPatchFilename = "diff.patch"

func (ch *Changeset) IsEmpty(ctx context.Context) (bool, error) {
	if ch.Before.ID().Digest() == ch.After.ID().Digest() {
		return true, nil
	}

	var isEmpty bool
	err := ch.withMountedDirs(ctx, func(beforeDir, afterDir string) error {
		identical, err := directoriesAreIdentical(ctx, beforeDir, afterDir)
		if err != nil {
			return err
		}
		isEmpty = identical
		return nil
	})
	if err != nil {
		return false, err
	}
	return isEmpty, nil
}

func (ch *Changeset) AsPatch(ctx context.Context) (*File, error) {
	beforeRef, err := getRefOrEvaluate(ctx, ch.Before.Self())
	if err != nil {
		return nil, err
	}

	afterRef, err := getRefOrEvaluate(ctx, ch.After.Self())
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}
	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()

	newRef, err := query.BuildkitCache().New(ctx, nil, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.asPatch"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, beforeRef, bkSessionGroup, func(before string, _ *mount.Mount) error {
		beforeDir, err := containerdfs.RootPath(before, ch.Before.Self().Dir)
		if err != nil {
			return err
		}
		return MountRef(ctx, afterRef, bkSessionGroup, func(after string, _ *mount.Mount) error {
			afterDir, err := containerdfs.RootPath(after, ch.After.Self().Dir)
			if err != nil {
				return err
			}
			return MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) (rerr error) {
				beforeMount := filepath.Join(root, "a")
				afterMount := filepath.Join(root, "b")
				if err := os.Mkdir(beforeMount, 0755); err != nil {
					return err
				}
				defer os.RemoveAll(beforeMount)
				if err := os.Mkdir(afterMount, 0755); err != nil {
					return err
				}
				defer os.RemoveAll(afterMount)
				if err := syscall.Mount(beforeDir, beforeMount, "", syscall.MS_BIND, ""); err != nil {
					return fmt.Errorf("mount before to ./a/: %w", err)
				}
				defer syscall.Unmount(beforeMount, syscall.MNT_DETACH)
				if err := syscall.Mount(afterDir, afterMount, "", syscall.MS_BIND, ""); err != nil {
					return fmt.Errorf("mount after to ./b/: %w", err)
				}
				defer syscall.Unmount(afterMount, syscall.MNT_DETACH)

				patchFile, err := os.Create(filepath.Join(root, ChangesetPatchFilename))
				if err != nil {
					return err
				}
				defer patchFile.Close()

				cmd := exec.Command("git", "diff", "--binary", "--no-prefix", "--no-index", "a", "b")
				cmd.Dir = root
				cmd.Stdout = io.MultiWriter(patchFile, stdio.Stdout)
				cmd.Stderr = stdio.Stderr
				if err := cmd.Run(); err != nil {
					var exitErr *exec.ExitError
					// Check if it's exit code 1, which is expected for git diff when files differ
					if errors.As(err, &exitErr) && exitErr.ExitCode() != 1 {
						// NB: we could technically populate a buildkit.ExecError here, but that
						// feels like it leaks implementation details; "exit status 128" isn't
						// exactly clear
						return fmt.Errorf("failed to generate patch: %w", err)
					}
				}
				return nil
			})
		}, mountRefAsReadOnly)
	}, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return &File{
		Result:   snap,
		File:     ChangesetPatchFilename,
		Platform: query.Platform(),
	}, nil
}

func (ch *Changeset) Export(ctx context.Context, destPath string) (rerr error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute paths: %w", err)
	}

	dir, err := ch.Before.Self().Diff(ctx, ch.After.Self())
	if err != nil {
		return err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export changeset to host %s", destPath))
	defer telemetry.EndWithCause(span, &rerr)

	root, closer, err := mountObj(ctx, dir)
	if err != nil {
		return fmt.Errorf("failed to mount directory: %w", err)
	}
	defer closer(false)

	root, err = containerdfs.RootPath(root, dir.Dir)
	if err != nil {
		return err
	}

	return bk.LocalDirExport(ctx, root, destPath, true, paths.Removed)
}

type ChangeType int

const (
	ChangeTypeAdded ChangeType = iota
	ChangeTypeModified
	ChangeTypeRemoved
)

type Conflict struct {
	Path  string
	Self  ChangeType
	Other ChangeType
	Err   error
}

var (
	ErrAddedTwice      = errors.New("path added in both changesets")
	ErrModifiedTwice   = errors.New("path modified in both changesets")
	ErrModifiedRemoved = errors.New("path modified in one changeset and removed in the other")
)

type Conflicts []Conflict

func (conflicts Conflicts) Error() (err error) {
	for _, c := range conflicts {
		err = errors.Join(err, fmt.Errorf("conflict between changesets at path %q: %w", c.Path, c.Err))
	}
	return err
}

func (conflicts Conflicts) IsEmpty() bool {
	return len(conflicts) == 0
}

// CheckConflicts detects conflicts using pre-computed path sets for O(1) lookups
func (ch *ChangesetPaths) CheckConflicts(other *ChangesetPaths) Conflicts {
	otherSets := other.pathSets()
	return ch.checkConflictsWithSets(otherSets)
}

func (ch *ChangesetPaths) checkConflictsWithSets(otherSets changesetPathSets) Conflicts {
	var conflicts Conflicts
	for _, addedPath := range ch.Added {
		if _, exists := otherSets.added[addedPath]; exists {
			conflicts = append(conflicts, Conflict{
				Path:  addedPath,
				Self:  ChangeTypeAdded,
				Other: ChangeTypeAdded,
				Err:   ErrAddedTwice,
			})
		}
	}
	for _, modifiedPath := range ch.Modified {
		if _, exists := otherSets.modified[modifiedPath]; exists {
			conflicts = append(conflicts, Conflict{
				Path:  modifiedPath,
				Self:  ChangeTypeModified,
				Other: ChangeTypeModified,
				Err:   ErrModifiedTwice,
			})
			continue
		}
		if _, exists := otherSets.removed[modifiedPath]; exists {
			conflicts = append(conflicts, Conflict{
				Path:  modifiedPath,
				Self:  ChangeTypeModified,
				Other: ChangeTypeRemoved,
				Err:   ErrModifiedRemoved,
			})
		}
	}
	for _, removedPath := range ch.Removed {
		if _, exists := otherSets.modified[removedPath]; exists {
			conflicts = append(conflicts, Conflict{
				Path:  removedPath,
				Self:  ChangeTypeRemoved,
				Other: ChangeTypeModified,
				Err:   ErrModifiedRemoved,
			})
		}
	}
	return conflicts
}

type WithChangesetMergeConflict int

const (
	FailOnConflict WithChangesetMergeConflict = iota
	LeaveConflictsOnConflict
	PreferOursOnConflict
	PreferTheirsOnConflict
)

// ErrBinaryConflict is returned when a binary file has conflicts and the
// strategy is LEAVE_CONFLICTS (binary files cannot have conflict markers).
var ErrBinaryConflict = errors.New("binary file has conflicts")

// WithChangeset merges another changeset into this one using git-based 3-way merge.
// The onConflictStrategy determines how conflicts are handled:
//   - FailOnConflict: fail if any file-level conflicts are detected
//   - LeaveConflictsOnConflict: leave conflict markers in conflicting files (fails for binary)
//   - PreferOursOnConflict: use our version for conflicts
//   - PreferTheirsOnConflict: use their version for conflicts
func (ch *Changeset) WithChangeset(
	ctx context.Context,
	other *Changeset,
	onConflictStrategy WithChangesetMergeConflict,
) (*Changeset, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	// For FAIL strategy, detect conflicts at file level first
	if onConflictStrategy == FailOnConflict {
		ourPaths, err := ch.ComputePaths(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute our paths: %w", err)
		}
		theirPaths, err := other.ComputePaths(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute their paths: %w", err)
		}
		if conflicts := ourPaths.CheckConflicts(theirPaths); !conflicts.IsEmpty() {
			return nil, conflicts.Error()
		}
	}

	// Generate patches for both changesets
	ourPatch, err := ch.AsPatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate our patch: %w", err)
	}
	theirPatch, err := other.AsPatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate their patch: %w", err)
	}

	// Merge "before" directories from both changesets
	var before dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &before,
		dagql.Selector{Field: "directory"},
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("")},
				{Name: "source", Value: dagql.NewID[*Directory](ch.Before.ID())},
			},
		},
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("")},
				{Name: "source", Value: dagql.NewID[*Directory](other.Before.ID())},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("merge before directories: %w", err)
	}

	// Perform git-based merge - this modifies the base directory in place
	afterDir, err := gitMergeWithPatches(ctx, before.Self(), ourPatch, theirPatch, onConflictStrategy)
	if err != nil {
		return nil, err
	}

	// Get the immutable ref ID to create a directory via the internal query
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	refID := afterDir.Result.ID()

	// Create the after directory using the internal __immutableRef query
	var after dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &after,
		dagql.Selector{
			Field: "__immutableRef",
			Args: []dagql.NamedInput{
				{Name: "ref", Value: dagql.NewString(refID)},
			},
		},
	); err != nil {
		// Fallback: create a new directory and merge the result
		scratchDir, err := NewScratchDirectory(ctx, query.Platform())
		if err != nil {
			return nil, fmt.Errorf("create scratch directory: %w", err)
		}
		scratchDir.Result = afterDir.Result
		scratchDir.Dir = afterDir.Dir

		// Use a simpler approach: merge the result with an empty directory
		if err := srv.Select(ctx, before, &after,
			dagql.Selector{
				Field: "diff",
				Args: []dagql.NamedInput{
					{Name: "other", Value: dagql.NewID[*Directory](before.ID())},
				},
			},
		); err != nil {
			return nil, fmt.Errorf("create after directory: %w", err)
		}
	}

	return NewChangeset(ctx, before, after)
}

// gitMergeWithPatches performs a git-based 3-way merge of two patches.
// It creates a temporary git repository, commits the base, creates two branches
// with each patch applied, and merges them with the specified strategy.
func gitMergeWithPatches(
	ctx context.Context,
	base *Directory,
	ourPatch, theirPatch *File,
	strategy WithChangesetMergeConflict,
) (*Directory, error) {
	baseRef, err := getRefOrEvaluate(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("evaluate base: %w", err)
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}
	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()

	// Read patch contents
	ourPatchContent, err := ourPatch.Contents(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read our patch: %w", err)
	}
	theirPatchContent, err := theirPatch.Contents(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read their patch: %w", err)
	}

	// Create a new ref for the merge result
	newRef, err := query.BuildkitCache().New(ctx, baseRef, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.withChangeset git merge"))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		workDir, err := containerdfs.RootPath(root, base.Dir)
		if err != nil {
			return err
		}

		// Initialize git repository with base commit
		if err := initGitRepo(ctx, workDir, stdio); err != nil {
			return err
		}

		// Create 'ours' branch with our patch
		if err := createBranchWithPatch(ctx, workDir, stdio, "ours", ourPatchContent); err != nil {
			return err
		}

		// Go back to base and create 'theirs' branch with their patch
		if err := checkoutBaseBranch(ctx, workDir, stdio); err != nil {
			return err
		}
		if err := createBranchWithPatch(ctx, workDir, stdio, "theirs", theirPatchContent); err != nil {
			return err
		}

		// Switch to 'ours' and merge 'theirs'
		if err := runGitCommand(ctx, workDir, stdio, "checkout", "ours"); err != nil {
			return fmt.Errorf("git checkout ours for merge: %w", err)
		}

		mergeErr := runGitCommand(ctx, workDir, stdio, "merge", "theirs", "--no-edit", "--no-commit")

		// Always check for unmerged files - merge might fail OR succeed with unresolved conflicts
		conflictFiles, err := getConflictedFiles(ctx, workDir)
		if err != nil {
			return fmt.Errorf("check conflicts: %w", err)
		}

		if len(conflictFiles) > 0 || mergeErr != nil {
			if err := handleMergeConflicts(ctx, workDir, stdio, conflictFiles, strategy, mergeErr); err != nil {
				return err
			}
		}

		// Clean up .git directory
		return os.RemoveAll(filepath.Join(workDir, ".git"))
	})
	if err != nil {
		return nil, err
	}

	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}

	return &Directory{
		Result:   snap,
		Dir:      base.Dir,
		Platform: query.Platform(),
	}, nil
}

// runGitCommand runs a git command in the specified directory.
func runGitCommand(ctx context.Context, dir string, stdio telemetry.SpanStreams, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = stdio.Stdout
	cmd.Stderr = stdio.Stderr
	return cmd.Run()
}

// runGitApply applies a patch using git apply.
func runGitApply(ctx context.Context, dir string, stdio telemetry.SpanStreams, patch string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", "--allow-empty", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(patch)
	cmd.Stdout = stdio.Stdout
	cmd.Stderr = stdio.Stderr
	return cmd.Run()
}

// initGitRepo initializes a git repository with user config and commits all files as base.
func initGitRepo(ctx context.Context, dir string, stdio telemetry.SpanStreams) error {
	if err := runGitCommand(ctx, dir, stdio, "init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	if err := runGitCommand(ctx, dir, stdio, "config", "user.email", "dagger@localhost"); err != nil {
		return fmt.Errorf("git config email: %w", err)
	}
	if err := runGitCommand(ctx, dir, stdio, "config", "user.name", "Dagger"); err != nil {
		return fmt.Errorf("git config name: %w", err)
	}
	if err := runGitCommand(ctx, dir, stdio, "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := runGitCommand(ctx, dir, stdio, "commit", "--allow-empty", "-m", "base"); err != nil {
		return fmt.Errorf("git commit base: %w", err)
	}
	return nil
}

// createBranchWithPatch creates a branch, applies the patch, and commits.
func createBranchWithPatch(ctx context.Context, dir string, stdio telemetry.SpanStreams, branchName string, patchContent []byte) error {
	if err := runGitCommand(ctx, dir, stdio, "checkout", "-b", branchName); err != nil {
		return fmt.Errorf("git checkout %s: %w", branchName, err)
	}
	if len(patchContent) > 0 {
		if err := runGitApply(ctx, dir, stdio, string(patchContent)); err != nil {
			return fmt.Errorf("apply %s patch: %w", branchName, err)
		}
		if err := runGitCommand(ctx, dir, stdio, "add", "-A"); err != nil {
			return fmt.Errorf("git add %s: %w", branchName, err)
		}
		if err := runGitCommand(ctx, dir, stdio, "commit", "--allow-empty", "-m", branchName); err != nil {
			return fmt.Errorf("git commit %s: %w", branchName, err)
		}
	}
	return nil
}

// checkoutBaseBranch checks out the base branch (master or main).
func checkoutBaseBranch(ctx context.Context, dir string, stdio telemetry.SpanStreams) error {
	if err := runGitCommand(ctx, dir, stdio, "checkout", "master"); err != nil {
		if err := runGitCommand(ctx, dir, stdio, "checkout", "main"); err != nil {
			return fmt.Errorf("git checkout base: %w", err)
		}
	}
	return nil
}

// handleMergeConflicts handles conflicts based on the merge strategy.
func handleMergeConflicts(ctx context.Context, dir string, stdio telemetry.SpanStreams, conflictFiles []string, strategy WithChangesetMergeConflict, mergeErr error) error {
	switch strategy {
	case LeaveConflictsOnConflict:
		for _, file := range conflictFiles {
			if isBinaryFile(ctx, dir, file) {
				return fmt.Errorf("%w: %s", ErrBinaryConflict, file)
			}
		}
		if err := runGitCommand(ctx, dir, stdio, "add", "-A"); err != nil {
			return fmt.Errorf("git add after merge: %w", err)
		}

	case PreferOursOnConflict:
		if err := resolveConflictsPreferSide(ctx, dir, stdio, conflictFiles, true); err != nil {
			return err
		}

	case PreferTheirsOnConflict:
		if err := resolveConflictsPreferSide(ctx, dir, stdio, conflictFiles, false); err != nil {
			return err
		}

	default:
		return fmt.Errorf("git merge failed with conflicts: %w", mergeErr)
	}
	return nil
}

// resolveConflictsPreferSide resolves all merge conflicts by preferring one side.
// If preferOurs is true, uses our version; otherwise uses their version.
func resolveConflictsPreferSide(ctx context.Context, dir string, stdio telemetry.SpanStreams, conflictFiles []string, preferOurs bool) error {
	side := "--theirs"
	sideName := "theirs"
	if preferOurs {
		side = "--ours"
		sideName = "ours"
	}

	// Resolve content conflicts by checking out the preferred side
	for _, file := range conflictFiles {
		if err := runGitCommand(ctx, dir, stdio, "checkout", side, "--", file); err != nil {
			// File might have been deleted on this side - that's fine
			continue
		}
	}

	// Handle files deleted on the preferred side
	deletedFiles, err := getDeletedFiles(ctx, dir, preferOurs)
	if err == nil {
		for _, file := range deletedFiles {
			_ = runGitCommand(ctx, dir, stdio, "rm", "--force", "--", file)
		}
	}

	if err := runGitCommand(ctx, dir, stdio, "add", "-A"); err != nil {
		return fmt.Errorf("git add after prefer %s: %w", sideName, err)
	}
	return nil
}

// getConflictedFiles returns a list of files with unresolved conflicts.
func getConflictedFiles(ctx context.Context, dir string) ([]string, error) {
	// Use git ls-files -u to get unmerged files
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-u", "--full-name")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	// Parse output - each line is "mode hash stage\tfilename"
	// We want unique filenames
	seen := make(map[string]struct{})
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			file := parts[1]
			if _, ok := seen[file]; !ok {
				seen[file] = struct{}{}
				files = append(files, file)
			}
		}
	}
	return files, nil
}

// getDeletedFiles returns files that were deleted in one branch during a merge conflict.
// If preferOurs is true, returns files deleted in ours (to be removed when preferring ours).
// If preferOurs is false, returns files deleted in theirs (to be removed when preferring theirs).
func getDeletedFiles(ctx context.Context, dir string, preferOurs bool) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-u", "--full-name")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}

	// Track which files have which stages
	type stages struct {
		hasOurs   bool // stage 2
		hasTheirs bool // stage 3
	}
	fileStages := make(map[string]*stages)

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// Format: "mode hash stage\tfilename"
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		stage := parts[2]
		tabIdx := strings.Index(line, "\t")
		if tabIdx == -1 {
			continue
		}
		file := line[tabIdx+1:]

		if fileStages[file] == nil {
			fileStages[file] = &stages{}
		}
		switch stage {
		case "2":
			fileStages[file].hasOurs = true
		case "3":
			fileStages[file].hasTheirs = true
		}
	}

	var deleted []string
	for file, s := range fileStages {
		if preferOurs {
			// Deleted in ours: has theirs (stage 3) but not ours (stage 2)
			if s.hasTheirs && !s.hasOurs {
				deleted = append(deleted, file)
			}
		} else {
			// Deleted in theirs: has ours (stage 2) but not theirs (stage 3)
			if s.hasOurs && !s.hasTheirs {
				deleted = append(deleted, file)
			}
		}
	}
	return deleted, nil
}

// isBinaryFile checks if a file is binary by checking the git attribute or file content.
func isBinaryFile(ctx context.Context, dir, file string) bool {
	// Check if git considers it binary using diff
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--numstat", "--", file)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil && strings.HasPrefix(string(out), "-\t-\t") {
		return true
	}

	// Also check the working tree file for NUL bytes (common binary indicator)
	filePath := filepath.Join(dir, file)
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	// Read first 8KB to check for NUL bytes
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

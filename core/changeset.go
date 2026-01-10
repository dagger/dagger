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
	bksession "github.com/dagger/dagger/internal/buildkit/session"
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
	PreferOursOnConflict
	PreferTheirsOnConflict
)

// WithChangeset merges another changeset into this one using file-level merge.
// Changes from both changesets are applied, with conflicts resolved at the file level.
// The onConflictStrategy determines how conflicts are handled:
//   - FailOnConflict: fail if any file-level conflicts are detected
//   - PreferOursOnConflict: use our version for conflicting files
//   - PreferTheirsOnConflict: use their version for conflicting files
func (ch *Changeset) WithChangeset(
	ctx context.Context,
	other *Changeset,
	onConflictStrategy WithChangesetMergeConflict,
) (*Changeset, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	// Always compute paths for conflict detection and file-level merge
	ourPaths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute our paths: %w", err)
	}
	theirPaths, err := other.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute their paths: %w", err)
	}

	// Check for conflicts
	conflicts := ourPaths.CheckConflicts(theirPaths)
	if onConflictStrategy == FailOnConflict && !conflicts.IsEmpty() {
		return nil, conflicts.Error()
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

	// Perform file-level merge using git apply (no repository needed)
	afterDir, err := applyChangesWithStrategy(ctx, before.Self(), ourPatch, theirPatch,
		ourPaths, theirPaths, ch.After.Self(), other.After.Self(),
		conflicts, onConflictStrategy)
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

// applyChangesWithStrategy applies changesets using git apply without creating a repository.
// For no conflicts, it applies both patches sequentially.
// For PreferOurs/PreferTheirs, it applies the preferred patch and copies non-conflicting files.
func applyChangesWithStrategy(
	ctx context.Context,
	base *Directory,
	ourPatch, theirPatch *File,
	ourPaths, theirPaths *ChangesetPaths,
	ourAfter, theirAfter *Directory,
	conflicts Conflicts,
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

	// Build conflict set for quick lookups
	conflictSet := buildConflictSet(conflicts)

	// Create a new ref for the merge result
	newRef, err := query.BuildkitCache().New(ctx, baseRef, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.withChangeset file-level merge"))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		workDir, err := containerdfs.RootPath(root, base.Dir)
		if err != nil {
			return err
		}

		switch strategy {
		case FailOnConflict:
			// No conflicts (already checked), apply both patches sequentially
			if len(ourPatchContent) > 0 {
				if err := runGitApply(ctx, workDir, stdio, string(ourPatchContent)); err != nil {
					return fmt.Errorf("apply our patch: %w", err)
				}
			}
			if len(theirPatchContent) > 0 {
				if err := runGitApply(ctx, workDir, stdio, string(theirPatchContent)); err != nil {
					return fmt.Errorf("apply their patch: %w", err)
				}
			}

		case PreferOursOnConflict:
			// Apply our patch first
			if len(ourPatchContent) > 0 {
				if err := runGitApply(ctx, workDir, stdio, string(ourPatchContent)); err != nil {
					return fmt.Errorf("apply our patch: %w", err)
				}
			}
			// Copy non-conflicting files from their After directory
			if err := copyNonConflictingChanges(ctx, theirAfter, workDir, theirPaths, conflictSet, bkSessionGroup); err != nil {
				return fmt.Errorf("copy their non-conflicting changes: %w", err)
			}

		case PreferTheirsOnConflict:
			// Apply their patch first
			if len(theirPatchContent) > 0 {
				if err := runGitApply(ctx, workDir, stdio, string(theirPatchContent)); err != nil {
					return fmt.Errorf("apply their patch: %w", err)
				}
			}
			// Copy non-conflicting files from our After directory
			if err := copyNonConflictingChanges(ctx, ourAfter, workDir, ourPaths, conflictSet, bkSessionGroup); err != nil {
				return fmt.Errorf("copy our non-conflicting changes: %w", err)
			}

		default:
			return fmt.Errorf("unsupported conflict strategy: %d", strategy)
		}

		return nil
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

// buildConflictSet creates a set of conflicting paths for quick lookups.
func buildConflictSet(conflicts Conflicts) map[string]struct{} {
	set := make(map[string]struct{}, len(conflicts))
	for _, c := range conflicts {
		set[c.Path] = struct{}{}
	}
	return set
}

// copyNonConflictingChanges copies files from the source directory for non-conflicting changes.
func copyNonConflictingChanges(
	ctx context.Context,
	sourceDir *Directory,
	targetPath string,
	paths *ChangesetPaths,
	conflictSet map[string]struct{},
	bkSessionGroup bksession.Group,
) error {
	sourceRef, err := getRefOrEvaluate(ctx, sourceDir)
	if err != nil {
		return fmt.Errorf("evaluate source: %w", err)
	}

	return MountRef(ctx, sourceRef, bkSessionGroup, func(sourceRoot string, _ *mount.Mount) error {
		sourcePath, err := containerdfs.RootPath(sourceRoot, sourceDir.Dir)
		if err != nil {
			return err
		}

		// Copy added files that don't conflict
		for _, file := range paths.Added {
			if _, conflicts := conflictSet[file]; conflicts {
				continue
			}
			if err := changesetCopyFileOrDir(filepath.Join(sourcePath, file), filepath.Join(targetPath, file)); err != nil {
				return fmt.Errorf("copy added %s: %w", file, err)
			}
		}

		// Copy modified files that don't conflict
		for _, file := range paths.Modified {
			if _, conflicts := conflictSet[file]; conflicts {
				continue
			}
			if err := changesetCopyFileOrDir(filepath.Join(sourcePath, file), filepath.Join(targetPath, file)); err != nil {
				return fmt.Errorf("copy modified %s: %w", file, err)
			}
		}

		// Delete removed files that don't conflict
		for _, file := range paths.Removed {
			if _, conflicts := conflictSet[file]; conflicts {
				continue
			}
			targetFile := filepath.Join(targetPath, file)
			if err := os.RemoveAll(targetFile); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", file, err)
			}
		}

		return nil
	}, mountRefAsReadOnly)
}

// changesetCopyFileOrDir copies a file or directory from src to dst.
func changesetCopyFileOrDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if srcInfo.IsDir() {
		return changesetCopyDir(src, dst)
	}
	return changesetCopyFile(src, dst)
}

// changesetCopyFile copies a single file from src to dst.
func changesetCopyFile(src, dst string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// changesetCopyDir recursively copies a directory from src to dst.
func changesetCopyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := changesetCopyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := changesetCopyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// runGitApply applies a patch using git apply (works without a repository).
func runGitApply(ctx context.Context, dir string, stdio telemetry.SpanStreams, patch string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", "--allow-empty", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(patch)
	cmd.Stdout = stdio.Stdout
	cmd.Stderr = stdio.Stderr
	return cmd.Run()
}

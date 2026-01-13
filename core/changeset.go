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
	"sync"
	"syscall"

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

// ModifyDeletePaths returns the paths of modify/delete conflicts.
func (conflicts Conflicts) ModifyDeletePaths() []string {
	var paths []string
	for _, c := range conflicts {
		if errors.Is(c.Err, ErrModifiedRemoved) {
			paths = append(paths, c.Path)
		}
	}
	return paths
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
	// FailEarlyOnConflict fails before attempting merge if file-level conflicts are detected.
	FailEarlyOnConflict WithChangesetMergeConflict = iota
	// FailOnConflict attempts the merge and fails if git merge fails due to conflicts.
	FailOnConflict
	// LeaveConflictMarkers lets git create conflict markers in files. For modify/delete
	// conflicts, keeps the modified version. Fails on binary conflicts.
	LeaveConflictMarkers
	// PreferOursOnConflict uses -X ours strategy and resolves modify/delete by preferring ours.
	PreferOursOnConflict
	// PreferTheirsOnConflict uses -X theirs strategy and resolves modify/delete by preferring theirs.
	PreferTheirsOnConflict
)

// WithChangesetsMergeConflict specifies how to handle conflicts when merging multiple changesets
// using git's octopus merge strategy. Only FAIL_EARLY and FAIL are supported (no -X ours/theirs).
type WithChangesetsMergeConflict int

const (
	// FailEarlyOnConflicts fails before attempting merge if file-level conflicts are detected.
	FailEarlyOnConflicts WithChangesetsMergeConflict = iota
	// FailOnConflicts attempts the merge and fails if git merge fails due to conflicts.
	FailOnConflicts
)

// WithChangeset merges another changeset into this one using git-based 3-way merge.
// The onConflictStrategy determines how conflicts are handled:
//   - FailEarlyOnConflict: fail before merge if file-level conflicts are detected
//   - FailOnConflict: attempt merge, fail if git merge fails
//   - LeaveConflictMarkers: let git create conflict markers, keep modified for modify/delete
//   - PreferOursOnConflict: use -X ours strategy
//   - PreferTheirsOnConflict: use -X theirs strategy
func (ch *Changeset) WithChangeset(
	ctx context.Context,
	other *Changeset,
	onConflictStrategy WithChangesetMergeConflict,
) (*Changeset, error) {
	// Compute paths for both changesets - needed for conflict detection and resolution
	ourPaths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute our paths: %w", err)
	}
	theirPaths, err := other.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute their paths: %w", err)
	}

	// Compute file-level conflicts
	conflicts := ourPaths.CheckConflicts(theirPaths)

	// FAIL_EARLY: check for conflicts before attempting merge
	if onConflictStrategy == FailEarlyOnConflict && !conflicts.IsEmpty() {
		return nil, conflicts.Error()
	}

	// Merge "before" directories from both changesets
	before, err := mergeBeforeDirectories(ctx, ch, other)
	if err != nil {
		return nil, err
	}

	ourPatch, err := ch.AsPatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate our patch: %w", err)
	}
	theirPatch, err := other.AsPatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate their patch: %w", err)
	}

	// Perform git-based merge
	afterDir, err := gitMergeWithPatches(ctx,
		before.Self(),
		ourPatch, theirPatch,
		ourPaths.AllRemoved, theirPaths.AllRemoved,
		conflicts,
		onConflictStrategy,
	)
	if err != nil {
		return nil, err
	}

	return newChangesetFromMerge(ctx, before, afterDir)
}

// WithChangesets merges multiple changesets into this one using git's octopus merge strategy.
// The onConflictStrategy determines how conflicts are handled:
//   - FailEarlyOnConflicts: fail before merge if file-level conflicts are detected
//   - FailOnConflicts: attempt merge, fail if git merge fails
func (ch *Changeset) WithChangesets(
	ctx context.Context,
	others []*Changeset,
	onConflictStrategy WithChangesetsMergeConflict,
) (*Changeset, error) {
	// Empty array: return unchanged changeset
	if len(others) == 0 {
		return ch, nil
	}

	// Single element: delegate to WithChangeset (more efficient 2-way merge)
	if len(others) == 1 {
		// Map the strategy to the 2-way merge equivalent
		var twoWayStrategy WithChangesetMergeConflict
		switch onConflictStrategy {
		case FailEarlyOnConflicts:
			twoWayStrategy = FailEarlyOnConflict
		default:
			twoWayStrategy = FailOnConflict
		}
		return ch.WithChangeset(ctx, others[0], twoWayStrategy)
	}

	// FAIL_EARLY: check for conflicts between all pairs before attempting merge
	if onConflictStrategy == FailEarlyOnConflicts {
		if err := checkAllPairwiseConflicts(ctx, ch, others); err != nil {
			return nil, err
		}
	}

	// Merge "before" directories from all changesets
	before, err := mergeBeforeDirectories(ctx, ch, others...)
	if err != nil {
		return nil, err
	}

	// Get patches for all changesets
	ourPatch, err := ch.AsPatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("get our patch: %w", err)
	}

	otherPatches := make([]*File, len(others))
	for i, other := range others {
		patch, err := other.AsPatch(ctx)
		if err != nil {
			return nil, fmt.Errorf("get patch for changeset %d: %w", i, err)
		}
		otherPatches[i] = patch
	}

	// Perform the octopus merge
	afterDir, err := gitOctopusMergeWithPatches(ctx, before.Self(), ourPatch, otherPatches)
	if err != nil {
		return nil, err
	}

	return newChangesetFromMerge(ctx, before, afterDir)
}

// mergeBeforeDirectories merges the "before" directories from all changesets.
func mergeBeforeDirectories(ctx context.Context, ch *Changeset, others ...*Changeset) (dagql.ObjectResult[*Directory], error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}

	selectors := []dagql.Selector{
		{Field: "directory"},
		withDirectorySelector(ch.Before.ID()),
	}
	for _, other := range others {
		selectors = append(selectors, withDirectorySelector(other.Before.ID()))
	}

	var before dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &before, selectors...); err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("merge before directories: %w", err)
	}
	return before, nil
}

// withDirectorySelector creates a dagql selector for withDirectory at root path.
func withDirectorySelector(dirID *call.ID) dagql.Selector {
	return dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("")},
			{Name: "source", Value: dagql.NewID[*Directory](dirID)},
		},
	}
}

// newChangesetFromMerge creates a new changeset from merged before directory and after directory result.
func newChangesetFromMerge(ctx context.Context, before dagql.ObjectResult[*Directory], afterDir *Directory) (*Changeset, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var after dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &after,
		dagql.Selector{
			Field: "__immutableRef",
			Args: []dagql.NamedInput{
				{Name: "ref", Value: dagql.NewString(afterDir.Result.ID())},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("create after directory: %w", err)
	}

	return NewChangeset(ctx, before, after)
}

// checkAllPairwiseConflicts checks for conflicts between all pairs of changesets.
func checkAllPairwiseConflicts(ctx context.Context, ch *Changeset, others []*Changeset) error {
	ourPaths, err := ch.ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute our paths: %w", err)
	}

	otherPaths := make([]*ChangesetPaths, len(others))
	for i, other := range others {
		paths, err := other.ComputePaths(ctx)
		if err != nil {
			return fmt.Errorf("compute paths for changeset %d: %w", i, err)
		}
		otherPaths[i] = paths
	}

	// Check conflicts between our changeset and each other
	for i, paths := range otherPaths {
		conflicts := ourPaths.CheckConflicts(paths)
		if !conflicts.IsEmpty() {
			return fmt.Errorf("conflict with changeset %d: %w", i, conflicts.Error())
		}
	}

	// Check conflicts between each pair of others
	for i := 0; i < len(otherPaths); i++ {
		for j := i + 1; j < len(otherPaths); j++ {
			conflicts := otherPaths[i].CheckConflicts(otherPaths[j])
			if !conflicts.IsEmpty() {
				return fmt.Errorf("conflict between changesets %d and %d: %w", i, j, conflicts.Error())
			}
		}
	}

	return nil
}

// gitMergeWithPatches performs a git-based 3-way merge of two patches.
// It creates a temporary git repository, commits the base, creates two branches
// with each patch applied, and merges them with the specified strategy.
// modifyDeleteConflicts contains paths that have modify/delete conflicts (the only
// conflicts not auto-resolved by -X ours/theirs).
func gitMergeWithPatches(
	ctx context.Context,
	base *Directory,
	ourPatch, theirPatch *File,
	ourRemoved, theirRemoved []string,
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
		if err := initGitRepo(ctx, workDir); err != nil {
			return err
		}

		// Create 'ours' branch with our patch
		if err := createBranchWithPatch(ctx, workDir, "ours", ourPatchContent); err != nil {
			return err
		}

		// Create 'theirs' branch from base commit (HEAD~1) with their patch
		if err := createBranchWithPatch(ctx, workDir, "theirs", theirPatchContent, "HEAD~1"); err != nil {
			return err
		}

		// Switch to 'ours' and merge 'theirs' with appropriate strategy
		if err := runGit(ctx, workDir, "checkout", "ours"); err != nil {
			return err
		}

		// Build merge command with strategy options
		mergeArgs := []string{"merge", "--no-edit", "--no-commit"}
		switch strategy {
		case PreferOursOnConflict:
			mergeArgs = append(mergeArgs, "-X", "ours")
		case PreferTheirsOnConflict:
			mergeArgs = append(mergeArgs, "-X", "theirs")
		}
		mergeArgs = append(mergeArgs, "theirs")

		mergeErr := runGit(ctx, workDir, mergeArgs...)

		// Handle conflicts based on strategy
		switch strategy {
		case FailOnConflict:
			// Fail if merge had any conflicts (including modify/delete)
			if mergeErr != nil {
				return mergeErr
			}

		case LeaveConflictMarkers, PreferOursOnConflict, PreferTheirsOnConflict:
			// Handle modify/delete conflicts based on strategy
			modifyDeleteConflicts := conflicts.ModifyDeletePaths()
			if len(modifyDeleteConflicts) > 0 {
				if err := resolveModifyDeleteConflicts(ctx, workDir, modifyDeleteConflicts, strategy, ourRemoved, theirRemoved); err != nil {
					return err
				}
			}

		default:
			// FailEarlyOnConflict or unknown strategy - fail if merge had issues
			if mergeErr != nil {
				return mergeErr
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

// gitOctopusMergeWithPatches performs a git octopus merge of multiple patches.
// It creates a temporary git repository, commits the base, creates a branch for
// each patch, and merges them all using git's octopus merge strategy.
func gitOctopusMergeWithPatches(
	ctx context.Context,
	base *Directory,
	ourPatch *File,
	otherPatches []*File,
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

	// Read all patch contents
	ourPatchContent, err := ourPatch.Contents(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read our patch: %w", err)
	}

	otherPatchContents := make([][]byte, len(otherPatches))
	for i, patch := range otherPatches {
		content, err := patch.Contents(ctx, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("read patch %d: %w", i, err)
		}
		otherPatchContents[i] = content
	}

	// Create a new ref for the merge result
	newRef, err := query.BuildkitCache().New(ctx, baseRef, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.withChangesets git octopus merge"))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		workDir, err := containerdfs.RootPath(root, base.Dir)
		if err != nil {
			return err
		}

		// Initialize git repository with base commit
		if err := initGitRepo(ctx, workDir); err != nil {
			return err
		}

		// Create 'ours' branch with our patch
		if err := createBranchWithPatch(ctx, workDir, "ours", ourPatchContent); err != nil {
			return err
		}

		// Create a branch for each other changeset from base commit (HEAD~1)
		branchNames := make([]string, len(otherPatchContents))
		for i, patchContent := range otherPatchContents {
			branchName := fmt.Sprintf("branch_%d", i)
			branchNames[i] = branchName
			if err := createBranchWithPatch(ctx, workDir, branchName, patchContent, "HEAD~1"); err != nil {
				return err
			}
		}

		// Switch to 'ours' branch for the merge
		if err := runGit(ctx, workDir, "checkout", "ours"); err != nil {
			return err
		}

		// Build octopus merge command: git merge --no-edit --no-commit branch_0 branch_1 ...
		mergeArgs := []string{"merge", "--no-edit", "--no-commit"}
		mergeArgs = append(mergeArgs, branchNames...)

		if err := runGit(ctx, workDir, mergeArgs...); err != nil {
			return err
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

// runGit executes a git command in the specified directory.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=/dev/null",
		"GIT_AUTHOR_NAME=Dagger",
		"GIT_AUTHOR_EMAIL=dagger@localhost",
		"GIT_COMMITTER_NAME=Dagger",
		"GIT_COMMITTER_EMAIL=dagger@localhost",
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return nil
}

// gitApplyPatch applies a patch using git apply via a temp file.
func gitApplyPatch(ctx context.Context, dir string, patch []byte) error {
	if len(patch) == 0 {
		return nil
	}
	patchFile := filepath.Join(dir, ".dagger-patch")
	if err := os.WriteFile(patchFile, patch, 0600); err != nil {
		return fmt.Errorf("write patch file: %w", err)
	}
	defer os.Remove(patchFile)
	return runGit(ctx, dir, "apply", "--allow-empty", patchFile)
}

// initGitRepo initializes a git repository and commits all files as base.
func initGitRepo(ctx context.Context, dir string) error {
	if err := runGit(ctx, dir, "init"); err != nil {
		return err
	}
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	return runGit(ctx, dir, "commit", "--allow-empty", "-m", "base")
}

// createBranchWithPatch creates a branch, applies the patch, and commits.
// If startPoint is provided, the branch is created from that commit.
func createBranchWithPatch(ctx context.Context, dir string, branchName string, patchContent []byte, startPoint ...string) error {
	checkoutArgs := []string{"checkout", "-b", branchName}
	if len(startPoint) > 0 {
		checkoutArgs = append(checkoutArgs, startPoint[0])
	}
	if err := runGit(ctx, dir, checkoutArgs...); err != nil {
		return err
	}
	if len(patchContent) > 0 {
		if err := gitApplyPatch(ctx, dir, patchContent); err != nil {
			return fmt.Errorf("apply %s patch: %w", branchName, err)
		}
	}
	// Always commit (even if empty) to ensure consistent commit structure
	// This is needed so that HEAD~1 references work correctly
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	if err := runGit(ctx, dir, "commit", "--allow-empty", "-m", branchName); err != nil {
		return err
	}
	return nil
}

// resolveModifyDeleteConflicts resolves modify/delete conflicts based on strategy.
// For PREFER_OURS/THEIRS: prefer that side (delete if it deleted, keep if it modified)
// For LEAVE_CONFLICT_MARKERS: always keep the modified version
func resolveModifyDeleteConflicts(ctx context.Context, dir string, conflictFiles []string, strategy WithChangesetMergeConflict, ourRemoved, theirRemoved []string) error {
	if len(conflictFiles) == 0 {
		return nil
	}

	// Build sets for quick lookup
	ourRemovedSet := make(map[string]struct{}, len(ourRemoved))
	for _, p := range ourRemoved {
		ourRemovedSet[p] = struct{}{}
	}
	theirRemovedSet := make(map[string]struct{}, len(theirRemoved))
	for _, p := range theirRemoved {
		theirRemovedSet[p] = struct{}{}
	}

	for _, file := range conflictFiles {
		_, ourDeleted := ourRemovedSet[file]
		_, theirDeleted := theirRemovedSet[file]

		// Determine which side to use based on strategy
		var useOurs bool
		switch strategy {
		case PreferOursOnConflict:
			useOurs = true
		case PreferTheirsOnConflict:
			useOurs = false
		case LeaveConflictMarkers:
			// Keep the modified version (not the deleted one)
			useOurs = theirDeleted && !ourDeleted
		default:
			continue
		}

		if useOurs {
			if ourDeleted {
				_ = runGit(ctx, dir, "rm", "--force", "--", file)
			} else {
				_ = runGit(ctx, dir, "checkout", "--ours", "--", file)
			}
		} else {
			if theirDeleted {
				_ = runGit(ctx, dir, "rm", "--force", "--", file)
			} else {
				_ = runGit(ctx, dir, "checkout", "--theirs", "--", file)
			}
		}
	}

	return runGit(ctx, dir, "add", "-A")
}

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

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
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

// NewEmptyChangeset creates a changeset with no changes (before and after are the same empty directory).
func NewEmptyChangeset(ctx context.Context) (*Changeset, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var emptyDir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &emptyDir,
		dagql.Selector{Field: "directory"},
	); err != nil {
		return nil, fmt.Errorf("create empty directory: %w", err)
	}

	return NewChangeset(ctx, emptyDir, emptyDir)
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

// ResolveRefs must be called after JSON unmarshaling to fully reconstruct the Changeset.
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

// changesetPathSets enables O(1) path lookups during conflict detection.
type changesetPathSets struct {
	added    map[string]struct{}
	modified map[string]struct{}
	removed  map[string]struct{}
}

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
				if err := bindMountDir(beforeDir, beforeMount); err != nil {
					return fmt.Errorf("mount before to ./a/: %w", err)
				}
				defer unmountDir(beforeMount)
				if err := bindMountDir(afterDir, afterMount); err != nil {
					return fmt.Errorf("mount after to ./b/: %w", err)
				}
				defer unmountDir(afterMount)

				patchFile, err := os.Create(filepath.Join(root, ChangesetPatchFilename))
				if err != nil {
					return err
				}
				defer patchFile.Close()

				cmd := exec.CommandContext(ctx, "git", "diff", "--binary", "--no-prefix", "--no-index", "a", "b")
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
	ourPaths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute our paths: %w", err)
	}
	theirPaths, err := other.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute their paths: %w", err)
	}

	conflicts := ourPaths.CheckConflicts(theirPaths)

	if conflicts.IsEmpty() {
		return mergeChangesetsWithoutGit(ctx, ch, other)
	} else if onConflictStrategy == FailEarlyOnConflict {
		return nil, conflicts.Error()
	}

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
	if len(others) == 0 {
		return ch, nil
	}

	// Single element uses more efficient 2-way merge
	if len(others) == 1 {
		var twoWayStrategy WithChangesetMergeConflict
		switch onConflictStrategy {
		case FailEarlyOnConflicts:
			twoWayStrategy = FailEarlyOnConflict
		default:
			twoWayStrategy = FailOnConflict
		}
		return ch.WithChangeset(ctx, others[0], twoWayStrategy)
	}

	err := checkAllPairwiseConflicts(ctx, ch, others)

	if err == nil {
		return mergeChangesetsWithoutGit(ctx, ch, others...)
	} else if onConflictStrategy == FailEarlyOnConflicts {
		return nil, err
	}

	before, err := mergeBeforeDirectories(ctx, ch, others...)
	if err != nil {
		return nil, err
	}

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

	afterDir, err := gitOctopusMergeWithPatches(ctx, before.Self(), ourPatch, otherPatches)
	if err != nil {
		return nil, err
	}

	return newChangesetFromMerge(ctx, before, afterDir)
}

// mergeBeforeDirectories merges the "before" directories from all changesets,
// excluding .git since the merge process creates its own temporary .git directory.
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

	selectors = append(selectors, dagql.Selector{
		Field: "withoutDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".git")},
		},
	})

	var before dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &before, selectors...); err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("merge before directories: %w", err)
	}
	return before, nil
}

func withDirectorySelector(dirID *call.ID) dagql.Selector {
	return dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("")},
			{Name: "source", Value: dagql.NewID[*Directory](dirID)},
		},
	}
}

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

	for i, paths := range otherPaths {
		conflicts := ourPaths.CheckConflicts(paths)
		if !conflicts.IsEmpty() {
			return fmt.Errorf("conflict with changeset %d: %w", i, conflicts.Error())
		}
	}

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

// withGitMergeWorkspace sets up a workspace for git merge operations, runs the provided
// function, then commits and returns the resulting directory.
func withGitMergeWorkspace(ctx context.Context, base *Directory, description string, fn func(workDir string) error) (*Directory, error) {
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

	newRef, err := query.BuildkitCache().New(ctx, baseRef, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(description))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		workDir, err := containerdfs.RootPath(root, base.Dir)
		if err != nil {
			return err
		}
		return fn(workDir)
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

func gitMergeWithPatches(
	ctx context.Context,
	base *Directory,
	ourPatch, theirPatch *File,
	ourRemoved, theirRemoved []string,
	conflicts Conflicts,
	strategy WithChangesetMergeConflict,
) (*Directory, error) {
	return withGitMergeWorkspace(ctx, base, "Changeset.withChangeset git merge", func(workDir string) error {
		if err := initGitRepo(ctx, workDir); err != nil {
			return err
		}
		if err := createBranchWithPatchFile(ctx, workDir, "ours", ourPatch); err != nil {
			return err
		}
		if err := createBranchWithPatchFile(ctx, workDir, "theirs", theirPatch, "HEAD~1"); err != nil {
			return err
		}
		if err := runGit(ctx, workDir, "checkout", "ours"); err != nil {
			return err
		}

		mergeArgs := []string{"merge", "--no-edit", "--no-commit"}
		switch strategy {
		case PreferOursOnConflict:
			mergeArgs = append(mergeArgs, "-X", "ours")
		case PreferTheirsOnConflict:
			mergeArgs = append(mergeArgs, "-X", "theirs")
		}
		mergeArgs = append(mergeArgs, "theirs")

		mergeErr := runGit(ctx, workDir, mergeArgs...)

		switch strategy {
		case FailOnConflict:
			if mergeErr != nil {
				return mergeErr
			}
		case LeaveConflictMarkers, PreferOursOnConflict, PreferTheirsOnConflict:
			modifyDeleteConflicts := conflicts.ModifyDeletePaths()
			if len(modifyDeleteConflicts) > 0 {
				if err := resolveModifyDeleteConflicts(ctx, workDir, modifyDeleteConflicts, strategy, ourRemoved, theirRemoved); err != nil {
					return err
				}
			}
		default:
			if mergeErr != nil {
				return mergeErr
			}
		}

		return os.RemoveAll(filepath.Join(workDir, ".git"))
	})
}

func gitOctopusMergeWithPatches(
	ctx context.Context,
	base *Directory,
	ourPatch *File,
	otherPatches []*File,
) (*Directory, error) {
	return withGitMergeWorkspace(ctx, base, "Changeset.withChangesets git octopus merge", func(workDir string) error {
		if err := initGitRepo(ctx, workDir); err != nil {
			return err
		}
		if err := createBranchWithPatchFile(ctx, workDir, "ours", ourPatch); err != nil {
			return err
		}

		branchNames := make([]string, len(otherPatches))
		for i, patch := range otherPatches {
			branchName := fmt.Sprintf("branch_%d", i)
			branchNames[i] = branchName
			if err := createBranchWithPatchFile(ctx, workDir, branchName, patch, "HEAD~1"); err != nil {
				return err
			}
		}

		if err := runGit(ctx, workDir, "checkout", "ours"); err != nil {
			return err
		}

		mergeArgs := []string{"merge", "--no-edit", "--no-commit"}
		mergeArgs = append(mergeArgs, branchNames...)

		if err := runGit(ctx, workDir, mergeArgs...); err != nil {
			return err
		}

		return os.RemoveAll(filepath.Join(workDir, ".git"))
	})
}

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

// gitApplyPatchFromFile streams the patch to avoid loading it entirely into memory.
func gitApplyPatchFromFile(ctx context.Context, dir string, patch *File) error {
	if patch == nil {
		return nil
	}

	patchRef, err := getRefOrEvaluate(ctx, patch)
	if err != nil {
		return fmt.Errorf("evaluate patch ref: %w", err)
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return fmt.Errorf("no buildkit session group in context")
	}

	return MountRef(ctx, patchRef, bkSessionGroup, func(patchMount string, _ *mount.Mount) error {
		patchPath, err := containerdfs.RootPath(patchMount, patch.File)
		if err != nil {
			return err
		}

		// Check if patch file is empty
		info, err := os.Stat(patchPath)
		if err != nil {
			return fmt.Errorf("stat patch file: %w", err)
		}
		if info.Size() == 0 {
			return nil
		}

		tempPatch := filepath.Join(dir, ".dagger-patch")
		srcFile, err := os.Open(patchPath)
		if err != nil {
			return fmt.Errorf("open patch file: %w", err)
		}
		defer srcFile.Close()

		dstFile, err := os.Create(tempPatch)
		if err != nil {
			return fmt.Errorf("create temp patch file: %w", err)
		}

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			dstFile.Close()
			os.Remove(tempPatch)
			return fmt.Errorf("copy patch file: %w", err)
		}
		if err := dstFile.Close(); err != nil {
			os.Remove(tempPatch)
			return fmt.Errorf("close temp patch file: %w", err)
		}

		defer os.Remove(tempPatch)
		return runGit(ctx, dir, "apply", "--allow-empty", tempPatch)
	}, mountRefAsReadOnly)
}

func initGitRepo(ctx context.Context, dir string) error {
	if err := runGit(ctx, dir, "init"); err != nil {
		return err
	}
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	return runGit(ctx, dir, "commit", "--allow-empty", "-m", "base")
}

func createBranchWithPatchFile(ctx context.Context, dir string, branchName string, patch *File, startPoint ...string) error {
	checkoutArgs := []string{"checkout", "-b", branchName}
	if len(startPoint) > 0 {
		checkoutArgs = append(checkoutArgs, startPoint[0])
	}
	if err := runGit(ctx, dir, checkoutArgs...); err != nil {
		return err
	}
	if patch != nil {
		if err := gitApplyPatchFromFile(ctx, dir, patch); err != nil {
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

// resolveModifyDeleteConflicts handles conflicts where one side modified and the other deleted.
// For LEAVE_CONFLICT_MARKERS, keeps the modified version.
func resolveModifyDeleteConflicts(ctx context.Context, dir string, conflictFiles []string, strategy WithChangesetMergeConflict, ourRemoved, theirRemoved []string) error {
	if len(conflictFiles) == 0 {
		return nil
	}

	ourRemovedSet := toSet(ourRemoved)
	theirRemovedSet := toSet(theirRemoved)

	for _, file := range conflictFiles {
		_, ourDeleted := ourRemovedSet[file]
		_, theirDeleted := theirRemovedSet[file]

		var useOurs bool
		switch strategy {
		case PreferOursOnConflict:
			useOurs = true
		case PreferTheirsOnConflict:
			useOurs = false
		case LeaveConflictMarkers:
			useOurs = theirDeleted && !ourDeleted
		default:
			continue
		}

		deleted := (useOurs && ourDeleted) || (!useOurs && theirDeleted)
		if deleted {
			if err := runGit(ctx, dir, "rm", "--force", "--", file); err != nil {
				return fmt.Errorf("git rm %s: %w", file, err)
			}
		} else {
			side := "--ours"
			if !useOurs {
				side = "--theirs"
			}
			if err := runGit(ctx, dir, "checkout", side, "--", file); err != nil {
				return fmt.Errorf("git checkout %s %s: %w", side, file, err)
			}
		}
	}

	return runGit(ctx, dir, "add", "-A")
}

func toSet(slice []string) map[string]struct{} {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}
	return set
}

// mergeChangesetsWithoutGit merges changesets without using git by applying
// each changeset sequentially. This is only safe when there are no file overlaps
// between changesets.
func mergeChangesetsWithoutGit(ctx context.Context, ch *Changeset, others ...*Changeset) (*Changeset, error) {
	// Merge before directories (same as git path)
	before, err := mergeBeforeDirectories(ctx, ch, others...)
	if err != nil {
		return nil, err
	}

	// Start with merged before and apply each changeset sequentially
	afterDir := before.Self()

	afterDir, err = afterDir.WithChanges(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("apply changeset: %w", err)
	}

	for i, other := range others {
		afterDir, err = afterDir.WithChanges(ctx, other)
		if err != nil {
			return nil, fmt.Errorf("apply changeset %d: %w", i, err)
		}
	}

	return newChangesetFromMerge(ctx, before, afterDir)
}

package core

import (
	"context"
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

	pathsOnce   *sync.Once
	cachedPaths *ChangesetPaths
	pathsErr    error
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

				cmd := exec.Command("git", "diff", "--no-prefix", "--no-index", "a", "b")
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

func selectorWithoutFile(path string) dagql.Selector {
	return dagql.Selector{
		Field: "withoutFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
		},
	}
}

func selectorWithFile(path string, source dagql.ObjectResult[*File]) dagql.Selector {
	return dagql.Selector{
		Field: "withFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
			{Name: "source", Value: dagql.NewID[*File](source.ID())},
		},
	}
}

func fileAt(
	ctx context.Context,
	srv *dagql.Server,
	dir dagql.ObjectResult[*Directory],
	path string,
) (dagql.ObjectResult[*File], error) {
	var file dagql.ObjectResult[*File]
	err := srv.Select(ctx, &dir, &file,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
			},
		})
	return file, err
}

func withFileFromBefore(
	ctx context.Context,
	srv *dagql.Server,
	changeset *Changeset,
	path string,
) (sel dagql.Selector, err error) {
	file, err := fileAt(ctx, srv, changeset.Before, path)
	if err != nil {
		return sel, err
	}
	return selectorWithFile(path, file), nil
}

func withFileFromAfter(
	ctx context.Context,
	srv *dagql.Server,
	changeset *Changeset,
	path string,
) (sel dagql.Selector, err error) {
	file, err := fileAt(ctx, srv, changeset.After, path)
	if err != nil {
		return sel, err
	}
	return selectorWithFile(path, file), nil
}

type WithChangesetMergeConflict int

const (
	FailOnConflict WithChangesetMergeConflict = iota
	SkipOnConflict
	PreferOursOnConflict
	PreferTheirsOnConflict
)

// AfterSelectorsForConflictResolution returns two arrays of selectors
// to apply to the "after" directories of two changesets to resolve their
// conflicts using the defined strategy.
//
//nolint:gocyclo
func AfterSelectorsForConflictResolution(
	ctx context.Context,
	srv *dagql.Server,
	ch *Changeset,
	other *Changeset,
	conflicts Conflicts,
	onConflictStrategy WithChangesetMergeConflict,
) (parentAfterSelector, additionalAfterSelector []dagql.Selector, err error) {
	var sel dagql.Selector
	// When SkipOnConflict all conflicts will be skipped
	// this means the initial state of the file will be kept in the "after" directory
	// so that the diff will not see any difference
	// - if file has been added, remove it from the "after" directory
	// - if file has been modified or removed, copy the file from "before" to "after" to keep its initial state
	// In case of PreferOurs or PreferTheirs this is applied to only one part (parent or additional changes)
	// that way one of the two will not see a change while the other will see it.
	// hence the change will be applied only once.
	for _, c := range conflicts {
		if c.Self == ChangeTypeAdded &&
			c.Other == ChangeTypeAdded {
			switch onConflictStrategy {
			// on skip, just remove the file from the "after" directories
			case SkipOnConflict:
				parentAfterSelector = append(parentAfterSelector, selectorWithoutFile(c.Path))
				additionalAfterSelector = append(additionalAfterSelector, selectorWithoutFile(c.Path))
			// if we prefer ours, keep it and put the file on the "after" of additional changes
			case PreferOursOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer theirs, keep it and put the file on the "after" of parent changes
			case PreferTheirsOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
			}
		} else if c.Self == ChangeTypeModified &&
			c.Other == ChangeTypeModified {
			switch onConflictStrategy {
			// on skip, use the "before" file everywhere
			case SkipOnConflict:
				if sel, err = withFileFromBefore(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
				if sel, err = withFileFromBefore(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer ours, keep it and put the file on the "after" of additional changes
			case PreferOursOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer theirs, keep it and put the file on the "after" of parent changes
			case PreferTheirsOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
			}
		} else if c.Self == ChangeTypeModified &&
			c.Other == ChangeTypeRemoved {
			switch onConflictStrategy {
			// on skip, use the "before" file everywhere
			case SkipOnConflict:
				if sel, err = withFileFromBefore(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
				if sel, err = withFileFromBefore(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer ours, use the "after" from parent changes on additional changes
			case PreferOursOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer theirs, remove the file from "after" of parent changes
			case PreferTheirsOnConflict:
				parentAfterSelector = append(parentAfterSelector, selectorWithoutFile(c.Path))
			}
		} else if c.Self == ChangeTypeRemoved &&
			c.Other == ChangeTypeModified {
			switch onConflictStrategy {
			// on skip, use the "before" file everywhere
			case SkipOnConflict:
				if sel, err = withFileFromBefore(ctx, srv, ch, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
				if sel, err = withFileFromBefore(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					additionalAfterSelector = append(additionalAfterSelector, sel)
				}
			// if we prefer ours, remove the file from "after" of additional changes
			case PreferOursOnConflict:
				additionalAfterSelector = append(additionalAfterSelector, selectorWithoutFile(c.Path))
			// if we prefer theirs, use the file from "after" of additional changes
			case PreferTheirsOnConflict:
				if sel, err = withFileFromAfter(ctx, srv, other, c.Path); err != nil {
					return parentAfterSelector, additionalAfterSelector, err
				} else {
					parentAfterSelector = append(parentAfterSelector, sel)
				}
			}
		}
	}
	return parentAfterSelector, additionalAfterSelector, err
}

// WithChangeset merges another changeset into this one, returning a new combined changeset.
// The onConflictStrategy determines how conflicts are handled.
func (ch *Changeset) WithChangeset(
	ctx context.Context,
	other *Changeset,
	onConflictStrategy WithChangesetMergeConflict,
) (*Changeset, error) {
	return ch.WithChangesets(ctx, []*Changeset{other}, onConflictStrategy)
}

// WithChangesets merges multiple changesets into this one efficiently in a single pass.
// This is more efficient than calling WithChangeset repeatedly because it:
// - Only creates ONE final changeset instead of N-1 intermediate changesets
// - Only walks directories 3 times total instead of 3*(N-1) times
// - Pre-computes path sets once for all conflict detection
func (ch *Changeset) WithChangesets(
	ctx context.Context,
	others []*Changeset,
	onConflictStrategy WithChangesetMergeConflict,
) (*Changeset, error) {
	if len(others) == 0 {
		return ch, nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	// Collect all changesets
	all := make([]*Changeset, 0, len(others)+1)
	all = append(all, ch)
	all = append(all, others...)

	// Build path sets for all changesets once (for O(1) conflict detection)
	csPaths := make([]*ChangesetPaths, len(all))
	pathSets := make([]changesetPathSets, len(all))
	for i, cs := range all {
		paths, err := cs.ComputePaths(ctx)
		if err != nil {
			return nil, err
		}
		csPaths[i] = paths
		pathSets[i] = paths.pathSets()
	}

	// Check for conflicts across ALL changeset pairs
	conflicts := checkConflictsMulti(csPaths, pathSets)

	if !conflicts.IsEmpty() {
		if onConflictStrategy == FailOnConflict {
			return nil, conflicts.Error()
		}
	}

	// Prepare changeset IDs - these will be modified if there are conflicts
	changesetIDs := make([]dagql.ObjectResult[*Changeset], len(all))

	if !conflicts.IsEmpty() {
		// Resolve conflicts by modifying "after" directories
		resolvedAfters := make([]dagql.ObjectResult[*Directory], len(all))
		for i := range all {
			resolvedAfters[i] = all[i].After
		}

		// Apply conflict resolution: for each conflict, determine which changeset(s) to modify
		if err := resolveConflictsMulti(ctx, srv, all, csPaths, conflicts, onConflictStrategy, resolvedAfters); err != nil {
			return nil, err
		}

		// Create changeset IDs from resolved afters
		for i, cs := range all {
			if err := srv.Select(ctx, resolvedAfters[i], &changesetIDs[i],
				dagql.Selector{
					Field: "changes",
					Args: []dagql.NamedInput{
						{Name: "from", Value: dagql.NewID[*Directory](cs.Before.ID())},
					},
				},
			); err != nil {
				return nil, err
			}
		}
	} else {
		// No conflicts - create changeset IDs from existing Before/After
		for i, cs := range all {
			if err := srv.Select(ctx, cs.After, &changesetIDs[i],
				dagql.Selector{
					Field: "changes",
					Args: []dagql.NamedInput{
						{Name: "from", Value: dagql.NewID[*Directory](cs.Before.ID())},
					},
				},
			); err != nil {
				return nil, err
			}
		}
	}

	// Merge all "before" directories into one
	// Start with an empty directory and merge all befores
	selectors := []dagql.Selector{{Field: "directory"}}
	for _, cs := range all {
		selectors = append(selectors, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("")},
				{Name: "source", Value: dagql.NewID[*Directory](cs.Before.ID())},
			},
		})
	}

	var before dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &before, selectors...); err != nil {
		return nil, err
	}

	// Apply all changesets to the merged before directory
	var after dagql.ObjectResult[*Directory]
	applySelectors := make([]dagql.Selector, len(changesetIDs))
	for i, csID := range changesetIDs {
		applySelectors[i] = dagql.Selector{
			Field: "withChanges",
			Args: []dagql.NamedInput{
				{Name: "changes", Value: dagql.NewID[*Changeset](csID.ID())},
			},
		}
	}
	if err := srv.Select(ctx, before, &after, applySelectors...); err != nil {
		return nil, err
	}

	// Create and return the new merged changeset
	return NewChangeset(ctx, before, after)
}

// checkConflictsMulti detects conflicts across multiple changesets using pre-computed path sets
func checkConflictsMulti(changesets []*ChangesetPaths, pathSets []changesetPathSets) Conflicts {
	var conflicts Conflicts

	// Check each pair of changesets for conflicts
	for i := 0; i < len(changesets); i++ {
		for j := i + 1; j < len(changesets); j++ {
			pairConflicts := changesets[i].checkConflictsWithSets(pathSets[j])
			conflicts = append(conflicts, pairConflicts...)
		}
	}
	return conflicts
}

// resolveConflictsMulti applies conflict resolution to the "after" directories
func resolveConflictsMulti(
	ctx context.Context,
	srv *dagql.Server,
	changesets []*Changeset,
	changesetsPaths []*ChangesetPaths,
	conflicts Conflicts,
	onConflictStrategy WithChangesetMergeConflict,
	resolvedAfters []dagql.ObjectResult[*Directory],
) error {
	// Group conflicts by the changesets they affect
	// For simplicity, we apply resolution similarly to pairwise merging:
	// - SKIP: revert to "before" state in all affected changesets
	// - PREFER_OURS: keep first changeset's version, remove from others
	// - PREFER_THEIRS: keep last changeset's version, remove from earlier ones

	// Build a map to track which paths need resolution in each changeset
	resolutionsByChangeset := make([][]dagql.Selector, len(changesets))

	for _, c := range conflicts {
		// Find which changesets are involved in this conflict
		var involvedIdxs []int
		for i, cs := range changesetsPaths {
			sets := cs.pathSets()
			if _, ok := sets.added[c.Path]; ok {
				involvedIdxs = append(involvedIdxs, i)
			} else if _, ok := sets.modified[c.Path]; ok {
				involvedIdxs = append(involvedIdxs, i)
			} else if _, ok := sets.removed[c.Path]; ok {
				involvedIdxs = append(involvedIdxs, i)
			}
		}

		if len(involvedIdxs) < 2 {
			continue // Not actually a conflict
		}

		switch onConflictStrategy {
		case SkipOnConflict:
			// Revert all involved changesets to "before" state for this path
			for _, idx := range involvedIdxs {
				cs := changesets[idx]
				if c.Self == ChangeTypeAdded || c.Other == ChangeTypeAdded {
					// Remove added file
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithoutFile(c.Path))
				} else {
					// Restore from before
					file, err := fileAt(ctx, srv, cs.Before, c.Path)
					if err != nil {
						return err
					}
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithFile(c.Path, file))
				}
			}

		case PreferOursOnConflict:
			// Keep first changeset's version, modify others to match
			firstIdx := involvedIdxs[0]
			firstCs := changesets[firstIdx]
			for _, idx := range involvedIdxs[1:] {
				// Copy the file from the first changeset's after to others
				file, err := fileAt(ctx, srv, firstCs.After, c.Path)
				if err != nil {
					// If file doesn't exist in first (removed), remove from others too
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithoutFile(c.Path))
				} else {
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithFile(c.Path, file))
				}
			}

		case PreferTheirsOnConflict:
			// Keep last changeset's version, modify earlier ones to match
			lastIdx := involvedIdxs[len(involvedIdxs)-1]
			lastCs := changesets[lastIdx]
			for _, idx := range involvedIdxs[:len(involvedIdxs)-1] {
				// Copy the file from the last changeset's after to earlier ones
				file, err := fileAt(ctx, srv, lastCs.After, c.Path)
				if err != nil {
					// If file doesn't exist in last (removed), remove from earlier too
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithoutFile(c.Path))
				} else {
					resolutionsByChangeset[idx] = append(resolutionsByChangeset[idx], selectorWithFile(c.Path, file))
				}
			}
		}
	}

	// Apply all resolutions
	for i, selectors := range resolutionsByChangeset {
		if len(selectors) == 0 {
			continue
		}
		if err := srv.Select(ctx, changesets[i].After, &resolvedAfters[i], selectors...); err != nil {
			return err
		}
	}

	return nil
}

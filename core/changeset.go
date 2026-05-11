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

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	telemetry "github.com/dagger/otel-go"
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
	Renamed    map[string]string // newPath → oldPath (also included in Added/Removed)
}

type DiffStatKind string

var DiffStatKindEnum = dagql.NewEnum[DiffStatKind]()

var (
	DiffStatKindAdded = DiffStatKindEnum.Register("ADDED",
		`A file or directory was added.`)
	DiffStatKindModified = DiffStatKindEnum.Register("MODIFIED",
		`A file was modified.`)
	DiffStatKindRemoved = DiffStatKindEnum.Register("REMOVED",
		`A file or directory was removed.`)
	DiffStatKindRenamed = DiffStatKindEnum.Register("RENAMED",
		`A file was renamed.`)
)

func (DiffStatKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "DiffStatKind",
		NonNull:   true,
	}
}

func (DiffStatKind) TypeDescription() string {
	return "The type of change for a diff stat entry."
}

func (DiffStatKind) Decoder() dagql.InputDecoder {
	return DiffStatKindEnum
}

func (k DiffStatKind) ToLiteral() call.Literal {
	return DiffStatKindEnum.Literal(k)
}

type DiffStat struct {
	Path         string       `field:"true" doc:"Path of the changed file or directory."`
	OldPath      *string      `field:"true" doc:"Previous path of the file, set only for renames."`
	Kind         DiffStatKind `field:"true" doc:"Type of change."`
	AddedLines   int          `field:"true" doc:"Number of added lines for this path."`
	RemovedLines int          `field:"true" doc:"Number of removed lines for this path."`
}

func (*DiffStat) Type() *ast.Type {
	return &ast.Type{
		NamedType: "DiffStat",
		NonNull:   true,
	}
}

// ComputePaths computes the added, modified, and removed paths using git diff.
func (ch *Changeset) ComputePaths(ctx context.Context) (*ChangesetPaths, error) {
	ch.pathsOnce.Do(func() {
		ch.cachedPaths, ch.pathsErr = ch.computePathsOnce(ctx)
	})
	return ch.cachedPaths, ch.pathsErr
}

func (ch *Changeset) computePathsOnce(ctx context.Context) (*ChangesetPaths, error) {
	beforeDigest, err := ch.Before.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("before content-preferred digest: %w", err)
	}
	afterDigest, err := ch.After.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("after content-preferred digest: %w", err)
	}
	if beforeDigest == afterDigest {
		return &ChangesetPaths{}, nil
	}

	var result *ChangesetPaths
	err = ch.withMountedDirs(ctx, func(beforeDir, afterDir string) (err error) {
		result, err = computeChangesetPaths(ctx, beforeDir, afterDir)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func computeChangesetPaths(ctx context.Context, beforeDir, afterDir string) (*ChangesetPaths, error) {
	fc, err := compareDirectories(ctx, beforeDir, afterDir)
	if err != nil {
		return nil, err
	}

	beforeDirs, err := listSubdirectories(beforeDir)
	if err != nil {
		return nil, fmt.Errorf("list before directories: %w", err)
	}
	afterDirs, err := listSubdirectories(afterDir)
	if err != nil {
		return nil, fmt.Errorf("list after directories: %w", err)
	}
	addedDirs, removedDirs := diffStringSlices(beforeDirs, afterDirs)

	// Expand renames into Added/Removed so addedPaths/removedPaths stay complete.
	renamedNew := make([]string, 0, len(fc.Renamed))
	renamedOld := make([]string, 0, len(fc.Renamed))
	for newPath, oldPath := range fc.Renamed {
		renamedNew = append(renamedNew, newPath)
		renamedOld = append(renamedOld, oldPath)
	}

	allRemoved := slices.Concat(fc.Removed, renamedOld, removedDirs)

	return &ChangesetPaths{
		Added:      slices.Concat(fc.Added, renamedNew, addedDirs),
		Modified:   fc.Modified,
		Removed:    collapseChildPaths(allRemoved),
		AllRemoved: allRemoved,
		Renamed:    fc.Renamed,
	}, nil
}

// withMountedDirs mounts the before and after directories and calls fn with their paths.
func (ch *Changeset) withMountedDirs(ctx context.Context, fn func(beforeDir, afterDir string) error) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, ch.Before, ch.After); err != nil {
		return fmt.Errorf("evaluate changeset directories: %w", err)
	}

	beforeRef, err := ch.Before.Self().Snapshot.GetOrEval(ctx, ch.Before.Result)
	if err != nil {
		return fmt.Errorf("evaluate before: %w", err)
	}

	afterRef, err := ch.After.Self().Snapshot.GetOrEval(ctx, ch.After.Result)
	if err != nil {
		return fmt.Errorf("evaluate after: %w", err)
	}

	beforeSelector, err := ch.Before.Self().Dir.GetOrEval(ctx, ch.Before.Result)
	if err != nil {
		return fmt.Errorf("evaluate before selector: %w", err)
	}
	afterSelector, err := ch.After.Self().Dir.GetOrEval(ctx, ch.After.Result)
	if err != nil {
		return fmt.Errorf("evaluate after selector: %w", err)
	}

	return MountRef(ctx, beforeRef, func(beforeMount string, _ *mount.Mount) error {
		beforeDir, err := containerdfs.RootPath(beforeMount, beforeSelector)
		if err != nil {
			return err
		}

		return MountRef(ctx, afterRef, func(afterMount string, _ *mount.Mount) error {
			afterDir, err := containerdfs.RootPath(afterMount, afterSelector)
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
	beforeID, err := ch.Before.ID()
	if err != nil {
		return nil, fmt.Errorf("before ID: %w", err)
	}
	afterID, err := ch.After.ID()
	if err != nil {
		return nil, fmt.Errorf("after ID: %w", err)
	}
	return json.Marshal(changesetJSONEnvelope{
		BeforeID: dagql.NewID[*Directory](beforeID),
		AfterID:  dagql.NewID[*Directory](afterID),
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

var _ Syncable = (*Changeset)(nil)
var _ dagql.HasDependencyResults = (*Changeset)(nil)

func (ch *Changeset) Evaluate(context.Context) error {
	return nil
}

func (ch *Changeset) Sync(ctx context.Context) error {
	return ch.Evaluate(ctx)
}

func (ch *Changeset) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if ch == nil {
		return nil, nil
	}

	var deps []dagql.AnyResult

	if ch.Before.Self() != nil {
		attached, err := attach(ch.Before)
		if err != nil {
			return nil, fmt.Errorf("attach changeset before: %w", err)
		}
		before, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach changeset before: unexpected result %T", attached)
		}
		ch.Before = before
		deps = append(deps, before)
	}

	if ch.After.Self() != nil {
		attached, err := attach(ch.After)
		if err != nil {
			return nil, fmt.Errorf("attach changeset after: %w", err)
		}
		after, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach changeset after: unexpected result %T", attached)
		}
		ch.After = after
		deps = append(deps, after)
	}

	return deps, nil
}

const ChangesetPatchFilename = "diff.patch"

func (ch *Changeset) IsEmpty(ctx context.Context) (bool, error) {
	beforeDigest, err := ch.Before.ContentPreferredDigest(ctx)
	if err != nil {
		return false, fmt.Errorf("before content-preferred digest: %w", err)
	}
	afterDigest, err := ch.After.ContentPreferredDigest(ctx)
	if err != nil {
		return false, fmt.Errorf("after content-preferred digest: %w", err)
	}
	if beforeDigest == afterDigest {
		return true, nil
	}

	var isEmpty bool
	err = ch.withMountedDirs(ctx, func(beforeDir, afterDir string) error {
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

func (ch *Changeset) DiffStats(ctx context.Context) ([]*DiffStat, error) {
	var paths *ChangesetPaths
	var statsByPath map[string]lineChanges
	err := ch.withMountedDirs(ctx, func(beforeDir, afterDir string) error {
		computedPaths, err := computeChangesetPaths(ctx, beforeDir, afterDir)
		if err != nil {
			return fmt.Errorf("compute paths: %w", err)
		}
		paths = computedPaths

		statsByPath, err = compareDirectoriesNumStat(ctx, beforeDir, afterDir)
		if err != nil {
			slog.Debug("changeset numstat failed; returning path-only diff stat entries", "error", err)
			statsByPath = nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	addEntry := func(path string, kind DiffStatKind) *DiffStat {
		entry := &DiffStat{Path: path, Kind: kind}
		if stat, ok := statsByPath[path]; ok {
			entry.AddedLines = stat.Added
			entry.RemovedLines = stat.Removed
		}
		return entry
	}

	// Build a set of old renamed paths so we can skip them in Removed
	// (they'll appear as KindRenamed via their new path in Added).
	renamedOld := make(map[string]bool, len(paths.Renamed))
	for _, oldPath := range paths.Renamed {
		renamedOld[oldPath] = true
	}

	var entries []*DiffStat
	for _, path := range paths.Added {
		if oldPath, isRenamed := paths.Renamed[path]; isRenamed {
			entry := addEntry(path, DiffStatKindRenamed)
			entry.OldPath = &oldPath
			entries = append(entries, entry)
		} else {
			entries = append(entries, addEntry(path, DiffStatKindAdded))
		}
	}
	for _, path := range paths.Modified {
		entries = append(entries, addEntry(path, DiffStatKindModified))
	}
	// Use AllRemoved (uncollapsed) so that patchpreview.foldRemovedDirs can
	// fold child files into their parent directory with summed line counts.
	for _, path := range paths.AllRemoved {
		if renamedOld[path] {
			continue
		}
		entries = append(entries, addEntry(path, DiffStatKindRemoved))
	}

	slices.SortFunc(entries, func(a, b *DiffStat) int {
		return strings.Compare(a.Path, b.Path)
	})
	return entries, nil
}

func (ch *Changeset) AsPatch(ctx context.Context) (*File, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, ch.Before, ch.After); err != nil {
		return nil, fmt.Errorf("evaluate changeset directories: %w", err)
	}

	beforeRef, err := ch.Before.Self().Snapshot.GetOrEval(ctx, ch.Before.Result)
	if err != nil {
		return nil, err
	}

	afterRef, err := ch.After.Self().Snapshot.GetOrEval(ctx, ch.After.Result)
	if err != nil {
		return nil, err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()

	newRef, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.asPatch"))
	if err != nil {
		return nil, err
	}
	beforeSelector, err := ch.Before.Self().Dir.GetOrEval(ctx, ch.Before.Result)
	if err != nil {
		return nil, err
	}
	afterSelector, err := ch.After.Self().Dir.GetOrEval(ctx, ch.After.Result)
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, beforeRef, func(before string, _ *mount.Mount) error {
		beforeDir, err := containerdfs.RootPath(before, beforeSelector)
		if err != nil {
			return err
		}
		return MountRef(ctx, afterRef, func(after string, _ *mount.Mount) error {
			afterDir, err := containerdfs.RootPath(after, afterSelector)
			if err != nil {
				return err
			}
			return MountRef(ctx, newRef, func(root string, _ *mount.Mount) (rerr error) {
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

				cmd := exec.CommandContext(ctx, "git", "diff", "--binary", "--no-prefix", "--no-index", "a", "b")
				cmd.Dir = root
				cmd.Stdout = io.MultiWriter(patchFile, stdio.Stdout)
				cmd.Stderr = stdio.Stderr
				if err := cmd.Run(); err != nil {
					var exitErr *exec.ExitError
					// Check if it's exit code 1, which is expected for git diff when files differ
					if errors.As(err, &exitErr) && exitErr.ExitCode() != 1 {
						// NB: we could technically populate an ExecError here, but that
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
	file := &File{
		Platform: query.Platform(),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	file.File.setValue(ChangesetPatchFilename)
	file.Snapshot.setValue(snap)
	return file, nil
}

func (ch *Changeset) Export(ctx context.Context, destPath string) (rerr error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute paths: %w", err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}
	var dir dagql.ObjectResult[*Directory]
	afterID, err := ch.After.ID()
	if err != nil {
		return fmt.Errorf("after ID: %w", err)
	}
	if err := srv.Select(ctx, ch.Before, &dir,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*Directory](afterID)},
			},
		},
	); err != nil {
		return fmt.Errorf("get changeset diff directory: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, dir); err != nil {
		return fmt.Errorf("evaluate changeset diff directory: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export changeset to host %s", destPath))
	defer telemetry.EndWithCause(span, &rerr)

	dirSnapshot, err := dir.Self().Snapshot.GetOrEval(ctx, dir.Result)
	if err != nil {
		return fmt.Errorf("failed to evaluate changeset diff snapshot: %w", err)
	}
	dirSelector, err := dir.Self().Dir.GetOrEval(ctx, dir.Result)
	if err != nil {
		return fmt.Errorf("failed to evaluate changeset diff selector: %w", err)
	}

	return MountRef(ctx, dirSnapshot, func(root string, _ *mount.Mount) error {
		root, err = containerdfs.RootPath(root, dirSelector)
		if err != nil {
			return err
		}
		return bk.LocalDirExport(ctx, root, destPath, true, paths.Removed)
	}, mountRefAsReadOnly)
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

	if !conflicts.IsEmpty() && onConflictStrategy == FailEarlyOnConflict {
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
		before,
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
	if err != nil && onConflictStrategy == FailEarlyOnConflicts {
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

	afterDir, err := gitOctopusMergeWithPatches(ctx, before, ourPatch, otherPatches)
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
	}
	beforeID, err := ch.Before.ID()
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("before ID: %w", err)
	}
	selectors = append(selectors, withDirectorySelector(beforeID))
	for _, other := range others {
		otherBeforeID, err := other.Before.ID()
		if err != nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("other before ID: %w", err)
		}
		selectors = append(selectors, withDirectorySelector(otherBeforeID))
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

	afterRef, _ := afterDir.Snapshot.Peek()
	if afterRef == nil {
		return nil, fmt.Errorf("evaluate merged directory snapshot: nil")
	}
	afterSelector, _ := afterDir.Dir.Peek()

	after, err := dagql.NewObjectResultForCall(afterDir, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		Type:        dagql.NewResultCallType(afterDir.Type()),
		SyntheticOp: "changeset_merge_output",
		ImplicitInputs: []*dagql.ResultCallArg{
			{
				Name: "snapshotID",
				Value: &dagql.ResultCallLiteral{
					Kind:        dagql.ResultCallLiteralKindString,
					StringValue: afterRef.SnapshotID(),
				},
			},
			{
				Name: "dir",
				Value: &dagql.ResultCallLiteral{
					Kind:        dagql.ResultCallLiteralKindString,
					StringValue: afterSelector,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create synthetic merged directory result: %w", err)
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
func withGitMergeWorkspace(ctx context.Context, base dagql.ObjectResult[*Directory], description string, fn func(workDir string) error) (*Directory, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, base); err != nil {
		return nil, fmt.Errorf("evaluate base: %w", err)
	}

	baseRef, err := base.Self().Snapshot.GetOrEval(ctx, base.Result)
	if err != nil {
		return nil, fmt.Errorf("evaluate base: %w", err)
	}
	baseSelector, err := base.Self().Dir.GetOrEval(ctx, base.Result)
	if err != nil {
		return nil, fmt.Errorf("evaluate base selector: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	newRef, err := query.SnapshotManager().New(ctx, baseRef,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(description))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		workDir, err := containerdfs.RootPath(root, baseSelector)
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
	dir := &Directory{
		Platform: query.Platform(),
		Services: slices.Clone(base.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue(baseSelector)
	dir.Snapshot.setValue(snap)
	return dir, nil
}

func gitMergeWithPatches(
	ctx context.Context,
	base dagql.ObjectResult[*Directory],
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

		if err := os.RemoveAll(filepath.Join(workDir, ".git")); err != nil {
			return fmt.Errorf("remove temporary merge git repository: %w", err)
		}
		return nil
	})
}

func gitOctopusMergeWithPatches(
	ctx context.Context,
	base dagql.ObjectResult[*Directory],
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

		if err := os.RemoveAll(filepath.Join(workDir, ".git")); err != nil {
			return fmt.Errorf("remove temporary octopus merge git repository: %w", err)
		}
		return nil
	})
}

var gitEphemeralConfig = []string{
	// These repositories are disposable. Detached maintenance can outlive the
	// git command and race with the immediate .git cleanup below.
	"-c", "maintenance.auto=false",
	"-c", "maintenance.autoDetach=false",
	"-c", "gc.auto=0",
	"-c", "gc.autoDetach=false",
}

func runGit(ctx context.Context, dir string, args ...string) error {
	gitArgs := make([]string, 0, len(gitEphemeralConfig)+len(args))
	gitArgs = append(gitArgs, gitEphemeralConfig...)
	gitArgs = append(gitArgs, args...)

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
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

	patchRef, _ := patch.Snapshot.Peek()
	if patchRef == nil {
		return fmt.Errorf("evaluate patch ref: nil")
	}
	patchPathSelector, _ := patch.File.Peek()

	return MountRef(ctx, patchRef, func(patchMount string, _ *mount.Mount) error {
		patchPath, err := containerdfs.RootPath(patchMount, patchPathSelector)
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

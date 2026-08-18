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
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/layercopy"
	"github.com/dagger/dagger/util/parallel"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/log"
	"golang.org/x/sys/unix"
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

var (
	_ dagql.PersistedObject        = (*DiffStat)(nil)
	_ dagql.PersistedObjectDecoder = (*DiffStat)(nil)
)

func (*DiffStat) Type() *ast.Type {
	return &ast.Type{
		NamedType: "DiffStat",
		NonNull:   true,
	}
}

type persistedDiffStat struct {
	Path         string       `json:"path"`
	OldPath      *string      `json:"oldPath,omitempty"`
	Kind         DiffStatKind `json:"kind"`
	AddedLines   int          `json:"addedLines"`
	RemovedLines int          `json:"removedLines"`
}

func (s *DiffStat) EncodePersistedObject(context.Context, dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	if s == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted diff stat: nil diff stat")
	}
	return encodePersistedObjectPayload(persistedDiffStat{
		Path:         s.Path,
		OldPath:      s.OldPath,
		Kind:         s.Kind,
		AddedLines:   s.AddedLines,
		RemovedLines: s.RemovedLines,
	})
}

func (*DiffStat) DecodePersistedObject(_ context.Context, _ *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedDiffStat
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted diff stat payload: %w", err)
	}
	return &DiffStat{
		Path:         persisted.Path,
		OldPath:      persisted.OldPath,
		Kind:         persisted.Kind,
		AddedLines:   persisted.AddedLines,
		RemovedLines: persisted.RemovedLines,
	}, nil
}

// ComputePaths computes the added, modified, and removed paths using file
// metadata and git diffs.
func (ch *Changeset) ComputePaths(ctx context.Context) (*ChangesetPaths, error) {
	ch.pathsOnce.Do(func() {
		_ = enginetel.Task(ctx, "computing paths", func(ctx context.Context) error {
			ch.cachedPaths, ch.pathsErr = ch.computePathsOnce(ctx)
			if ch.pathsErr != nil {
				// nothing to report; cachedPaths is nil on error
				return ch.pathsErr
			}
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			defer stdio.Close()
			fmt.Fprintln(stdio.Stdout, "added:", ch.cachedPaths.Added)
			fmt.Fprintln(stdio.Stdout, "removed:", ch.cachedPaths.Removed)
			fmt.Fprintln(stdio.Stdout, "modified:", ch.cachedPaths.Modified)
			fmt.Fprintln(stdio.Stdout, "renamed:", ch.cachedPaths.Renamed)
			return nil
		})
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
		result, _, err = computeChangesetPathsDelta(ctx, beforeDir, afterDir, false)
		if err != nil {
			slog.Warn("changeset delta diff failed; falling back to full content diff", "error", err)
			result, err = computeChangesetPaths(ctx, beforeDir, afterDir)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// maxGitPathSpecBytes bounds the pathspecs appended to a git diff. They only
// narrow work that would otherwise be correct anyway, and argv has a hard size
// limit, so the list gets collapsed or dropped rather than allowed to grow
// until exec fails.
const maxGitPathSpecBytes = 128 << 10

// gitDiffPathSpecs returns pathspecs limiting a diff to the paths that
// actually changed. Renamed paths need no entry of their own: ChangesetPaths
// records them under both their old and new names, in Removed and Added.
//
// An empty result means "diff everything". Too many paths to pass are first
// collapsed to their parent directories, which still match everything beneath
// them, and given up on entirely once even those don't fit.
func gitDiffPathSpecs(paths *ChangesetPaths) []string {
	specs := slices.Concat(paths.Added, paths.Removed, paths.Modified)
	for pathSpecsSize(specs) > maxGitPathSpecBytes {
		specs = collapsePathSpecsToParents(specs)
		if len(specs) == 0 {
			return nil
		}
	}
	return specs
}

func pathSpecsSize(specs []string) int {
	var size int
	for _, spec := range specs {
		size += len(spec) + 1 // NUL terminator in argv
	}
	return size
}

// collapsePathSpecsToParents replaces each pathspec with its parent directory,
// which matches everything the original did and then some. Returns nil once
// any entry sits at the tree root, since its parent is the whole tree.
func collapsePathSpecsToParents(specs []string) []string {
	seen := make(map[string]struct{}, len(specs))
	collapsed := make([]string, 0, len(specs))
	for _, spec := range specs {
		parent := path.Dir(path.Clean(spec))
		if parent == "." || parent == "/" {
			return nil
		}
		if _, ok := seen[parent]; ok {
			continue
		}
		seen[parent] = struct{}{}
		collapsed = append(collapsed, parent)
	}
	return collapsed
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

	// Sort to match computeChangesetPathsDelta: both paths must emit one
	// canonical order, or which order a caller observes silently depends on
	// whether the metadata delta walk succeeded on the underlying mounts.
	allRemoved := slices.Concat(fc.Removed, renamedOld, removedDirs)
	slices.Sort(allRemoved)
	added := slices.Concat(fc.Added, renamedNew, addedDirs)
	slices.Sort(added)
	slices.Sort(fc.Modified)

	return &ChangesetPaths{
		Added:      added,
		Modified:   fc.Modified,
		Removed:    CollapseChildPaths(allRemoved),
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

type persistedChangesetPayload struct {
	BeforeResultID uint64 `json:"beforeResultID,omitempty"`
	AfterResultID  uint64 `json:"afterResultID,omitempty"`
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

func (ch *Changeset) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if ch == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted changeset: nil changeset")
	}
	beforeID, err := encodePersistedObjectRef(cache, ch.Before, "changeset before")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	afterID, err := encodePersistedObjectRef(cache, ch.After, "changeset after")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload, err := json.Marshal(persistedChangesetPayload{
		BeforeResultID: beforeID,
		AfterResultID:  afterID,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted changeset payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payload), nil
}

func (*Changeset) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedChangesetPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted changeset payload: %w", err)
	}

	before, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.BeforeResultID, "changeset before")
	if err != nil {
		return nil, err
	}
	after, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.AfterResultID, "changeset after")
	if err != nil {
		return nil, err
	}
	return NewChangeset(ctx, before, after)
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

var (
	_ Syncable                     = (*Changeset)(nil)
	_ dagql.PersistedObject        = (*Changeset)(nil)
	_ dagql.PersistedObjectDecoder = (*Changeset)(nil)
	_ dagql.HasDependencyResults   = (*Changeset)(nil)
)

// Evaluate forces the changeset's before/after directories to materialize. This
// is where a generator's underlying exec (e.g. SDK codegen) actually runs, so
// syncing a changeset within a generator's span attributes any failure to that
// span -- the frontend then renders a red generator with its exec logs, instead
// of the failure surfacing later, unattributed, during the merge.
func (ch *Changeset) Evaluate(ctx context.Context) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	return cache.Evaluate(ctx, ch.Before, ch.After)
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
		empty, err := changesetDeltaIsEmpty(ctx, beforeDir, afterDir)
		if err == nil {
			isEmpty = empty
			return nil
		}
		slog.Warn("changeset delta diff failed; falling back to full content diff", "error", err)
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
		computedPaths, deltaStats, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, true)
		if err != nil {
			slog.Warn("changeset delta diff failed; falling back to full content diff", "error", err)
			computedPaths, err = computeChangesetPaths(ctx, beforeDir, afterDir)
			if err != nil {
				return fmt.Errorf("compute paths: %w", err)
			}
			deltaStats, err = compareDirectoriesNumStat(ctx, beforeDir, afterDir)
			if err != nil {
				slog.Debug("changeset numstat failed; returning path-only diff stat entries", "error", err)
				deltaStats = nil
			}
		}
		paths = computedPaths
		statsByPath = deltaStats
		return nil
	})
	if err != nil {
		return nil, err
	}

	return buildDiffStats(paths, statsByPath), nil
}

// buildDiffStats turns computed changeset paths and their line counts into the
// reported diff stat entries, deciding which directory entries are worth
// reporting on their own.
func buildDiffStats(paths *ChangesetPaths, statsByPath map[string]lineChanges) []*DiffStat {
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
		// An added directory is reported by the files it contains; emitting
		// the directory itself as well would double-count the same change
		// (e.g. "core/ ADDED" next to "core/probe.txt ADDED"). A directory
		// that adds no files is still worth reporting — it is the only
		// evidence the directory appeared at all.
		if strings.HasSuffix(path, "/") && pathHasReportedChildren(path, paths) {
			continue
		}
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
	// Use AllRemoved (uncollapsed) so every removed file gets its own entry.
	for _, path := range paths.AllRemoved {
		if renamedOld[path] {
			continue
		}
		// A removed directory whose removal is implied by the paths removed
		// beneath it carries no information: every consumer can infer it, git
		// doesn't track directories, and a unified diff (Changeset.asPatch)
		// cannot express it. Report only removals that nothing else records,
		// i.e. directories that held no files at all — the mirror of the
		// added empty directory, which is likewise the only record of itself.
		if strings.HasSuffix(path, "/") && removalImpliedByChildren(path, paths.AllRemoved) {
			continue
		}
		entries = append(entries, addEntry(path, DiffStatKindRemoved))
	}

	slices.SortFunc(entries, func(a, b *DiffStat) int {
		return strings.Compare(a.Path, b.Path)
	})
	return entries
}

// pathHasReportedChildren reports whether any added or modified path lies
// beneath the given directory path (which carries a trailing "/"), i.e.
// whether the directory's appearance is already described by per-file entries.
func pathHasReportedChildren(dir string, paths *ChangesetPaths) bool {
	for _, group := range [][]string{paths.Added, paths.Modified} {
		for _, p := range group {
			if p == dir || strings.HasSuffix(p, "/") {
				continue
			}
			if strings.HasPrefix(p, dir) {
				return true
			}
		}
	}
	return false
}

// removalImpliedByChildren reports whether a removed directory path (carrying
// a trailing "/") holds any removed file beneath it, i.e. whether its removal
// is already implied by per-file removal entries. Directories whose removal is
// implied are omitted from diff stats, matching git — which does not track
// directories — and the unified diff, which cannot represent one. A directory
// holding no files (empty, or holding only empty directories) is not implied
// by anything, so it stays reported.
func removalImpliedByChildren(dir string, allRemoved []string) bool {
	for _, p := range allRemoved {
		if p == dir || strings.HasSuffix(p, "/") {
			continue
		}
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
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

	// Determine the changed paths so we only diff things that actually changed,
	// rather than the entire tree
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute paths: %w", err)
	}
	noChanges := changesetPathsEmpty(paths)
	pathSpecs := gitDiffPathSpecs(paths)

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
				if err := os.Mkdir(beforeMount, 0o755); err != nil {
					return err
				}
				defer os.RemoveAll(beforeMount)
				if err := os.Mkdir(afterMount, 0o755); err != nil {
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

				if noChanges {
					// No paths actually changed (no-op Changeset) - just return early rather than
					// computing an expensive no-op diff.
					return nil
				}

				return enginetel.Task(ctx, "git diff", func(ctx context.Context) error {
					stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
					defer stdio.Close()
					return writeGitDiffPatch(ctx, root, pathSpecs,
						io.MultiWriter(patchFile, stdio.Stdout), stdio.Stdout, stdio.Stderr)
				})
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

// writeGitDiffPatch runs `git diff` between the a/ and b/ mount dirs beneath
// root and writes the resulting unified diff to out, normalizing the
// `diff --git` header lines along the way (see diffGitHeaderRewriter).
//
// logOut/logErr receive the command's own diagnostics; logOut also gets the
// argv, which the span name can't carry.
func writeGitDiffPatch(ctx context.Context, root string, pathSpecs []string, out, logOut, logErr io.Writer) error {
	// --no-renames: with --no-prefix, git strips the a/ b/ mount dirs
	// from the ---/+++ lines but not from rename from/to lines, so a
	// rename entry makes the patch unapplyable ("inconsistent old
	// filename"). Emitting renames as delete+add avoids the mismatch;
	// with --binary the result is identical.
	args := []string{"diff", "--binary", "--no-prefix", "--no-renames", "--no-index", "a", "b"}
	if len(pathSpecs) > 0 {
		// -- so a path starting with - isn't parsed as a flag, and
		// GIT_LITERAL_PATHSPECS below so one starting with : isn't
		// parsed as pathspec magic. Either would otherwise fail the
		// diff or silently drop the path from the patch.
		args = append(args, "--")
		args = append(args, pathSpecs...)
	}
	// The span is named for the command rather than the whole argv, which the
	// pathspecs make unbounded; log those.
	fmt.Fprintln(logOut, "running git", strings.Join(args, " "))

	rewriter := &diffGitHeaderRewriter{w: out}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1")
	cmd.Stdout = rewriter
	cmd.Stderr = logErr
	runErr := cmd.Run()
	if flushErr := rewriter.Flush(); flushErr != nil && runErr == nil {
		return flushErr
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		// Exit code 1 just means the trees differ, which is the
		// whole point; returning it would end the span in error
		// for every patch that isn't empty.
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		// NB: we could technically populate an ExecError here, but that
		// feels like it leaks implementation details; "exit status 128" isn't
		// exactly clear
		return fmt.Errorf("failed to generate patch: %w", runErr)
	}
	return nil
}

// diffGitHeaderRewriter is an io.Writer that passes a unified diff through
// unchanged apart from its `diff --git` header lines, which it rewrites via
// fixDiffGitHeader. Data is buffered until a newline, so callers must Flush
// once the source is exhausted.
type diffGitHeaderRewriter struct {
	w   io.Writer
	buf []byte
}

func (r *diffGitHeaderRewriter) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(r.buf[:i])
		r.buf = r.buf[i+1:]
		if _, err := io.WriteString(r.w, fixDiffGitHeader(line)+"\n"); err != nil {
			return 0, err
		}
	}
}

// Flush writes any trailing data not terminated by a newline.
func (r *diffGitHeaderRewriter) Flush() error {
	if len(r.buf) == 0 {
		return nil
	}
	line := string(r.buf)
	r.buf = nil
	_, err := io.WriteString(r.w, fixDiffGitHeader(line))
	return err
}

// fixDiffGitHeader normalizes the path prefixes on a `diff --git` header line.
//
// AsPatch diffs two bind mounts literally named a/ and b/ with --no-prefix, so
// the header line carries whichever mount dir the file exists in on both
// sides: an added file yields "diff --git b/f b/f" and a deleted one
// "diff --git a/f a/f". Real git always writes "diff --git a/<path> b/<path>"
// regardless of the change kind (only the ---/+++ lines use /dev/null), and
// `git apply` — plus anything else consuming these patches — expects that, so
// rewrite the prefixes to a/ and b/.
//
// Lines that aren't headers, or that don't parse as a header, are returned
// unchanged. Diff payload lines can never be mistaken for a header: text hunk
// lines always start with ' ', '+', '-' or '\', and --binary payload lines are
// base85, which has no space or '-'.
func fixDiffGitHeader(line string) string {
	const marker = "diff --git "
	rest, ok := strings.CutPrefix(line, marker)
	if !ok {
		return line
	}
	// The two paths only differ in their leading mount dir (--no-renames means
	// git never emits a rename header here), so they have equal length and the
	// separating space sits exactly in the middle. Splitting there — rather
	// than on the first space — keeps paths containing spaces intact.
	var left, right string
	if mid := len(rest) / 2; len(rest)%2 == 1 && rest[mid] == ' ' {
		left, right = rest[:mid], rest[mid+1:]
	} else if fields := strings.Split(rest, " "); len(fields) == 2 {
		left, right = fields[0], fields[1]
	} else {
		return line
	}
	newLeft, leftOK := retagDiffPath(left, 'a')
	newRight, rightOK := retagDiffPath(right, 'b')
	if !leftOK || !rightOK {
		return line
	}
	return marker + newLeft + " " + newRight
}

// retagDiffPath replaces the leading a/ or b/ prefix of a path as it appears on
// a `diff --git` line with the given tag, accounting for git's C-style quoting
// of paths with unusual characters. It reports false if the path doesn't have
// such a prefix, in which case the caller leaves the line alone.
func retagDiffPath(p string, tag byte) (string, bool) {
	i := 0
	if strings.HasPrefix(p, `"`) {
		i = 1
	}
	if len(p) < i+2 || (p[i] != 'a' && p[i] != 'b') || p[i+1] != '/' {
		return "", false
	}
	return p[:i] + string(tag) + p[i+1:], true
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
		// A directory present in both changesets is not a conflict: git's
		// 3-way merge unions directories, so disjoint files under a common
		// new directory merge cleanly. Only a file added in both sides is a
		// real conflict. Directories carry a trailing slash (see
		// listSubdirectories); skip them here.
		if strings.HasSuffix(addedPath, "/") {
			continue
		}
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

	ourContent, err := ch.content(ctx)
	if err != nil {
		return nil, fmt.Errorf("materialize our changes: %w", err)
	}
	theirContent, err := other.content(ctx)
	if err != nil {
		return nil, fmt.Errorf("materialize their changes: %w", err)
	}

	afterDir, err := gitMergeChangesets(ctx,
		before,
		ourContent, theirContent,
		conflicts,
		onConflictStrategy,
	)
	if err != nil {
		return nil, err
	}

	return newChangesetFromMerge(ctx, before, afterDir)
}

// maxParallelChangesets bounds how many changesets are worked on at once.
// Each job mounts both of a changeset's snapshots and walks them, so this is
// I/O bound work holding real resources for its duration, not something to fan
// out one goroutine per changeset over.
const maxParallelChangesets = 8

// changesetJobs returns a job pool for per-changeset work, capped so that a
// merge of many changesets doesn't mount all of them at once.
func changesetJobs() parallel.Jobs {
	return parallel.New().
		WithContextualTracer(true).
		WithInternal(true).
		WithLimit(maxParallelChangesets)
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
	// Before wasting any effort, remove any changesets that are empty.
	//
	// This asks ComputePaths rather than IsEmpty: every surviving changeset
	// needs its paths computed anyway and ComputePaths memoizes, whereas
	// IsEmpty would mount and walk both trees all over again for each one. It
	// also counts directory-only changes, which IsEmpty deliberately ignores
	// the way `git diff --quiet` does.
	filtered := make([]*Changeset, len(others))
	jobs := changesetJobs()
	for i, other := range others {
		jobs = jobs.WithJob(fmt.Sprintf("changeset %d paths", i), func(ctx context.Context) error {
			paths, err := other.ComputePaths(ctx)
			if err != nil {
				return fmt.Errorf("compute paths for changeset %d: %w", i, err)
			}
			if !changesetPathsEmpty(paths) {
				filtered[i] = other
			}
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	others = slices.DeleteFunc(filtered, func(cs *Changeset) bool { return cs == nil })

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

	err := enginetel.Task(ctx, "checking pairwise conflicts", func(ctx context.Context) error {
		return checkAllPairwiseConflicts(ctx, ch, others)
	})
	if err != nil && onConflictStrategy == FailEarlyOnConflicts {
		return nil, err
	}

	before, err := enginetel.TaskRet(ctx, "merging before directories", func(ctx context.Context) (dagql.ObjectResult[*Directory], error) {
		return mergeBeforeDirectories(ctx, ch, others...)
	})
	if err != nil {
		return nil, err
	}

	// Each diff is an independent snapshot-diff materialization, so take them
	// all at once.
	var ourContent *changesetContent
	otherContents := make([]*changesetContent, len(others))
	contentJobs := changesetJobs().WithJob("self changes", func(ctx context.Context) error {
		content, err := ch.content(ctx)
		if err != nil {
			return fmt.Errorf("materialize our changes: %w", err)
		}
		ourContent = content
		return nil
	})
	for i, other := range others {
		contentJobs = contentJobs.WithJob(fmt.Sprintf("changeset %d changes", i), func(ctx context.Context) error {
			content, err := other.content(ctx)
			if err != nil {
				return fmt.Errorf("materialize changes for changeset %d: %w", i, err)
			}
			otherContents[i] = content
			return nil
		})
	}
	if err := contentJobs.Run(ctx); err != nil {
		return nil, err
	}

	afterDir, err := enginetel.TaskRet(ctx, "octopus merge", func(ctx context.Context) (*Directory, error) {
		return gitOctopusMergeChangesets(ctx, before, ourContent, otherContents)
	})
	if err != nil {
		return nil, err
	}

	return enginetel.TaskRet(ctx, "new changeset from merge", func(ctx context.Context) (*Changeset, error) {
		return newChangesetFromMerge(ctx, before, afterDir)
	})
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
	// Changesets merged together are normally diffs taken against a common
	// base, so their before directories are usually the same directory
	// repeated. Merging a directory onto itself is a no-op, but each merge
	// still walks the whole tree, so folding in N identical copies costs N
	// full-tree copies to produce what the first one already produced.
	//
	// Only consecutive repeats are collapsed. Dropping every repeat would
	// reorder a sequence like A, B, A, where the trailing A is what decides
	// paths that A and B disagree on.
	// haveLast rather than comparing against the zero digest: an empty digest
	// would otherwise read as a repeat of nothing and drop the first before
	// directory, silently changing the merge base.
	var lastDigest digest.Digest
	var haveLast bool
	appendBefore := func(before dagql.ObjectResult[*Directory]) error {
		return enginetel.Task(ctx, "append before", func(ctx context.Context) error {
			dgst, err := before.ContentPreferredDigest(ctx)
			if err != nil {
				return fmt.Errorf("before content-preferred digest: %w", err)
			}
			if haveLast && dgst == lastDigest {
				return nil
			}
			id, err := before.ID()
			if err != nil {
				return fmt.Errorf("before ID: %w", err)
			}
			lastDigest, haveLast = dgst, true
			selectors = append(selectors, withDirectorySelector(id))
			return nil
		})
	}

	if err := appendBefore(ch.Before); err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}
	for _, other := range others {
		if err := appendBefore(other.Before); err != nil {
			return dagql.ObjectResult[*Directory]{}, err
		}
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
	jobs := changesetJobs()
	for i, other := range others {
		jobs = jobs.WithJob(fmt.Sprintf("changeset %d paths", i), func(ctx context.Context) error {
			paths, err := other.ComputePaths(ctx)
			if err != nil {
				return fmt.Errorf("compute paths for changeset %d: %w", i, err)
			}
			otherPaths[i] = paths
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return err
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

// changesetContent is a changeset reduced to its file-level content: a diff
// directory holding its added and modified files, plus the computed paths
// describing removals and empty added directories. Unlike a patch, this
// content can be applied onto any base — content the base already contains
// overlays as a no-op instead of double-applying or failing a hunk.
type changesetContent struct {
	// diff is the materialized Before.diff(After) directory — the same
	// content Directory.WithChanges and Changeset.Export apply. Zero when the
	// changeset adds and modifies nothing.
	diff  dagql.ObjectResult[*Directory]
	paths *ChangesetPaths
}

// content reduces the changeset to its file-level content, independent of its
// before directory.
func (ch *Changeset) content(ctx context.Context) (*changesetContent, error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute paths: %w", err)
	}
	content := &changesetContent{paths: paths}
	if len(paths.Added) == 0 && len(paths.Modified) == 0 {
		// Nothing to overlay; removals are carried by paths alone.
		return content, nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	afterID, err := ch.After.ID()
	if err != nil {
		return nil, fmt.Errorf("after ID: %w", err)
	}
	var diffDir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, ch.Before, &diffDir,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*Directory](afterID)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("compute changes diff directory: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, diffDir); err != nil {
		return nil, fmt.Errorf("evaluate changes diff directory: %w", err)
	}
	content.diff = diffDir
	return content, nil
}

// gitMergeWorkspace is a mounted scratch copy of the merge base that git
// branches are built in.
type gitMergeWorkspace struct {
	root    string // mounted snapshot root
	dir     string // base directory selector within root
	workDir string // absolute path of dir under root; where git runs
}

// applyContent applies a changeset's file-level content to the work tree:
// removed paths are deleted, the diff directory is overlaid, and empty added
// directories are recreated (the diff snapshot does not carry them). This
// mirrors Directory.WithChanges' application of the same content.
func (ws *gitMergeWorkspace) applyContent(ctx context.Context, content *changesetContent) error {
	if err := removeChangesetPaths(ws.root, ws.dir, content.paths.Removed); err != nil {
		return fmt.Errorf("remove paths: %w", err)
	}

	// The copier must write through the mounted view, not the snapshot's
	// upperdir: git runs inside the mount and has to see every file this
	// writes, and modifying an overlay's layers behind a live mount leaves
	// the view incoherent (git add sees names it cannot stat). So no Mount is
	// passed here, unlike Directory.WithChanges, which never reads back
	// through the mount before committing.
	copier, err := layercopy.NewCopier(layercopy.Mount{Root: ws.root})
	if err != nil {
		return err
	}
	defer copier.Close()

	if content.diff.Self() != nil {
		diffRef, err := content.diff.Self().Snapshot.GetOrEval(ctx, content.diff.Result)
		if err != nil {
			return fmt.Errorf("diff snapshot: %w", err)
		}
		diffPath, err := content.diff.Self().Dir.GetOrEval(ctx, content.diff.Result)
		if err != nil {
			return fmt.Errorf("diff path: %w", err)
		}
		if diffPath == "" {
			diffPath = "/"
		}
		if diffRef != nil {
			err = MountRef(ctx, diffRef, func(srcRoot string, srcMnt *mount.Mount) error {
				return copier.Copy(ctx,
					layercopy.Mount{Root: srcRoot, Mount: srcMnt},
					diffPath,
					ws.dir,
					layercopy.CopyOptions{
						CopyDirContents: true,
						ReplaceExisting: true,
						// Linking from the diff snapshot into the mounted
						// work tree crosses devices, so every attempt would
						// just fail into the copy fallback.
						DisableSourceHardlinks: true,
					},
				)
			}, mountRefAsReadOnly)
			if err != nil {
				return fmt.Errorf("copy changed paths: %w", err)
			}
		}
	}

	if err := mkdirChangesetAddedDirs(ctx, copier, ws.dir, content.paths); err != nil {
		return err
	}
	return ws.touchAppliedPaths(content.paths)
}

// touchAppliedPaths bumps the mtime of every path the changeset wrote so git
// can see the change. Snapshot contents carry normalized timestamps and the
// copier preserves them, so a same-size edit can leave a file's mtime and
// size both identical to the index entry; with core.checkStat=minimal (see
// gitEphemeralConfig) git add would then skip re-hashing it and silently
// drop the change from the branch commit.
func (ws *gitMergeWorkspace) touchAppliedPaths(paths *ChangesetPaths) error {
	for _, p := range slices.Concat(paths.Added, paths.Modified) {
		if strings.HasSuffix(p, "/") {
			// Directories are untracked by git; only file stat data matters.
			continue
		}
		full, err := RootPathWithoutFinalSymlink(ws.root, path.Join(ws.dir, p))
		if err != nil {
			return err
		}
		err = unix.UtimesNanoAt(unix.AT_FDCWD, full, nil, unix.AT_SYMLINK_NOFOLLOW)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("touch %s: %w", p, err)
		}
	}
	return nil
}

// withGitMergeWorkspace sets up a workspace for git merge operations, runs the provided
// function, then commits and returns the resulting directory.
func withGitMergeWorkspace(ctx context.Context, base dagql.ObjectResult[*Directory], description string, fn func(ws *gitMergeWorkspace) error) (*Directory, error) {
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
		return fn(&gitMergeWorkspace{
			root:    root,
			dir:     baseSelector,
			workDir: workDir,
		})
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

func gitMergeChangesets(
	ctx context.Context,
	base dagql.ObjectResult[*Directory],
	ours, theirs *changesetContent,
	conflicts Conflicts,
	strategy WithChangesetMergeConflict,
) (*Directory, error) {
	return withGitMergeWorkspace(ctx, base, "Changeset.withChangeset git merge", func(ws *gitMergeWorkspace) error {
		if err := initGitRepo(ctx, ws.workDir); err != nil {
			return err
		}
		if err := createBranchWithContent(ctx, ws, "ours", ours); err != nil {
			return err
		}
		if err := createBranchWithContent(ctx, ws, "theirs", theirs, "HEAD~1"); err != nil {
			return err
		}
		if err := runGit(ctx, ws.workDir, "checkout", "ours"); err != nil {
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

		mergeErr := runGit(ctx, ws.workDir, mergeArgs...)

		switch strategy {
		case FailOnConflict:
			if mergeErr != nil {
				return mergeErr
			}
		case LeaveConflictMarkers, PreferOursOnConflict, PreferTheirsOnConflict:
			modifyDeleteConflicts := conflicts.ModifyDeletePaths()
			if len(modifyDeleteConflicts) > 0 {
				if err := resolveModifyDeleteConflicts(ctx, ws.workDir, modifyDeleteConflicts, strategy, ours.paths.AllRemoved, theirs.paths.AllRemoved); err != nil {
					return err
				}
			}
		default:
			if mergeErr != nil {
				return mergeErr
			}
		}

		if err := os.RemoveAll(filepath.Join(ws.workDir, ".git")); err != nil {
			return fmt.Errorf("remove temporary merge git repository: %w", err)
		}
		return nil
	})
}

func gitOctopusMergeChangesets(
	ctx context.Context,
	base dagql.ObjectResult[*Directory],
	ourContent *changesetContent,
	otherContents []*changesetContent,
) (*Directory, error) {
	return withGitMergeWorkspace(ctx, base, "Changeset.withChangesets git octopus merge", func(ws *gitMergeWorkspace) error {
		if err := enginetel.Task(ctx, "init git", func(ctx context.Context) error {
			return initGitRepo(ctx, ws.workDir)
		}); err != nil {
			return err
		}
		if err := enginetel.Task(ctx, "create branch for ours", func(ctx context.Context) error {
			return createBranchWithContent(ctx, ws, "ours", ourContent)
		}); err != nil {
			return err
		}

		branchNames := make([]string, len(otherContents))
		for i, content := range otherContents {
			branchName := fmt.Sprintf("branch_%d", i)
			branchNames[i] = branchName
			if err := enginetel.Task(ctx, "create branch "+branchName, func(ctx context.Context) error {
				return createBranchWithContent(ctx, ws, branchName, content, "HEAD~1")
			}); err != nil {
				return err
			}
		}

		if err := enginetel.Task(ctx, "checkout ours", func(ctx context.Context) error {
			return runGit(ctx, ws.workDir, "checkout", "ours")
		}); err != nil {
			return err
		}

		mergeArgs := []string{"merge", "--no-edit", "--no-commit"}
		mergeArgs = append(mergeArgs, branchNames...)

		if err := enginetel.Task(ctx, "git merge", func(ctx context.Context) error {
			return runGit(ctx, ws.workDir, mergeArgs...)
		}); err != nil {
			return err
		}

		if err := os.RemoveAll(filepath.Join(ws.workDir, ".git")); err != nil {
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
	// Snapshots share file contents via hardlinks, so a file's ctime, inode
	// and device can all change out from under us (e.g. when a concurrent
	// snapshot hardlinks the same inode) without the content changing.
	//
	// Left alone, git sees these as stat-dirty entries, and `git merge`'s
	// internal `git stash create` fails with a bewildering "fatal: stash
	// failed".
	//
	// 'minimal' compares only mtime + size, which is all we can trust here.
	"-c", "core.checkStat=minimal",
	"-c", "core.trustctime=false",
}

func runGit(ctx context.Context, dir string, args ...string) error {
	_, err := runGitEnv(ctx, dir, nil, args...)
	return err
}

// runGitEnv runs git in dir with extra environment entries layered over the
// hermetic base environment, returning its standard output. Errors carry the
// standard error stream, which is where git reports what went wrong.
func runGitEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	gitArgs := make([]string, 0, len(gitEphemeralConfig)+len(args))
	gitArgs = append(gitArgs, gitEphemeralConfig...)
	gitArgs = append(gitArgs, args...)

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=/dev/null",
		"GIT_AUTHOR_NAME=Dagger",
		"GIT_AUTHOR_EMAIL=dagger@localhost",
		"GIT_COMMITTER_NAME=Dagger",
		"GIT_COMMITTER_EMAIL=dagger@localhost",
	}, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	return stdout.String(), nil
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

// createBranchWithContent creates branchName from startPoint (default: the
// current HEAD) and populates it with the changeset's file-level content.
func createBranchWithContent(ctx context.Context, ws *gitMergeWorkspace, branchName string, content *changesetContent, startPoint ...string) error {
	checkoutArgs := []string{"checkout", "-b", branchName}
	if len(startPoint) > 0 {
		checkoutArgs = append(checkoutArgs, startPoint[0])
	}
	if err := runGit(ctx, ws.workDir, checkoutArgs...); err != nil {
		return err
	}
	if err := ws.applyContent(ctx, content); err != nil {
		return fmt.Errorf("apply %s changes: %w", branchName, err)
	}
	// Always commit (even if empty) to ensure consistent commit structure
	// This is needed so that HEAD~1 references work correctly
	if err := runGit(ctx, ws.workDir, "add", "-A"); err != nil {
		return err
	}
	if err := runGit(ctx, ws.workDir, "commit", "--allow-empty", "-m", branchName); err != nil {
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

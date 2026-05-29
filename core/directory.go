package core

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	containerdfs "github.com/containerd/continuity/fs"
	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/layercopy"
	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/dustin/go-humanize"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/moby/go-archive"
	"github.com/moby/go-archive/chrootarchive"
	"github.com/moby/go-archive/compression"
)

// Directory is a content-addressed directory.
type Directory struct {
	Platform Platform
	// Services necessary to provision the directory.
	Services ServiceBindings

	Lazy     Lazy[*Directory]
	Dir      *LazyAccessor[string, *Directory] // a selected subdir of the rootfs of the on-disk Result, if any
	Snapshot *LazyAccessor[bkcache.ImmutableRef, *Directory]
}

func (*Directory) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Directory",
		NonNull:   true,
	}
}

func (*Directory) TypeDescription() string {
	return "A directory."
}

var _ dagql.OnReleaser = (*Directory)(nil)
var _ dagql.HasDependencyResults = (*Directory)(nil)
var _ dagql.HasDependencyResultsKinds = (*Directory)(nil)
var _ dagql.HasLazyEvaluation = (*Directory)(nil)

func (dir *Directory) OnRelease(ctx context.Context) error {
	if dir == nil || dir.Snapshot == nil {
		return nil
	}
	snapshot, ok := dir.Snapshot.Peek()
	if ok && snapshot != nil {
		return snapshot.Release(ctx)
	}
	return nil
}

func (dir *Directory) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	depsWithKinds, err := dir.AttachDependencyResultsKinds(ctx, self, attach)
	if err != nil {
		return nil, err
	}
	deps := make([]dagql.AnyResult, 0, len(depsWithKinds))
	for _, dep := range depsWithKinds {
		deps = append(deps, dep.Result)
	}
	return deps, nil
}

func (dir *Directory) AttachDependencyResultsKinds(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.DependencyResult, error) {
	if dir == nil {
		return nil, nil
	}
	serviceDeps, err := dir.Services.AttachDependencyResults("directory", attach)
	if err != nil {
		return nil, err
	}
	if dir.Lazy == nil {
		return serviceDeps, nil
	}
	lazyDeps, err := dir.Lazy.AttachDependencies(ctx, attach)
	if err != nil {
		return nil, err
	}
	deps := make([]dagql.DependencyResult, 0, len(serviceDeps)+len(lazyDeps))
	deps = append(deps, serviceDeps...)
	for _, dep := range lazyDeps {
		// Liveness-only: receiver/prerequisite chain. Failures attribute to
		// the parent's own install span via direct lookup, not transitively
		// onto downstream chained calls.
		deps = append(deps, dagql.DependencyResult{Result: dep, Owned: false})
	}
	return deps, nil
}

func (dir *Directory) LazyEvalFunc() dagql.LazyEvalFunc {
	if dir == nil || dir.Lazy == nil {
		return nil
	}
	return func(ctx context.Context) error {
		// Successful lazy evaluation materializes the directory into a plain
		// value. Clearing Lazy keeps Lazy != nil as a truthful signal that the
		// directory still has deferred work.
		lazy := dir.Lazy
		if err := lazy.Evaluate(ctx, dir); err != nil {
			return err
		}
		if dir.Lazy == lazy {
			dir.Lazy = nil
		}
		return nil
	}
}

func (dir *Directory) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if dir == nil {
		return 0, false, nil
	}
	if dir.Snapshot == nil {
		return 0, false, nil
	}
	snapshot, ok := dir.Snapshot.Peek()
	if !ok || snapshot == nil {
		return 0, false, nil
	}
	if snapshot.SnapshotID() != identity {
		return 0, false, nil
	}
	size, err := snapshot.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (dir *Directory) CacheUsageIdentities() []string {
	if dir == nil || dir.Snapshot == nil {
		return nil
	}
	snapshot, ok := dir.Snapshot.Peek()
	if !ok || snapshot == nil {
		return nil
	}
	return []string{snapshot.SnapshotID()}
}

func (dir *Directory) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if dir == nil || dir.Snapshot == nil {
		return nil
	}
	snapshot, ok := dir.Snapshot.Peek()
	if !ok || snapshot == nil {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{
		{
			RefKey: snapshot.SnapshotID(),
			Role:   "snapshot",
		},
	}
}

const (
	persistedDirectoryFormSnapshot = "snapshot"
	persistedDirectoryFormLazy     = "lazy"
)

const (
	persistedDirectoryLazyKindContainerRootFS               = "container.rootfs"
	persistedDirectoryLazyKindContainerDirectory            = "container.directory"
	persistedDirectoryLazyKindWithDirectory                 = "directory.withDirectory"
	persistedDirectoryLazyKindWithDirectoryDockerfileCompat = "directory.__withDirectoryDockerfileCompat"
	persistedDirectoryLazyKindWithPatchFile                 = "directory.withPatchFile"
	persistedDirectoryLazyKindWithNewFile                   = "directory.withNewFile"
	persistedDirectoryLazyKindWithFile                      = "directory.withFile"
	persistedDirectoryLazyKindWithTimestamps                = "directory.withTimestamps"
	persistedDirectoryLazyKindWithNewDirectory              = "directory.withNewDirectory"
	persistedDirectoryLazyKindSubdirectory                  = "directory.directory"
	persistedDirectoryLazyKindDiff                          = "directory.diff"
	persistedDirectoryLazyKindWithChanges                   = "directory.withChanges"
	persistedDirectoryLazyKindWithout                       = "directory.without"
	persistedDirectoryLazyKindWithSymlink                   = "directory.withSymlink"
	persistedDirectoryLazyKindChown                         = "directory.chown"
)

type persistedDirectoryPayload struct {
	Form     string                    `json:"form"`
	Dir      string                    `json:"dir,omitempty"`
	Platform Platform                  `json:"platform"`
	Services []persistedServiceBinding `json:"services,omitempty"`
	LazyKind string                    `json:"lazyKind,omitempty"`
	LazyJSON json.RawMessage           `json:"lazyJSON,omitempty"`
}

func (dir *Directory) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	if dir == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted directory: nil directory")
	}
	dirPath := ""
	if dir.Dir != nil {
		if peekedDir, ok := dir.Dir.Peek(); ok {
			dirPath = peekedDir
		}
	}
	services, err := encodePersistedServiceBindings(cache, "directory", dir.Services)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedDirectoryPayload{
		Dir:      dirPath,
		Platform: dir.Platform,
		Services: services,
	}
	if dir.Snapshot != nil {
		if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
			payload.Form = persistedDirectoryFormSnapshot
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted directory payload: %w", err)
			}
			return dagql.PersistedObjectEncoding{
				JSON: payloadJSON,
				SnapshotLinks: []dagql.PersistedSnapshotRefLink{{
					RefKey: snapshot.SnapshotID(),
					Role:   "snapshot",
				}},
			}, nil
		}
	}
	if dir.Lazy != nil {
		payload.Form = persistedDirectoryFormLazy
		lazyKind, lazyJSON, err := encodePersistedDirectoryLazy(ctx, cache, dir.Lazy)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.LazyKind = lazyKind
		payload.LazyJSON = lazyJSON
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted directory payload: %w", err)
		}
		return encodePersistedObjectRawJSON(payloadJSON), nil
	}
	slog.Error(
		"encode persisted directory: neither snapshot nor lazy op is available",
		"dir", dirPath,
		"platform", dir.Platform,
		"services", len(dir.Services),
	)
	return dagql.PersistedObjectEncoding{}, fmt.Errorf("%w: encode persisted directory: missing snapshot and lazy op", dagql.ErrPersistStateNotReady)
}

//nolint:dupl // symmetric with decodePersistedFileWithSnapshotRole in file.go; sharing hides type specifics
func decodePersistedDirectoryWithSnapshotRole(ctx context.Context, dag *dagql.Server, resultID uint64, payload json.RawMessage, snapshotRole string) (*Directory, error) {
	var persisted persistedDirectoryPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted directory payload: %w", err)
	}
	services, err := decodePersistedServiceBindings(ctx, dag, "directory", persisted.Services)
	if err != nil {
		return nil, err
	}

	dir := &Directory{
		Platform: persisted.Platform,
		Services: services,
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	if persisted.Dir != "" {
		dir.Dir.setValue(persisted.Dir)
	}
	switch persisted.Form {
	case persistedDirectoryFormSnapshot:
		snapshot, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "directory", snapshotRole)
		if err != nil {
			return nil, err
		}
		dir.Snapshot.setValue(snapshot)
		return dir, nil
	case persistedDirectoryFormLazy:
		if persisted.LazyKind == "" {
			return nil, fmt.Errorf("decode persisted directory payload: missing lazy kind")
		}
		lazy, err := decodePersistedDirectoryLazy(ctx, dag, persisted.LazyKind, persisted.LazyJSON)
		if err != nil {
			return nil, err
		}
		dir.Lazy = lazy
		return dir, nil
	default:
		return nil, fmt.Errorf("decode persisted directory payload: unsupported form %q", persisted.Form)
	}
}

func (*Directory) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	return decodePersistedDirectoryWithSnapshotRole(ctx, dag, resultID, payload, "snapshot")
}

func loadCanonicalScratchDirectory(ctx context.Context) (string, bkcache.ImmutableRef, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("load canonical scratch directory: %w", err)
	}
	scratchSnapshot, err := query.SnapshotManager().Scratch(ctx)
	if err != nil {
		return "", nil, err
	}
	return "/", scratchSnapshot, nil
}

type DirectoryWithDirectoryLazy struct {
	LazyState
	Parent      dagql.ObjectResult[*Directory]
	DestDir     string
	Source      dagql.ObjectResult[*Directory]
	Filter      CopyFilter
	Owner       string
	Permissions *int
}

type DirectoryWithDirectoryDockerfileCompatLazy struct {
	LazyState
	Parent                           dagql.ObjectResult[*Directory]
	DestDir                          string
	SrcPath                          string
	Source                           dagql.ObjectResult[*Directory]
	Filter                           CopyFilter
	Owner                            string
	Permissions                      *int
	FollowSymlink                    bool
	DirCopyContents                  bool
	AttemptUnpackDockerCompatibility bool
	CreateDestPath                   bool
	AllowWildcard                    bool
	AllowEmptyWildcard               bool
	AlwaysReplaceExistingDestPaths   bool
}

type DirectoryWithPatchFileLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	Patch  dagql.ObjectResult[*File]
}

type DirectoryWithNewFileLazy struct {
	LazyState
	Parent      dagql.ObjectResult[*Directory]
	Dest        string
	Content     []byte
	Permissions fs.FileMode
	Ownership   *Ownership
}

type DirectoryWithFileLazy struct {
	LazyState
	Parent                           dagql.ObjectResult[*Directory]
	DestPath                         string
	Source                           dagql.ObjectResult[*File]
	Permissions                      *int
	Owner                            string
	DoNotCreateDestPath              bool
	AttemptUnpackDockerCompatibility bool
}

type DirectoryWithTimestampsLazy struct {
	LazyState
	Parent    dagql.ObjectResult[*Directory]
	Timestamp int
}

type DirectoryWithNewDirectoryLazy struct {
	LazyState
	Parent      dagql.ObjectResult[*Directory]
	Dest        string
	Permissions fs.FileMode
}

type DirectorySubdirectoryLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	Subdir string
}

type DirectoryDiffLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	Other  dagql.ObjectResult[*Directory]
}

type DirectoryWithChangesLazy struct {
	LazyState
	Parent  dagql.ObjectResult[*Directory]
	Changes dagql.ObjectResult[*Changeset]
}

type DirectoryWithoutLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	Paths  []string
}

type DirectoryWithSymlinkLazy struct {
	LazyState
	Parent   dagql.ObjectResult[*Directory]
	Target   string
	LinkName string
}

type DirectoryChownLazy struct {
	LazyState
	Parent    dagql.ObjectResult[*Directory]
	ChownPath string
	Owner     string
}

type persistedDirectoryWithDirectoryLazy struct {
	ParentResultID uint64     `json:"parentResultID"`
	DestDir        string     `json:"destDir"`
	SourceResultID uint64     `json:"sourceResultID"`
	Filter         CopyFilter `json:"filter"`
	Owner          string     `json:"owner,omitempty"`
	Permissions    *int       `json:"permissions,omitempty"`
}

type persistedDirectoryWithDirectoryDockerfileCompatLazy struct {
	ParentResultID                   uint64     `json:"parentResultID"`
	DestDir                          string     `json:"destDir"`
	SrcPath                          string     `json:"srcPath,omitempty"`
	SourceResultID                   uint64     `json:"sourceResultID"`
	Filter                           CopyFilter `json:"filter"`
	Owner                            string     `json:"owner,omitempty"`
	Permissions                      *int       `json:"permissions,omitempty"`
	FollowSymlink                    bool       `json:"followSymlink,omitempty"`
	DirCopyContents                  bool       `json:"dirCopyContents,omitempty"`
	AttemptUnpackDockerCompatibility bool       `json:"attemptUnpackDockerCompatibility,omitempty"`
	CreateDestPath                   bool       `json:"createDestPath,omitempty"`
	AllowWildcard                    bool       `json:"allowWildcard,omitempty"`
	AllowEmptyWildcard               bool       `json:"allowEmptyWildcard,omitempty"`
	AlwaysReplaceExistingDestPaths   bool       `json:"alwaysReplaceExistingDestPaths,omitempty"`
}

type persistedDirectoryWithPatchFileLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	PatchResultID  uint64 `json:"patchResultID"`
}

type persistedDirectoryWithNewFileLazy struct {
	ParentResultID uint64      `json:"parentResultID"`
	Dest           string      `json:"dest"`
	Content        []byte      `json:"content"`
	Permissions    fs.FileMode `json:"permissions"`
	Ownership      *Ownership  `json:"ownership,omitempty"`
}

type persistedDirectoryWithFileLazy struct {
	ParentResultID                   uint64 `json:"parentResultID"`
	DestPath                         string `json:"destPath"`
	SourceResultID                   uint64 `json:"sourceResultID"`
	Permissions                      *int   `json:"permissions,omitempty"`
	Owner                            string `json:"owner,omitempty"`
	DoNotCreateDestPath              bool   `json:"doNotCreateDestPath,omitempty"`
	AttemptUnpackDockerCompatibility bool   `json:"attemptUnpackDockerCompatibility,omitempty"`
}

type persistedDirectoryWithTimestampsLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Timestamp      int    `json:"timestamp"`
}

type persistedDirectoryWithNewDirectoryLazy struct {
	ParentResultID uint64      `json:"parentResultID"`
	Dest           string      `json:"dest"`
	Permissions    fs.FileMode `json:"permissions"`
}

type persistedDirectorySubdirectoryLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Subdir         string `json:"subdir"`
}

type persistedDirectoryDiffLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	OtherResultID  uint64 `json:"otherResultID"`
}

type persistedDirectoryWithChangesLazy struct {
	ParentResultID  uint64 `json:"parentResultID"`
	ChangesResultID uint64 `json:"changesResultID"`
}

type persistedDirectoryWithoutLazy struct {
	ParentResultID uint64   `json:"parentResultID"`
	Paths          []string `json:"paths"`
}

type persistedDirectoryWithSymlinkLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	LinkName       string `json:"linkName"`
}

type persistedDirectoryChownLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	ChownPath      string `json:"chownPath"`
	Owner          string `json:"owner"`
}

func attachDirectoryResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*Directory], label string) (dagql.ObjectResult[*Directory], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

func attachFileResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*File], label string) (dagql.ObjectResult[*File], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*File]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*File])
	if !ok {
		return dagql.ObjectResult[*File]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

func encodePersistedDirectoryLazy(ctx context.Context, cache dagql.PersistedObjectCache, lazy Lazy[*Directory]) (string, json.RawMessage, error) {
	switch lazy := lazy.(type) {
	case *ContainerRootFSLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindContainerRootFS, payload, err
	case *ContainerDirectoryLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindContainerDirectory, payload, err
	case *DirectoryWithDirectoryLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithDirectory, payload, err
	case *DirectoryWithDirectoryDockerfileCompatLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithDirectoryDockerfileCompat, payload, err
	case *DirectoryWithPatchFileLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithPatchFile, payload, err
	case *DirectoryWithNewFileLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithNewFile, payload, err
	case *DirectoryWithFileLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithFile, payload, err
	case *DirectoryWithTimestampsLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithTimestamps, payload, err
	case *DirectoryWithNewDirectoryLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithNewDirectory, payload, err
	case *DirectorySubdirectoryLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindSubdirectory, payload, err
	case *DirectoryDiffLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindDiff, payload, err
	case *DirectoryWithChangesLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithChanges, payload, err
	case *DirectoryWithoutLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithout, payload, err
	case *DirectoryWithSymlinkLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindWithSymlink, payload, err
	case *DirectoryChownLazy:
		payload, err := lazy.EncodePersisted(ctx, cache)
		return persistedDirectoryLazyKindChown, payload, err
	default:
		return "", nil, fmt.Errorf("encode persisted directory lazy: unsupported lazy type %T", lazy)
	}
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func decodePersistedDirectoryLazy(ctx context.Context, dag *dagql.Server, lazyKind string, payload json.RawMessage) (Lazy[*Directory], error) {
	switch lazyKind {
	case persistedDirectoryLazyKindContainerRootFS:
		var persisted persistedContainerRootFSLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted container rootfs lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container rootfs parent")
		if err != nil {
			return nil, err
		}
		return &ContainerRootFSLazy{LazyState: NewLazyState(), Parent: parent}, nil
	case persistedDirectoryLazyKindContainerDirectory:
		var persisted persistedContainerDirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted container directory lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container directory parent")
		if err != nil {
			return nil, err
		}
		return &ContainerDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Path: persisted.Path}, nil
	case persistedDirectoryLazyKindWithDirectory:
		var persisted persistedDirectoryWithDirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withDirectory lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withDirectory parent")
		if err != nil {
			return nil, err
		}
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "directory withDirectory source")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithDirectoryLazy{
			LazyState:   NewLazyState(),
			Parent:      parent,
			DestDir:     persisted.DestDir,
			Source:      source,
			Filter:      persisted.Filter,
			Owner:       persisted.Owner,
			Permissions: persisted.Permissions,
		}, nil
	case persistedDirectoryLazyKindWithDirectoryDockerfileCompat:
		var persisted persistedDirectoryWithDirectoryDockerfileCompatLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory __withDirectoryDockerfileCompat lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory __withDirectoryDockerfileCompat parent")
		if err != nil {
			return nil, err
		}
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "directory __withDirectoryDockerfileCompat source")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithDirectoryDockerfileCompatLazy{
			LazyState:                        NewLazyState(),
			Parent:                           parent,
			DestDir:                          persisted.DestDir,
			SrcPath:                          persisted.SrcPath,
			Source:                           source,
			Filter:                           persisted.Filter,
			Owner:                            persisted.Owner,
			Permissions:                      persisted.Permissions,
			FollowSymlink:                    persisted.FollowSymlink,
			DirCopyContents:                  persisted.DirCopyContents,
			AttemptUnpackDockerCompatibility: persisted.AttemptUnpackDockerCompatibility,
			CreateDestPath:                   persisted.CreateDestPath,
			AllowWildcard:                    persisted.AllowWildcard,
			AllowEmptyWildcard:               persisted.AllowEmptyWildcard,
			AlwaysReplaceExistingDestPaths:   persisted.AlwaysReplaceExistingDestPaths,
		}, nil
	case persistedDirectoryLazyKindWithPatchFile:
		var persisted persistedDirectoryWithPatchFileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withPatchFile lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withPatchFile parent")
		if err != nil {
			return nil, err
		}
		patch, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.PatchResultID, "directory withPatchFile patch")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithPatchFileLazy{LazyState: NewLazyState(), Parent: parent, Patch: patch}, nil
	case persistedDirectoryLazyKindWithNewFile:
		var persisted persistedDirectoryWithNewFileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withNewFile lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withNewFile parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithNewFileLazy{
			LazyState:   NewLazyState(),
			Parent:      parent,
			Dest:        persisted.Dest,
			Content:     persisted.Content,
			Permissions: persisted.Permissions,
			Ownership:   persisted.Ownership,
		}, nil
	case persistedDirectoryLazyKindWithFile:
		var persisted persistedDirectoryWithFileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withFile lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withFile parent")
		if err != nil {
			return nil, err
		}
		source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.SourceResultID, "directory withFile source")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithFileLazy{
			LazyState:                        NewLazyState(),
			Parent:                           parent,
			DestPath:                         persisted.DestPath,
			Source:                           source,
			Permissions:                      persisted.Permissions,
			Owner:                            persisted.Owner,
			DoNotCreateDestPath:              persisted.DoNotCreateDestPath,
			AttemptUnpackDockerCompatibility: persisted.AttemptUnpackDockerCompatibility,
		}, nil
	case persistedDirectoryLazyKindWithTimestamps:
		var persisted persistedDirectoryWithTimestampsLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withTimestamps lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withTimestamps parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithTimestampsLazy{LazyState: NewLazyState(), Parent: parent, Timestamp: persisted.Timestamp}, nil
	case persistedDirectoryLazyKindWithNewDirectory:
		var persisted persistedDirectoryWithNewDirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withNewDirectory lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withNewDirectory parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithNewDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Dest: persisted.Dest, Permissions: persisted.Permissions}, nil
	case persistedDirectoryLazyKindSubdirectory:
		var persisted persistedDirectorySubdirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory directory lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory subdirectory parent")
		if err != nil {
			return nil, err
		}
		return &DirectorySubdirectoryLazy{LazyState: NewLazyState(), Parent: parent, Subdir: persisted.Subdir}, nil
	case persistedDirectoryLazyKindDiff:
		var persisted persistedDirectoryDiffLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory diff lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory diff parent")
		if err != nil {
			return nil, err
		}
		other, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.OtherResultID, "directory diff other")
		if err != nil {
			return nil, err
		}
		return &DirectoryDiffLazy{LazyState: NewLazyState(), Parent: parent, Other: other}, nil
	case persistedDirectoryLazyKindWithChanges:
		var persisted persistedDirectoryWithChangesLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withChanges lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withChanges parent")
		if err != nil {
			return nil, err
		}
		changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dag, persisted.ChangesResultID, "directory withChanges changes")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithChangesLazy{LazyState: NewLazyState(), Parent: parent, Changes: changes}, nil
	case persistedDirectoryLazyKindWithout:
		var persisted persistedDirectoryWithoutLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory without lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory without parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithoutLazy{LazyState: NewLazyState(), Parent: parent, Paths: persisted.Paths}, nil
	case persistedDirectoryLazyKindWithSymlink:
		var persisted persistedDirectoryWithSymlinkLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory withSymlink lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory withSymlink parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryWithSymlinkLazy{LazyState: NewLazyState(), Parent: parent, Target: persisted.Target, LinkName: persisted.LinkName}, nil
	case persistedDirectoryLazyKindChown:
		var persisted persistedDirectoryChownLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted directory chown lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "directory chown parent")
		if err != nil {
			return nil, err
		}
		return &DirectoryChownLazy{LazyState: NewLazyState(), Parent: parent, ChownPath: persisted.ChownPath, Owner: persisted.Owner}, nil
	default:
		return nil, fmt.Errorf("decode persisted directory lazy payload: unsupported lazy kind %q", lazyKind)
	}
}

func (lazy *DirectoryWithDirectoryLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withDirectory", func(ctx context.Context) error {
		return dir.WithDirectory(ctx, lazy.Parent, lazy.DestDir, lazy.Source, lazy.Filter, lazy.Owner, lazy.Permissions)
	})
}

func (lazy *DirectoryWithDirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withDirectory parent")
	if err != nil {
		return nil, err
	}
	source, err := attachDirectoryResult(attach, lazy.Source, "attach directory withDirectory source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *DirectoryWithDirectoryLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withDirectory parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "directory withDirectory source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithDirectoryLazy{
		ParentResultID: parentID,
		DestDir:        lazy.DestDir,
		SourceResultID: sourceID,
		Filter:         lazy.Filter,
		Owner:          lazy.Owner,
		Permissions:    lazy.Permissions,
	})
}

func (lazy *DirectoryWithDirectoryDockerfileCompatLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withDirectoryDockerfileCompat", func(ctx context.Context) error {
		return dir.WithDirectoryDockerfileCompat(
			ctx,
			lazy.Parent,
			lazy.DestDir,
			lazy.SrcPath,
			lazy.Source,
			lazy.Filter,
			lazy.Owner,
			lazy.Permissions,
			lazy.FollowSymlink,
			lazy.DirCopyContents,
			lazy.AttemptUnpackDockerCompatibility,
			lazy.CreateDestPath,
			lazy.AllowWildcard,
			lazy.AllowEmptyWildcard,
			lazy.AlwaysReplaceExistingDestPaths,
		)
	})
}

func (lazy *DirectoryWithDirectoryDockerfileCompatLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withDirectoryDockerfileCompat parent")
	if err != nil {
		return nil, err
	}
	source, err := attachDirectoryResult(attach, lazy.Source, "attach directory withDirectoryDockerfileCompat source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *DirectoryWithDirectoryDockerfileCompatLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withDirectoryDockerfileCompat parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "directory withDirectoryDockerfileCompat source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithDirectoryDockerfileCompatLazy{
		ParentResultID:                   parentID,
		DestDir:                          lazy.DestDir,
		SrcPath:                          lazy.SrcPath,
		SourceResultID:                   sourceID,
		Filter:                           lazy.Filter,
		Owner:                            lazy.Owner,
		Permissions:                      lazy.Permissions,
		FollowSymlink:                    lazy.FollowSymlink,
		DirCopyContents:                  lazy.DirCopyContents,
		AttemptUnpackDockerCompatibility: lazy.AttemptUnpackDockerCompatibility,
		CreateDestPath:                   lazy.CreateDestPath,
		AllowWildcard:                    lazy.AllowWildcard,
		AllowEmptyWildcard:               lazy.AllowEmptyWildcard,
		AlwaysReplaceExistingDestPaths:   lazy.AlwaysReplaceExistingDestPaths,
	})
}

func (lazy *DirectoryWithPatchFileLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withPatchFile", func(ctx context.Context) error {
		return dir.applyPatchFileResult(ctx, lazy.Parent, lazy.Patch)
	})
}

func (lazy *DirectoryWithPatchFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withPatchFile parent")
	if err != nil {
		return nil, err
	}
	patch, err := attachFileResult(attach, lazy.Patch, "attach directory withPatchFile patch")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Patch = patch
	return []dagql.AnyResult{parent, patch}, nil
}

func (lazy *DirectoryWithPatchFileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withPatchFile parent")
	if err != nil {
		return nil, err
	}
	patchID, err := encodePersistedObjectRef(cache, lazy.Patch, "directory withPatchFile patch")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithPatchFileLazy{ParentResultID: parentID, PatchResultID: patchID})
}

func (lazy *DirectoryWithNewFileLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withNewFile", func(ctx context.Context) error {
		return dir.WithNewFile(ctx, lazy.Parent, lazy.Dest, lazy.Content, lazy.Permissions, lazy.Ownership)
	})
}

func (lazy *DirectoryWithNewFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withNewFile parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryWithNewFileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withNewFile parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithNewFileLazy{
		ParentResultID: parentID,
		Dest:           lazy.Dest,
		Content:        lazy.Content,
		Permissions:    lazy.Permissions,
		Ownership:      lazy.Ownership,
	})
}

func (lazy *DirectoryWithFileLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withFile", func(ctx context.Context) error {
		return dir.WithFile(ctx, lazy.Parent, lazy.DestPath, lazy.Source, lazy.Permissions, lazy.Owner, lazy.DoNotCreateDestPath, lazy.AttemptUnpackDockerCompatibility)
	})
}

func (lazy *DirectoryWithFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withFile parent")
	if err != nil {
		return nil, err
	}
	source, err := attachFileResult(attach, lazy.Source, "attach directory withFile source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *DirectoryWithFileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withFile parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "directory withFile source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithFileLazy{
		ParentResultID:                   parentID,
		DestPath:                         lazy.DestPath,
		SourceResultID:                   sourceID,
		Permissions:                      lazy.Permissions,
		Owner:                            lazy.Owner,
		DoNotCreateDestPath:              lazy.DoNotCreateDestPath,
		AttemptUnpackDockerCompatibility: lazy.AttemptUnpackDockerCompatibility,
	})
}

func (lazy *DirectoryWithTimestampsLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withTimestamps", func(ctx context.Context) error {
		return dir.WithTimestamps(ctx, lazy.Parent, lazy.Timestamp)
	})
}

func (lazy *DirectoryWithTimestampsLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withTimestamps parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryWithTimestampsLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withTimestamps parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithTimestampsLazy{ParentResultID: parentID, Timestamp: lazy.Timestamp})
}

func (lazy *DirectoryWithNewDirectoryLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withNewDirectory", func(ctx context.Context) error {
		return dir.WithNewDirectory(ctx, lazy.Parent, lazy.Dest, lazy.Permissions)
	})
}

func (lazy *DirectoryWithNewDirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withNewDirectory parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryWithNewDirectoryLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withNewDirectory parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithNewDirectoryLazy{ParentResultID: parentID, Dest: lazy.Dest, Permissions: lazy.Permissions})
}

func (lazy *DirectorySubdirectoryLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.directory", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}

		parentDir, err := lazy.Parent.Self().Dir.GetOrEval(ctx, lazy.Parent.Result)
		if err != nil {
			return fmt.Errorf("failed to get parent directory path: %w", err)
		}

		finalDir := parentDir
		if cleanDotsAndSlashes(lazy.Subdir) != "" {
			finalDir = path.Join(parentDir, lazy.Subdir)

			query, err := CurrentQuery(ctx)
			if err != nil {
				return err
			}
			srv, err := query.Server.Server(ctx)
			if err != nil {
				return err
			}

			// Validate the subdirectory during lazy evaluation so unresolved
			// subdirectory selections stay cheap until they are actually needed.
			info, err := lazy.Parent.Self().Stat(ctx, lazy.Parent, srv, lazy.Subdir, false)
			if err != nil {
				return RestoreErrPath(err, lazy.Subdir)
			}
			if info.FileType != FileTypeDirectory {
				return notADirectoryError{fmt.Errorf("path %s is a file, not a directory", lazy.Subdir)}
			}
		}

		parentSnapshot, err := lazy.Parent.Self().Snapshot.GetOrEval(ctx, lazy.Parent.Result)
		if err != nil {
			return fmt.Errorf("failed to get parent directory snapshot: %w", err)
		}

		if parentSnapshot == nil {
			scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
			if err != nil {
				return err
			}
			dir.Dir.setValue(scratchDir)
			dir.Snapshot.setValue(scratchSnapshot)
			return nil
		}
		dir.Dir.setValue(finalDir)

		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, parentSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		dir.Snapshot.setValue(reopened)
		return nil
	})
}

func (lazy *DirectorySubdirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory directory parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectorySubdirectoryLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory directory parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectorySubdirectoryLazy{ParentResultID: parentID, Subdir: lazy.Subdir})
}

func (lazy *DirectoryDiffLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.diff", func(ctx context.Context) error {
		return dir.Diff(ctx, lazy.Parent, lazy.Other)
	})
}

func (lazy *DirectoryDiffLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory diff parent")
	if err != nil {
		return nil, err
	}
	other, err := attachDirectoryResult(attach, lazy.Other, "attach directory diff other")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Other = other
	return []dagql.AnyResult{parent, other}, nil
}

func (lazy *DirectoryDiffLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory diff parent")
	if err != nil {
		return nil, err
	}
	otherID, err := encodePersistedObjectRef(cache, lazy.Other, "directory diff other")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryDiffLazy{ParentResultID: parentID, OtherResultID: otherID})
}

func (lazy *DirectoryWithChangesLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withChanges", func(ctx context.Context) error {
		return dir.WithChanges(ctx, lazy.Parent, lazy.Changes)
	})
}

func (lazy *DirectoryWithChangesLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withChanges parent")
	if err != nil {
		return nil, err
	}
	attached, err := attach(lazy.Changes)
	if err != nil {
		return nil, fmt.Errorf("attach directory withChanges changes: %w", err)
	}
	changes, ok := attached.(dagql.ObjectResult[*Changeset])
	if !ok {
		return nil, fmt.Errorf("attach directory withChanges changes: unexpected result %T", attached)
	}
	lazy.Parent = parent
	lazy.Changes = changes
	return []dagql.AnyResult{parent, changes}, nil
}

func (lazy *DirectoryWithChangesLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withChanges parent")
	if err != nil {
		return nil, err
	}
	changesID, err := encodePersistedObjectRef(cache, lazy.Changes, "directory withChanges changes")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithChangesLazy{ParentResultID: parentID, ChangesResultID: changesID})
}

func (lazy *DirectoryWithoutLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.without", func(ctx context.Context) error {
		return dir.Without(ctx, lazy.Parent, dagql.CurrentCall(ctx), true, lazy.Paths...)
	})
}

func (lazy *DirectoryWithoutLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory without parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryWithoutLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory without parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithoutLazy{ParentResultID: parentID, Paths: slices.Clone(lazy.Paths)})
}

func (lazy *DirectoryWithSymlinkLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withSymlink", func(ctx context.Context) error {
		return dir.WithSymlink(ctx, lazy.Parent, lazy.Target, lazy.LinkName)
	})
}

func (lazy *DirectoryWithSymlinkLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory withSymlink parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryWithSymlinkLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory withSymlink parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryWithSymlinkLazy{ParentResultID: parentID, Target: lazy.Target, LinkName: lazy.LinkName})
}

func (lazy *DirectoryChownLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.chown", func(ctx context.Context) error {
		return dir.Chown(ctx, lazy.Parent, lazy.ChownPath, lazy.Owner)
	})
}

func (lazy *DirectoryChownLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach directory chown parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryChownLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "directory chown parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryChownLazy{ParentResultID: parentID, ChownPath: lazy.ChownPath, Owner: lazy.Owner})
}

func (dir *Directory) Digest(ctx context.Context, self dagql.ObjectResult[*Directory]) (string, error) {
	snapshot, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate directory: %w", err)
	}
	if snapshot == nil {
		return "", fmt.Errorf("failed to evaluate null directory")
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return "", fmt.Errorf("failed to get directory path: %w", err)
	}

	digest, err := bkcontenthash.Checksum(
		ctx,
		snapshot,
		dirPath,
		bkcontenthash.ChecksumOpts{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to compute digest: %w", err)
	}

	return digest.String(), nil
}

func (dir *Directory) Entries(ctx context.Context, self dagql.ObjectResult[*Directory], src string) ([]string, error) {
	paths := []string{}
	useSlash := SupportsDirSlash(ctx)
	snapshot, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	src = path.Join(dirPath, src)
	if snapshot == nil {
		err = errEmptyResultRef
	} else {
		err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
			resolvedDir, err := containerdfs.RootPath(root, src)
			if err != nil {
				return err
			}
			entries, err := os.ReadDir(resolvedDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				path := entry.Name()
				if useSlash && entry.IsDir() {
					path += "/"
				}
				paths = append(paths, path)
			}
			return nil
		}, mountRefAsReadOnly)
	}
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			// empty directory, i.e. llb.Scratch()
			if clean := path.Clean(src); clean == "." || clean == "/" {
				return []string{}, nil
			}
			return nil, fmt.Errorf("%s: %w", src, os.ErrNotExist)
		}
		return nil, err
	}
	return paths, nil
}

// patternWithoutTrailingGlob is from fsuitls
func patternWithoutTrailingGlob(p *patternmatcher.Pattern) string {
	patStr := p.String()
	// We use filepath.Separator here because patternmatcher.Pattern patterns
	// get transformed to use the native path separator:
	// https://github.com/moby/patternmatcher/blob/130b41bafc16209dc1b52a103fdac1decad04f1a/patternmatcher.go#L52
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"**")
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"*")
	return patStr
}

// Glob returns a list of files that matches the given pattern.
func (dir *Directory) Glob(ctx context.Context, self dagql.ObjectResult[*Directory], pattern string) ([]string, error) {
	paths := []string{}

	pat, err := patternmatcher.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create glob pattern matcher: %w", err)
	}

	// from fsutils
	patternChars := "*[]?^"
	if filepath.Separator != '\\' {
		patternChars += `\`
	}
	onlyPrefixIncludes := !strings.ContainsAny(patternWithoutTrailingGlob(pat), patternChars)

	useSlash := SupportsDirSlash(ctx)
	snapshot, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		err = errEmptyResultRef
	} else {
		err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
			resolvedDir, err := containerdfs.RootPath(root, dirPath)
			if err != nil {
				return err
			}

			return filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, prevErr error) error {
				if prevErr != nil {
					return prevErr
				}

				path, err := filepath.Rel(resolvedDir, path)
				if err != nil {
					return err
				}
				// Skip root
				if path == "." {
					return nil
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					break
				}

				match, err := pat.Match(path)
				if err != nil {
					return err
				}

				if match {
					if useSlash && d.IsDir() {
						path += "/"
					}
					paths = append(paths, path)
				} else if d.IsDir() && onlyPrefixIncludes {
					// fsutils Optimization: we can skip walking this dir if no include
					// patterns could match anything inside it.
					dirSlash := path + string(filepath.Separator)
					if !pat.Exclusion() {
						patStr := patternWithoutTrailingGlob(pat) + string(filepath.Separator)
						if !strings.HasPrefix(patStr, dirSlash) {
							return filepath.SkipDir
						}
					}
				}

				return nil
			})
		}, mountRefAsReadOnly)
	}
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			// empty directory, i.e. llb.Scratch()
			return []string{}, nil
		}
		return nil, err
	}

	return paths, nil
}

func (dir *Directory) WithNewFile(ctx context.Context, parent dagql.ObjectResult[*Directory], dest string, content []byte, permissions fs.FileMode, ownership *Ownership) error {
	err := validateFileName(dest)
	if err != nil {
		return err
	}

	if permissions == 0 {
		permissions = 0o644
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withNewFile %s (%s)", dest, humanize.Bytes(uint64(len(content))))),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		resolvedDest, err := containerdfs.RootPath(root, path.Join(parentDir, dest))
		if err != nil {
			return err
		}
		destPathDir, _ := filepath.Split(resolvedDest)
		err = os.MkdirAll(filepath.Dir(destPathDir), 0755)
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(resolvedDest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, permissions)
		if err != nil {
			return err
		}
		defer func() {
			if dst != nil {
				_ = dst.Close()
			}
		}()

		_, err = dst.Write(content)
		if err != nil {
			return err
		}

		err = dst.Close()
		if err != nil {
			return err
		}
		dst = nil

		if ownership != nil {
			err = os.Chown(resolvedDest, ownership.UID, ownership.GID)
			if err != nil {
				return fmt.Errorf("failed to set chown %s: err", resolvedDest)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snapshot)
	return nil
}

func (dir *Directory) applyPatchToSnapshot(ctx context.Context, parentRef bkcache.ImmutableRef, dirPath string, patch dagql.ObjectResult[*File]) (bkcache.ImmutableRef, error) {
	reopenParent := func() (bkcache.ImmutableRef, error) {
		if parentRef == nil {
			return nil, nil
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		return query.SnapshotManager().GetBySnapshotID(ctx, parentRef.SnapshotID(), bkcache.NoUpdateLastUsed)
	}

	patchSnapshot, err := patch.Self().Snapshot.GetOrEval(ctx, patch.Result)
	if err != nil {
		return nil, err
	}
	if patchSnapshot == nil {
		return reopenParent()
	}
	patchPath, err := patch.Self().File.GetOrEval(ctx, patch.Result)
	if err != nil {
		return nil, err
	}

	patchIsEmpty := false
	err = MountRef(ctx, patchSnapshot, func(patchRoot string, _ *mount.Mount) error {
		resolvedPatchPath, err := containerdfs.RootPath(patchRoot, patchPath)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolvedPatchPath)
		if err != nil {
			return fmt.Errorf("stat patch file: %w", err)
		}
		patchIsEmpty = info.Size() == 0
		return nil
	}, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}
	if patchIsEmpty {
		return reopenParent()
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	newRef, err := query.SnapshotManager().New(ctx, parentRef, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("patch"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		return MountRef(ctx, patchSnapshot, func(patchRoot string, _ *mount.Mount) error {
			resolvedPatchPath, err := containerdfs.RootPath(patchRoot, patchPath)
			if err != nil {
				return err
			}
			patchFile, err := os.Open(resolvedPatchPath)
			if err != nil {
				return fmt.Errorf("open patch file: %w", err)
			}
			defer patchFile.Close()
			return applyGitPatch(ctx, resolvedDir, patchFile, stdio)
		}, mountRefAsReadOnly)
	})
	if err != nil {
		return nil, err
	}
	return newRef.Commit(ctx)
}

func (dir *Directory) withoutPathsFromSnapshot(ctx context.Context, parentSnapshot bkcache.ImmutableRef, dirPath string, paths ...string) (bkcache.ImmutableRef, bool, error) {
	if parentSnapshot == nil {
		return nil, false, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("without %s", strings.Join(paths, ","))),
	)
	if err != nil {
		return nil, false, err
	}

	var anyPathsRemoved bool
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		for _, p := range paths {
			p = path.Join(dirPath, p)
			var matches []string
			if strings.Contains(p, "*") {
				var err error
				matches, err = layercopy.ResolveWildcards(root, p, true)
				if err != nil {
					return err
				}
			} else {
				matches = []string{p}
			}

			for _, m := range matches {
				fullPath, err := RootPathWithoutFinalSymlink(root, m)
				if err != nil {
					return err
				}
				_, statErr := os.Lstat(fullPath)
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				} else if statErr != nil {
					return statErr
				}

				anyPathsRemoved = true
				if err := os.RemoveAll(fullPath); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return nil, false, err
	}
	return snapshot, anyPathsRemoved, nil
}

func (dir *Directory) applyPatchFileResult(ctx context.Context, parent dagql.ObjectResult[*Directory], patch dagql.ObjectResult[*File]) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent, patch); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	snap, err := dir.applyPatchToSnapshot(ctx, parentRef, parentDir, patch)
	if err != nil {
		return err
	}
	if snap == nil {
		scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		dir.Dir.setValue(scratchDir)
		dir.Snapshot.setValue(scratchSnapshot)
		return nil
	}
	dir.Snapshot.setValue(snap)
	return nil
}

func applyGitPatch(ctx context.Context, dir string, patch io.Reader, stdio telemetry.SpanStreams) error {
	apply := exec.CommandContext(ctx, "git", "apply", "--allow-empty", "-")
	apply.Dir = dir
	apply.Stdin = patch
	apply.Stdout = stdio.Stdout
	apply.Stderr = stdio.Stderr
	if err := apply.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git apply: %w", err)
	}
	return nil
}

func (dir *Directory) Search(ctx context.Context, self dagql.ObjectResult[*Directory], opts SearchOpts, verbose bool, paths []string, globs []string) ([]*SearchResult, error) {
	// Validate and normalize paths to prevent directory traversal attacks
	for i, p := range paths {
		// If absolute, make it relative to the directory
		if filepath.IsAbs(p) {
			paths[i] = strings.TrimPrefix(p, "/")
		}

		// Clean the path (e.g., remove ../, ./, etc.)
		paths[i] = filepath.Clean(paths[i])

		// Check if the normalized path would escape the directory
		if !filepath.IsLocal(paths[i]) {
			return nil, fmt.Errorf("path cannot escape directory: %s", p)
		}
	}

	ref, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		// empty directory, i.e. llb.Scratch()
		return []*SearchResult{}, nil
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}

	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))

	results := []*SearchResult{}
	err = MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		rgArgs := opts.RipgrepArgs()
		for _, glob := range globs {
			rgArgs = append(rgArgs, "--glob="+glob)
		}
		if len(paths) > 0 {
			rgArgs = append(rgArgs, "--")
			for _, p := range paths {
				resolved, err := containerdfs.RootPath(resolvedDir, p)
				if err != nil {
					return err
				}
				// make it relative, now that it's safe, just for less obtuse errors
				resolved, err = filepath.Rel(resolvedDir, resolved)
				if err != nil {
					return err
				}
				rgArgs = append(rgArgs, resolved)
			}
		}
		rg := exec.Command("rg", rgArgs...)
		rg.Dir = resolvedDir
		results, err = opts.RunRipgrep(ctx, rg, verbose)
		return err
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// cleanDotsAndSlashes is similar to path.Clean; however it does not remove any directory names, e.g. "keep/../this//.//" will return "keep/../this".
// This is needed for cases where a referenced directory is a symlink, e.g. consider keep linking to some/other/directory, then keep/../this,
// would end up being some/other/directory/../this, which would end up as some/other/this
func cleanDotsAndSlashes(path string) string {
	cleaned := []string{}
	for _, d := range filepath.SplitList(path) {
		if d == "" || d == "." || d == "/" {
			continue
		}
		cleaned = append(cleaned, d)
	}
	return filepath.Join(cleaned...)
}

func (dir *Directory) Subdirectory(ctx context.Context, parent dagql.ObjectResult[*Directory], subdir string) (*Directory, error) {
	_ = ctx
	return &Directory{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &DirectorySubdirectoryLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Subdir:    subdir,
		},
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}, nil
}

type notADirectoryError struct {
	inner error
}

func (e notADirectoryError) Error() string {
	return e.inner.Error()
}

func (e notADirectoryError) Unwrap() error {
	return e.inner
}

func (dir *Directory) Subfile(ctx context.Context, parent dagql.ObjectResult[*Directory], file string) (*File, error) {
	_ = ctx
	subfile := &File{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &FileSubfileLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Path:      file,
		},
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	if parentDir, ok := parent.Self().Dir.Peek(); ok {
		subfile.File.setValue(filepath.Join(parentDir, file))
	}
	return subfile, nil
}

type notAFileError struct {
	inner error
}

func (e notAFileError) Error() string {
	return e.inner.Error()
}

func (e notAFileError) Unwrap() error {
	return e.inner
}

type CopyFilter struct {
	Exclude   []string `default:"[]"`
	Include   []string `default:"[]"`
	Gitignore bool     `default:"false"`
}

func (cf *CopyFilter) IsEmpty() bool {
	if cf == nil {
		return true
	}
	return len(cf.Exclude) == 0 && len(cf.Include) == 0 && !cf.Gitignore
}

func layercopyOwnership(ownership *Ownership) *layercopy.Ownership {
	if ownership == nil {
		return nil
	}
	return &layercopy.Ownership{UID: ownership.UID, GID: ownership.GID}
}

func layercopyMode(permissions *int) *os.FileMode {
	if permissions == nil {
		return nil
	}
	raw := *permissions
	mode := os.FileMode(raw) & os.ModePerm
	if raw&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if raw&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if raw&syscall.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return &mode
}

func (dir *Directory) WithDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*Directory],
	destDir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
	permissions *int,
) error {
	dagqlCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := dagqlCache.Evaluate(ctx, parent, src); err != nil {
		return err
	}

	baseDir := parent.Self()
	if baseDir == nil {
		return fmt.Errorf("directory withDirectory base: nil directory")
	}
	srcDirObj := src.Self()
	if srcDirObj == nil {
		return fmt.Errorf("directory withDirectory source: nil directory")
	}

	parentRef, ourDir, err := materializedDirectorySnapshotAndPath(baseDir)
	if err != nil {
		return err
	}
	dir.Dir.setValue(ourDir)

	srcRef, err := srcDirObj.Snapshot.GetOrEval(ctx, src.Result)
	if err != nil {
		return fmt.Errorf("failed to get source directory ref: %w", err)
	}
	srcDir, err := srcDirObj.Dir.GetOrEval(ctx, src.Result)
	if err != nil {
		return fmt.Errorf("failed to get source directory path: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current query: %w", err)
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentRef,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Directory.withDirectory"),
	)
	if err != nil {
		return fmt.Errorf("snapshotmanager.New failed: %w", err)
	}
	defer newRef.Release(context.WithoutCancel(ctx))

	destDir = directoryWithDirectoryDest(ourDir, destDir)
	var usage snapshots.Usage
	if err := MountRef(ctx, newRef, func(destRoot string, destMnt *mount.Mount) error {
		var ownership *Ownership
		if owner != "" {
			var err error
			ownership, err = resolveDirectoryOwner(destRoot, owner)
			if err != nil {
				return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
			}
		}
		opts := layercopy.CopyOptions{
			Filter: layercopy.Filter{
				Include:   filter.Include,
				Exclude:   filter.Exclude,
				Gitignore: filter.Gitignore,
			},
			Chown:           layercopyOwnership(ownership),
			Mode:            layercopyMode(permissions),
			CopyDirContents: true,
			ReplaceExisting: true,
		}

		copier, err := layercopy.NewCopier(layercopy.Mount{Root: destRoot, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()

		if srcRef == nil {
			if err := copier.Mkdir(ctx, destDir, opts); err != nil {
				return err
			}
		} else {
			if err := MountRef(ctx, srcRef, func(srcRoot string, srcMnt *mount.Mount) error {
				return copier.Copy(ctx, layercopy.Mount{Root: srcRoot, Mount: srcMnt}, srcDir, destDir, opts)
			}, mountRefAsReadOnly); err != nil {
				return err
			}
		}
		usage, err = copier.Usage()
		return err
	}); err != nil {
		return err
	}

	ref, err := newRef.CommitWithUsage(ctx, usage)
	if err != nil {
		return fmt.Errorf("failed to commit copied directory: %w", err)
	}
	dir.Snapshot.setValue(ref)
	return nil
}

func materializedDirectorySnapshotAndPath(dir *Directory) (bkcache.ImmutableRef, string, error) {
	if dir == nil {
		return nil, "", fmt.Errorf("materialized directory: nil directory")
	}
	if dir.Lazy != nil {
		return nil, "", fmt.Errorf("materialized directory: still lazy %T", dir.Lazy)
	}
	dirRef, ok := dir.Snapshot.Peek()
	if !ok {
		return nil, "", fmt.Errorf("materialized directory: missing snapshot")
	}
	dirPath, ok := dir.Dir.Peek()
	if !ok {
		return nil, "", fmt.Errorf("materialized directory: missing path")
	}
	return dirRef, dirPath, nil
}

func directoryWithDirectoryDest(baseDir, destDir string) string {
	destDirTrailingSlash := strings.HasSuffix(destDir, "/") || strings.HasSuffix(destDir, "/.")
	destDir = path.Join(baseDir, destDir)
	if destDirTrailingSlash && !strings.HasSuffix(destDir, "/") {
		destDir += "/"
	}
	return destDir
}

//nolint:gocyclo
func (dir *Directory) WithDirectoryDockerfileCompat(
	ctx context.Context,
	parent dagql.ObjectResult[*Directory],
	destDir string,
	srcPath string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
	permissions *int,
	_ bool,
	_ bool,
	attemptUnpackDockerCompatibility bool,
	createDestPath bool,
	_ bool,
	_ bool,
	_ bool,
) error {
	dagqlCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := dagqlCache.Evaluate(ctx, parent, src); err != nil {
		return err
	}
	ourDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get directory path: %w", err)
	}
	dir.Dir.setValue(ourDir)
	dirRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get directory ref: %w", err)
	}
	srcDir, err := src.Self().Dir.GetOrEval(ctx, src.Result)
	if err != nil {
		return fmt.Errorf("failed to get source directory path: %w", err)
	}
	srcRef, err := src.Self().Snapshot.GetOrEval(ctx, src.Result)
	if err != nil {
		return fmt.Errorf("failed to get source directory ref: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current query: %w", err)
	}
	destDirTrailingSlash := strings.HasSuffix(destDir, "/") || strings.HasSuffix(destDir, "/.")
	destDir = path.Join(ourDir, destDir)
	if destDirTrailingSlash && !strings.HasSuffix(destDir, "/") {
		destDir += "/"
	}

	var parentRoot string
	var releaseParent func() error
	if dirRef != nil {
		parentRoot, _, releaseParent, err = MountRefCloser(ctx, dirRef, mountRefAsReadOnly)
		if err != nil {
			return fmt.Errorf("failed to mount parent directory: %w", err)
		}
		defer releaseParent()
	}

	newRef, err := query.SnapshotManager().New(
		ctx,
		dirRef,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Directory.withDirectoryDockerfileCompat"),
	)
	if err != nil {
		return fmt.Errorf("snapshotmanager.New failed: %w", err)
	}
	defer newRef.Release(context.WithoutCancel(ctx))

	var usage snapshots.Usage
	if err := MountRef(ctx, newRef, func(copyDest string, destMnt *mount.Mount) error {
		var ownership *Ownership
		if owner != "" {
			ownerRoot := copyDest
			if parentRoot != "" {
				ownerRoot = parentRoot
			}
			var err error
			ownership, err = resolveDirectoryOwner(ownerRoot, owner)
			if err != nil {
				return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
			}
		}

		copier, err := layercopy.NewCopier(layercopy.Mount{Root: copyDest, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()

		opts := layercopy.CopyOptions{
			Filter: layercopy.Filter{
				Include:   filter.Include,
				Exclude:   filter.Exclude,
				Gitignore: filter.Gitignore,
			},
			Chown:             layercopyOwnership(ownership),
			Mode:              layercopyMode(permissions),
			CopyDirContents:   true,
			ReplaceExisting:   true,
			DestPathHintIsDir: destDirTrailingSlash,
		}

		if srcRef == nil {
			if err := copier.Mkdir(ctx, destDir, opts); err != nil {
				return err
			}
			usage, err = copier.Usage()
			return err
		}

		if err := MountRef(ctx, srcRef, func(mntedSrcPath string, srcMnt *mount.Mount) error {
			if attemptUnpackDockerCompatibility && srcPath != "" {
				srcPathCopy := srcPath
				if srcDir != "" && srcDir != "/" {
					srcPathCopy = srcDir + "/" + srcPathCopy
				}
				didUnpack, err := attemptCopyArchiveUnpack(
					ctx,
					copier,
					layercopy.Mount{Root: mntedSrcPath, Mount: srcMnt},
					destDir,
					[]string{strings.TrimPrefix(srcPathCopy, "/")},
					filter.Exclude,
					filter.Gitignore,
					ownership,
					permissions,
					destDirTrailingSlash,
				)
				if err != nil {
					return fmt.Errorf("failed to unpack source archive: %w", err)
				}
				if didUnpack {
					return nil
				}
			}

			pathsToCopy := []string{srcDir}
			if srcPath != "" {
				srcPathWithDir := srcPath
				if srcDir != "" && srcDir != "/" {
					srcPathWithDir = srcDir + "/" + srcPathWithDir
				}
				pathsToCopy, err = layercopy.ResolveWildcards(mntedSrcPath, srcPathWithDir, true)
				if err != nil {
					return err
				}
			}

			for _, srcPath := range pathsToCopy {
				copyDestPath := destDir
				if !createDestPath {
					destDirPath := filepath.Dir(copyDestPath)
					existsRoot := copyDest
					if parentRoot != "" {
						existsRoot = parentRoot
					}
					resolvedExistsPath, err := containerdfs.RootPath(existsRoot, destDirPath)
					if err != nil {
						return err
					}
					if _, err := os.Lstat(resolvedExistsPath); err != nil {
						err = TrimErrPathPrefix(err, path.Join(mntedSrcPath, srcDir))
						err = TrimErrPathPrefix(err, existsRoot)
						return err
					}
				}
				if err := copier.Copy(ctx, layercopy.Mount{Root: mntedSrcPath, Mount: srcMnt}, srcPath, copyDestPath, opts); err != nil {
					err = TrimErrPathPrefix(err, path.Join(mntedSrcPath, srcDir))
					err = TrimErrPathPrefix(err, copyDest)
					return fmt.Errorf("failed to copy source directory: %w", err)
				}
			}
			return nil
		}, mountRefAsReadOnly); err != nil {
			return err
		}

		usage, err = copier.Usage()
		return err
	}); err != nil {
		return err
	}

	ref, err := newRef.CommitWithUsage(ctx, usage)
	if err != nil {
		return fmt.Errorf("failed to commit copied directory: %w", err)
	}
	dir.Snapshot.setValue(ref)
	return nil
}

func attemptCopyArchiveUnpack(
	ctx context.Context,
	copier *layercopy.Copier,
	srcMount layercopy.Mount,
	destPath string,
	includePatterns []string,
	excludePatterns []string,
	useGitignore bool,
	ownership *Ownership,
	permissions *int,
	destPathHintIsDirectory bool,
) (bool, error) {
	// Keep default path untouched for anything non-canonical to this compatibility mode.
	if useGitignore || len(excludePatterns) > 0 || len(includePatterns) == 0 {
		return false, nil
	}

	matches, ok, err := resolveAttemptUnpackMatches(srcMount.Root, includePatterns)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if len(matches) == 0 {
		// No matches means no copy work to do; handled here to keep fallback copy from
		// re-applying broader include semantics.
		return true, nil
	}

	opts := layercopy.CopyOptions{
		Chown:             layercopyOwnership(ownership),
		Mode:              layercopyMode(permissions),
		CopyDirContents:   true,
		ReplaceExisting:   true,
		DestPathHintIsDir: destPathHintIsDirectory,
	}

	for _, src := range matches {
		resolvedSrcPath, err := containerdfs.RootPath(srcMount.Root, src)
		if err != nil {
			return false, err
		}

		if isArchivePath(resolvedSrcPath) {
			resolvedDestPath, err := copier.MaterializeDestDir(ctx, destPath)
			if err != nil {
				return false, err
			}
			if err := unpackArchiveFile(resolvedSrcPath, resolvedDestPath, ownership); err != nil {
				return false, err
			}
			continue
		}

		if len(matches) == 1 {
			copiedAsSingleFile, err := copyAttemptUnpackNonArchiveSingleFile(ctx, copier, srcMount, src, destPath, opts, destPathHintIsDirectory)
			if err != nil {
				return false, err
			}
			if copiedAsSingleFile {
				continue
			}
		}

		if err := copier.Copy(ctx, srcMount, src, destPath, opts); err != nil {
			return false, err
		}
	}

	return true, nil
}

func copyAttemptUnpackNonArchiveSingleFile(
	ctx context.Context,
	copier *layercopy.Copier,
	srcMount layercopy.Mount,
	srcPatternPath string,
	destPath string,
	opts layercopy.CopyOptions,
	destPathHintIsDirectory bool,
) (bool, error) {
	resolvedSrcPath, err := containerdfs.RootPath(srcMount.Root, srcPatternPath)
	if err != nil {
		return false, err
	}
	srcInfo, err := os.Stat(resolvedSrcPath)
	if err != nil {
		return false, err
	}
	if !srcInfo.Mode().IsRegular() {
		return false, nil
	}

	opts.DestPathHintIsDir = destPathHintIsDirectory
	return true, copier.CopyFile(ctx, srcMount, srcPatternPath, destPath, opts)
}

func resolveAttemptUnpackMatches(srcRoot string, includePatterns []string) ([]string, bool, error) {
	seen := make(map[string]struct{}, len(includePatterns))
	out := make([]string, 0, len(includePatterns))

	for _, includePattern := range includePatterns {
		if includePattern == "" || strings.HasPrefix(includePattern, "!") {
			return nil, false, nil
		}
		matches, err := layercopy.ResolveWildcards(srcRoot, includePattern, true)
		if err != nil {
			return nil, false, err
		}
		for _, match := range matches {
			if _, ok := seen[match]; ok {
				continue
			}
			seen[match] = struct{}{}
			out = append(out, match)
		}
	}

	return out, true, nil
}

func unpackArchiveFile(srcPath string, destPath string, ownership *Ownership) error {
	if !isArchivePath(srcPath) {
		return nil
	}

	if err := os.MkdirAll(destPath, 0o755); err != nil {
		return err
	}
	if ownership != nil {
		if err := os.Chown(destPath, ownership.UID, ownership.GID); err != nil {
			return err
		}
	}

	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	opts := &archive.TarOptions{
		BestEffortXattrs: true,
	}
	if err := chrootarchive.Untar(file, destPath, opts); err != nil {
		return err
	}
	return nil
}

func isArchivePath(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeType != 0 {
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	rdr, err := compression.DecompressStream(file)
	if err != nil {
		return false
	}
	defer rdr.Close()

	tr := tar.NewReader(rdr)
	_, err = tr.Next()
	return err == nil
}

func ensureCopyDestParentExists(ctx context.Context, baseRef bkcache.ImmutableRef, destPath string) error {
	parentPath := filepath.Dir(path.Clean(destPath))
	if parentPath == "." {
		parentPath = "/"
	}

	if baseRef == nil {
		if parentPath == "/" {
			return nil
		}
		return &os.PathError{Op: "lstat", Path: parentPath, Err: syscall.ENOENT}
	}

	return MountRef(ctx, baseRef, func(root string, _ *mount.Mount) error {
		resolvedParentPath, err := containerdfs.RootPath(root, parentPath)
		if err != nil {
			return err
		}
		_, err = os.Lstat(resolvedParentPath)
		return TrimErrPathPrefix(err, root)
	})
}

func (dir *Directory) WithFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Directory],
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
	doNotCreateDestPath bool,
	attemptUnpackDockerCompatibility bool,
) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent, src); err != nil {
		return err
	}
	ourDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(ourDir)
	srcPath, err := src.Self().File.GetOrEval(ctx, src.Result)
	if err != nil {
		return err
	}
	srcCacheRef, err := src.Self().Snapshot.GetOrEval(ctx, src.Result)
	if err != nil {
		return err
	}
	if srcCacheRef == nil {
		return fmt.Errorf("with file: nil source snapshot")
	}

	dirCacheRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	destPathHintIsDirectory := strings.HasSuffix(destPath, "/") || strings.HasSuffix(destPath, "/.")
	destPath = path.Join(ourDir, destPath)
	if doNotCreateDestPath {
		if err := ensureCopyDestParentExists(ctx, dirCacheRef, destPath); err != nil {
			return err
		}
	}
	newRef, err := query.SnapshotManager().New(ctx, dirCacheRef, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withfile %s %s", destPath, filepath.Base(srcPath))))
	if err != nil {
		return err
	}
	defer newRef.Release(context.WithoutCancel(ctx))

	var usage snapshots.Usage
	if err := MountRef(ctx, newRef, func(root string, destMnt *mount.Mount) (rerr error) {
		var ownership *Ownership
		if owner != "" {
			var err error
			ownership, err = resolveDirectoryOwner(root, owner)
			if err != nil {
				return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
			}
		}

		copier, err := layercopy.NewCopier(layercopy.Mount{Root: root, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()

		opts := layercopy.CopyOptions{
			Chown:             layercopyOwnership(ownership),
			Mode:              layercopyMode(permissions),
			ReplaceExisting:   true,
			DestPathHintIsDir: destPathHintIsDirectory,
		}

		if err := MountRef(ctx, srcCacheRef, func(srcRoot string, srcMnt *mount.Mount) error {
			if attemptUnpackDockerCompatibility {
				resolvedSrcPath, err := containerdfs.RootPath(srcRoot, srcPath)
				if err != nil {
					return err
				}
				if isArchivePath(resolvedSrcPath) {
					realUnpackDestPath, err := copier.MaterializeDestDir(ctx, destPath)
					if err != nil {
						return err
					}
					if err := unpackArchiveFile(resolvedSrcPath, realUnpackDestPath, ownership); err != nil {
						return fmt.Errorf("failed to unpack source archive: %w", err)
					}
					return nil
				}
			}
			return copier.CopyFile(ctx, layercopy.Mount{Root: srcRoot, Mount: srcMnt}, srcPath, destPath, opts)
		}, mountRefAsReadOnly); err != nil {
			return err
		}

		usage, err = copier.Usage()
		return err
	}); err != nil {
		return err
	}

	snap, err := newRef.CommitWithUsage(ctx, usage)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snap)
	return nil
}

func (dir *Directory) WithTimestamps(ctx context.Context, parent dagql.ObjectResult[*Directory], unix int) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withTimestamps %d", unix)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, parentDir)
		if err != nil {
			return err
		}
		return filepath.WalkDir(resolvedDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			modTime := time.Unix(int64(unix), 0)
			return os.Chtimes(path, modTime, modTime)
		})
	})
	if err != nil {
		return err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snapshot)
	return nil
}

func (dir *Directory) WithNewDirectory(ctx context.Context, parent dagql.ObjectResult[*Directory], dest string, permissions fs.FileMode) error {
	dest = path.Clean(dest)
	if strings.HasPrefix(dest, "../") {
		return fmt.Errorf("cannot create directory outside parent: %s", dest)
	}

	if permissions == 0 {
		permissions = 0755
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withNewDirectory %s", dest)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, path.Join(parentDir, dest))
		if err != nil {
			return err
		}
		return TrimErrPathPrefix(os.MkdirAll(resolvedDir, permissions), root)
	})
	if err != nil {
		return err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snapshot)
	return nil
}

func (dir *Directory) Diff(ctx context.Context, parent dagql.ObjectResult[*Directory], other dagql.ObjectResult[*Directory]) error {
	dagqlCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := dagqlCache.Evaluate(ctx, parent, other); err != nil {
		return err
	}
	thisDirRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get directory ref: %w", err)
	}

	thisDirPath, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get directory path: %w", err)
	}
	if thisDirPath == "" {
		thisDirPath = "/"
	}
	otherDirPath, err := other.Self().Dir.GetOrEval(ctx, other.Result)
	if err != nil {
		return fmt.Errorf("failed to get other directory path: %w", err)
	}
	if otherDirPath == "" {
		otherDirPath = "/"
	}
	if thisDirPath != "/" || otherDirPath != "/" {
		return fmt.Errorf("internal error: Directory.diff expects rebased root dirs, got %q and %q", thisDirPath, otherDirPath)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current query: %w", err)
	}
	otherDirRef, err := other.Self().Snapshot.GetOrEval(ctx, other.Result)
	if err != nil {
		return fmt.Errorf("failed to get other directory ref: %w", err)
	}

	var ref bkcache.ImmutableRef
	if thisDirRef == nil {
		if otherDirRef == nil {
			ref = nil
		} else {
			ref, err = query.SnapshotManager().GetBySnapshotID(
				ctx,
				otherDirRef.SnapshotID(),
				bkcache.NoUpdateLastUsed,
			)
		}
	} else {
		ref, err = query.SnapshotManager().ApplySnapshotDiff(
			ctx,
			thisDirRef,
			otherDirRef,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription("directory diff"),
		)
		if err != nil {
			return fmt.Errorf("failed to diff directories: %w", err)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to diff directories: %w", err)
	}
	if ref == nil {
		scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		dir.Dir.setValue(scratchDir)
		dir.Snapshot.setValue(scratchSnapshot)
		return nil
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(ref)
	return nil
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (dir *Directory) WithChanges(ctx context.Context, parent dagql.ObjectResult[*Directory], changes dagql.ObjectResult[*Changeset]) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent, changes); err != nil {
		return err
	}
	ourDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(ourDir)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dagql server: %w", err)
	}
	currentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}

	var diffDir dagql.ObjectResult[*Directory]
	afterID, err := changes.Self().After.ID()
	if err != nil {
		return fmt.Errorf("after ID: %w", err)
	}
	if err := srv.Select(ctx, changes.Self().Before, &diffDir,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*Directory](afterID)},
			},
		},
	); err != nil {
		return fmt.Errorf("compute structural diff: %w", err)
	}
	if err := cache.Evaluate(ctx, diffDir); err != nil {
		return fmt.Errorf("evaluate structural diff: %w", err)
	}

	var diffSnapshot bkcache.ImmutableRef
	if diffDir.Self() != nil {
		diffSnapshot, err = diffDir.Self().Snapshot.GetOrEval(ctx, diffDir.Result)
		if err != nil {
			return fmt.Errorf("diff snapshot: %w", err)
		}
	}
	if ourDir != "" && ourDir != "/" && diffSnapshot != nil {
		diffID, err := diffDir.ID()
		if err != nil {
			return fmt.Errorf("diff ID: %w", err)
		}
		var rebasedDiff dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, srv.Root(), &rebasedDiff,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(ourDir)},
					{Name: "source", Value: dagql.NewID[*Directory](diffID)},
				},
			},
		); err != nil {
			return fmt.Errorf("rebase diff to target path: %w", err)
		}
		if err := cache.Evaluate(ctx, rebasedDiff); err != nil {
			return fmt.Errorf("evaluate rebased diff: %w", err)
		}
		diffSnapshot, err = rebasedDiff.Self().Snapshot.GetOrEval(ctx, rebasedDiff.Result)
		if err != nil {
			return fmt.Errorf("rebased diff snapshot: %w", err)
		}
	}

	currentSnapshot, err = query.SnapshotManager().Merge(
		ctx,
		[]bkcache.ImmutableRef{currentSnapshot, diffSnapshot},
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Directory.withChanges"),
	)
	if err != nil {
		return fmt.Errorf("merge changes into target: %w", err)
	}

	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute paths: %w", err)
	}
	if len(paths.Removed) > 0 {
		currentSnapshot, _, err = dir.withoutPathsFromSnapshot(ctx, currentSnapshot, ourDir, paths.Removed...)
		if err != nil {
			return fmt.Errorf("remove paths: %w", err)
		}
	}

	var addedDirs []string
	for _, p := range paths.Added {
		if strings.HasSuffix(p, "/") {
			addedDirs = append(addedDirs, strings.TrimSuffix(p, "/"))
		}
	}
	if len(addedDirs) > 0 {
		newRef, err := query.SnapshotManager().New(
			ctx,
			currentSnapshot,
			nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("withChanges add dirs %s", strings.Join(addedDirs, ","))),
		)
		if err != nil {
			return err
		}
		err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
			for _, p := range addedDirs {
				resolvedDir, err := containerdfs.RootPath(root, path.Join(ourDir, p))
				if err != nil {
					return err
				}
				if err := os.MkdirAll(resolvedDir, 0o755); err != nil {
					return TrimErrPathPrefix(err, root)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		currentSnapshot, err = newRef.Commit(ctx)
		if err != nil {
			return err
		}
	}

	if currentSnapshot == nil {
		scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		dir.Dir.setValue(scratchDir)
		dir.Snapshot.setValue(scratchSnapshot)
		return nil
	}
	dir.Snapshot.setValue(currentSnapshot)
	return nil
}

func (dir *Directory) Without(ctx context.Context, parent dagql.ObjectResult[*Directory], opCall *dagql.ResultCall, teachNoopEquivalence bool, paths ...string) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	snapshot, anyPathsRemoved, err := dir.withoutPathsFromSnapshot(ctx, parentSnapshot, parentDir, paths...)
	if err != nil {
		return err
	}
	if snapshot == nil {
		scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		dir.Dir.setValue(scratchDir)
		dir.Snapshot.setValue(scratchSnapshot)
	} else {
		dir.Snapshot.setValue(snapshot)
	}

	if teachNoopEquivalence && !anyPathsRemoved && opCall != nil && parent.Self() != nil {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("directory no-op equivalence client metadata: %w", err)
		}
		if clientMetadata.SessionID == "" {
			return fmt.Errorf("directory no-op equivalence: empty session ID")
		}
		if err := cache.TeachCallEquivalentToResult(ctx, clientMetadata.SessionID, opCall, parent); err != nil {
			return fmt.Errorf("teach directory without no-op equivalence: %w", err)
		}
	}

	return nil
}

func (dir *Directory) Exists(ctx context.Context, self dagql.ObjectResult[*Directory], srv *dagql.Server, targetPath string, targetType ExistsType, doNotFollowSymlinks bool) (bool, error) {
	stat, err := dir.Stat(ctx, self, srv, targetPath, doNotFollowSymlinks || targetType == ExistsTypeSymlink)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	switch targetType {
	case ExistsTypeDirectory:
		return stat.FileType == FileTypeDirectory, nil
	case ExistsTypeRegular:
		return stat.FileType == FileTypeRegular, nil
	case ExistsTypeSymlink:
		return stat.FileType == FileTypeSymlink, nil
	case "":
		return true, nil
	default:
		return false, fmt.Errorf("invalid path type %s", targetType)
	}
}

type Stat struct {
	Size        int      `field:"true" doc:"file size"`
	Name        string   `field:"true" doc:"file name"`
	FileType    FileType `field:"true" doc:"file type"`
	Permissions int      `field:"true" doc:"permission bits"`
}

func (*Stat) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Stat",
		NonNull:   false,
	}
}

func (*Stat) TypeDescription() string {
	return "A file or directory status object."
}

func (s *Stat) IsDir() bool {
	return s != nil && s.FileType == FileTypeDirectory
}

func (s Stat) Clone() *Stat {
	cp := s
	return &cp
}

func (dir *Directory) Stat(ctx context.Context, self dagql.ObjectResult[*Directory], srv *dagql.Server, targetPath string, doNotFollowSymlinks bool) (*Stat, error) {
	if targetPath == "" {
		return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
	}

	immutableRef, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if immutableRef == nil {
		return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}

	osStatFunc := os.Stat
	rootPathFunc := containerdfs.RootPath
	if doNotFollowSymlinks {
		// symlink testing requires the Lstat call, which does NOT follow symlinks
		osStatFunc = os.Lstat
		// similarly, containerdfs.RootPath can't be used, since it follows symlinks
		rootPathFunc = RootPathWithoutFinalSymlink
	}

	var fileInfo os.FileInfo
	err = MountRef(ctx, immutableRef, func(root string, _ *mount.Mount) error {
		resolvedPath, err := rootPathFunc(root, path.Join(dirPath, targetPath))
		if err != nil {
			return err
		}
		fileInfo, err = osStatFunc(resolvedPath)
		return TrimErrPathPrefix(err, root)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
		}
		return nil, err
	}

	m := fileInfo.Mode()

	stat := &Stat{
		Size:        int(fileInfo.Size()),
		Name:        fileInfo.Name(),
		Permissions: int(fileInfo.Mode().Perm()),
	}

	if m.IsDir() {
		stat.FileType = FileTypeDirectory
	} else if m.IsRegular() {
		stat.FileType = FileTypeRegular
	} else if m&fs.ModeSymlink != 0 {
		stat.FileType = FileTypeSymlink
	} else {
		stat.FileType = FileTypeUnknown
	}

	return stat, nil
}

func (dir *Directory) Export(ctx context.Context, self dagql.ObjectResult[*Directory], destPath string, merge bool) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}

	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return err
	}
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export directory %s to host %s", dirPath, destPath))
	defer telemetry.EndWithCause(span, &rerr)

	immutableRef, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return fmt.Errorf("failed to evaluate directory: %w", err)
	}
	if immutableRef == nil {
		return errEmptyResultRef
	}

	return MountRef(ctx, immutableRef, func(root string, _ *mount.Mount) error {
		root, err = containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		return bk.LocalDirExport(ctx, root, destPath, merge, nil)
	})
}

func (dir *Directory) Mount(ctx context.Context, self dagql.ObjectResult[*Directory], f func(string) error) error {
	snapshot, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return errEmptyResultRef
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return err
	}

	return MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		src, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		return f(src)
	}, mountRefAsReadOnly)
}

func (dir *Directory) WithSymlink(ctx context.Context, parent dagql.ObjectResult[*Directory], target, linkName string) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("symlink %s -> %s", linkName, target)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		fullLinkName := path.Join(parentDir, linkName)
		linkDir, linkBasename := filepath.Split(fullLinkName)
		resolvedLinkDir, err := containerdfs.RootPath(root, linkDir)
		if err != nil {
			return err
		}
		err = os.MkdirAll(resolvedLinkDir, 0755)
		if err != nil {
			return err
		}
		resolvedLinkName := path.Join(resolvedLinkDir, linkBasename)
		return os.Symlink(target, resolvedLinkName)
	})
	if err != nil {
		return err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snapshot)
	return nil
}

func ParseDirectoryOwner(owner string) (*Ownership, error) {
	uidStr, gidStr, hasGroup := strings.Cut(owner, ":")
	var uid, gid int
	uid, err := parseUID(uidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid uid %q: %w", uidStr, err)
	}
	if hasGroup {
		gid, err = parseUID(gidStr)
		if err != nil {
			return nil, fmt.Errorf("invalid gid %q: %w", gidStr, err)
		}
	}
	if !hasGroup {
		gid = uid
	}

	return &Ownership{
		UID: uid,
		GID: gid,
	}, nil
}

func resolveDirectoryOwner(root, owner string) (*Ownership, error) {
	uidOrName, gidOrName, hasGroup := strings.Cut(owner, ":")

	uid, err := parseUID(uidOrName)
	if err != nil {
		passwdPath, err := containerdfs.RootPath(root, "/etc/passwd")
		if err != nil {
			return nil, err
		}
		passwdFile, err := os.Open(passwdPath)
		if err != nil {
			return nil, TrimErrPathPrefix(err, root)
		}
		defer passwdFile.Close()

		uid, err = findUID(passwdFile, uidOrName)
		if err != nil {
			return nil, fmt.Errorf("find uid: %w", err)
		}
	}

	var gid int
	if hasGroup {
		gid, err = parseUID(gidOrName)
		if err != nil {
			groupPath, err := containerdfs.RootPath(root, "/etc/group")
			if err != nil {
				return nil, err
			}
			groupFile, err := os.Open(groupPath)
			if err != nil {
				return nil, TrimErrPathPrefix(err, root)
			}
			defer groupFile.Close()

			gid, err = findGID(groupFile, gidOrName)
			if err != nil {
				return nil, fmt.Errorf("find gid: %w", err)
			}
		}
	} else {
		gid = uid
	}

	return &Ownership{
		UID: uid,
		GID: gid,
	}, nil
}

func (dir *Directory) Chown(ctx context.Context, parent dagql.ObjectResult[*Directory], chownPath string, owner string) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}
	parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	dir.Dir.setValue(parentDir)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("chown %s %s", chownPath, owner)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		ownership, err := resolveDirectoryOwner(root, owner)
		if err != nil {
			return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
		}

		chownPath := path.Join(parentDir, chownPath)
		chownPath, err = containerdfs.RootPath(root, chownPath)
		if err != nil {
			return err
		}

		err = filepath.WalkDir(chownPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := os.Lchown(path, ownership.UID, ownership.GID); err != nil {
				return fmt.Errorf("failed to set chown %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to walk %s: %w", chownPath, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Snapshot.setValue(snapshot)
	return nil
}

func ValidateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.New("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}

func validateFileName(file string) error {
	return ValidateFileName(file)
}

func SupportsDirSlash(ctx context.Context) bool {
	return Supports(ctx, "v0.17.0")
}

// TODO deprecate ExistsType in favor of FileType

type ExistsType string

var ExistsTypes = dagql.NewEnum[ExistsType]()

var (

	// NOTE calling ExistsTypes.Register("DIRECTORY", ...) will generate:
	// const (
	//     ExistsTypeDirectory ExistsType = "DIRECTORY"
	//     Directory ExistsType = ExistsTypeDirectory
	// )
	// which will conflict with "type Directory struct { ... }",
	// therefore everything will have a _TYPE suffix to avoid naming conflicts

	ExistsTypeRegular = ExistsTypes.Register("REGULAR_TYPE",
		"Tests path is a regular file")
	ExistsTypeDirectory = ExistsTypes.Register("DIRECTORY_TYPE",
		"Tests path is a directory")
	ExistsTypeSymlink = ExistsTypes.Register("SYMLINK_TYPE",
		"Tests path is a symlink")
)

func (et ExistsType) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ExistsType",
		NonNull:   false,
	}
}

func (et ExistsType) TypeDescription() string {
	return "File type."
}

func (et ExistsType) Decoder() dagql.InputDecoder {
	return ExistsTypes
}

func (et ExistsType) ToLiteral() call.Literal {
	return ExistsTypes.Literal(et)
}

func (et ExistsType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(et))
}

func (et *ExistsType) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}
	*et = ExistsType(str)
	return nil
}

type FileType string

var FileTypes = dagql.NewEnum[FileType]()

var (
	FileTypeRegular   = FileTypes.RegisterView("REGULAR", enumView, "regular file type")
	FileTypeDirectory = FileTypes.RegisterView("DIRECTORY", enumView, "directory file type")
	FileTypeSymlink   = FileTypes.RegisterView("SYMLINK", enumView, "symlink file type")
	FileTypeUnknown   = FileTypes.Register("UNKNOWN", "unknown file type")

	_ = FileTypes.AliasView("REGULAR_TYPE", "REGULAR", enumView)
	_ = FileTypes.AliasView("DIRECTORY_TYPE", "DIRECTORY", enumView)
	_ = FileTypes.AliasView("SYMLINK_TYPE", "SYMLINK", enumView)
)

func (ft FileType) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FileType",
		NonNull:   false,
	}
}

func (ft FileType) TypeDescription() string {
	return "File type."
}

func (ft FileType) Decoder() dagql.InputDecoder {
	return FileTypes
}

func (ft FileType) ToLiteral() call.Literal {
	return FileTypes.Literal(ft)
}

func (ft FileType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(ft))
}

func (ft *FileType) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}
	*ft = FileType(str)
	return nil
}

func FileModeToFileType(m fs.FileMode) FileType {
	if m.IsDir() {
		return FileTypeDirectory
	} else if m.IsRegular() {
		return FileTypeRegular
	} else if m&fs.ModeSymlink != 0 {
		return FileTypeSymlink
	} else {
		return FileTypeUnknown
	}
}

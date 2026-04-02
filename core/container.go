package core

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/containerd/platforms"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	dockerfileparser "github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/parser"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerui"
	"github.com/dagger/dagger/util/containerutil"
	"github.com/dagger/dagger/util/llbtodagger"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
)

var ErrMountNotExist = errors.New("mount does not exist")

type DefaultTerminalCmdOpts struct {
	Args []string

	// Provide dagger access to the executed command
	ExperimentalPrivilegedNesting dagql.Optional[dagql.Boolean] `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities dagql.Optional[dagql.Boolean] `default:"false"`
}

// Container is a content-addressed container.
type Container struct {
	// The container's root filesystem.
	FS *ContainerDirectorySource

	// Image configuration (env, workdir, etc)
	Config dockerspec.DockerOCIImageConfig

	// List of GPU devices that will be exposed to the container
	EnabledGPUs []string

	// Mount points configured for the container.
	Mounts ContainerMounts

	// MetaSnapshot is the internal exec metadata snapshot containing stdout,
	// stderr, combined output, and exit code files. It will be nil if nothing
	// has run yet.
	MetaSnapshot bkcache.ImmutableRef

	// The platform of the container's rootfs.
	Platform Platform

	// OCI annotations
	Annotations []containerutil.ContainerAnnotation

	// Secrets to expose to the container.
	Secrets []ContainerSecret

	// Sockets to expose to the container.
	Sockets []ContainerSocket

	// Image reference
	ImageRef string

	// Ports to expose from the container.
	Ports []Port

	// Services to start before running the container.
	Services ServiceBindings

	// The args to invoke when using the terminal api on this container.
	DefaultTerminalCmd DefaultTerminalCmdOpts

	// (Internal-only for now) Environment variables from the engine container, prefixed
	// with a special value, that will be inherited by this container if set.
	SystemEnvNames []string

	// DefaultArgs have been explicitly set by the user
	DefaultArgs bool

	Lazy Lazy[*Container]

	persistedResultID uint64
}

type DirectoryFromContainerLazy struct {
	Container *Container
}

type FileFromContainerLazy struct {
	Container *Container
}

type ContainerWithDirectoryLazy struct {
	LazyState
	Parent                           dagql.ObjectResult[*Container]
	Path                             string
	Source                           dagql.ObjectResult[*Directory]
	Filter                           CopyFilter
	Owner                            string
	Permissions                      *int
	DoNotCreateDestPath              bool
	AttemptUnpackDockerCompatibility bool
	RequiredSourcePath               string
	DestPathHintIsDirectory          bool
	CopySourcePathContentsWhenDir    bool
}

type ContainerWithFileLazy struct {
	LazyState
	Parent                           dagql.ObjectResult[*Container]
	Path                             string
	Source                           dagql.ObjectResult[*File]
	Permissions                      *int
	Owner                            string
	DoNotCreateDestPath              bool
	AttemptUnpackDockerCompatibility bool
}

type ContainerWithoutPathLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Path   string
}

type ContainerWithSymlinkLazy struct {
	LazyState
	Parent   dagql.ObjectResult[*Container]
	Target   string
	LinkPath string
}

type ContainerDirectorySource struct {
	Result *dagql.ObjectResult[*Directory]
	Value  *Directory
}

type ContainerFileSource struct {
	Result *dagql.ObjectResult[*File]
	Value  *File
}

func newContainerDirectoryResultSource(dir dagql.ObjectResult[*Directory]) *ContainerDirectorySource {
	return &ContainerDirectorySource{Result: &dir}
}

func newContainerDirectoryValueSource(dir *Directory) *ContainerDirectorySource {
	return &ContainerDirectorySource{Value: dir}
}

func newContainerFileResultSource(file dagql.ObjectResult[*File]) *ContainerFileSource {
	return &ContainerFileSource{Result: &file}
}

func newContainerFileValueSource(file *File) *ContainerFileSource {
	return &ContainerFileSource{Value: file}
}

func (src *ContainerDirectorySource) self() *Directory {
	if src == nil {
		return nil
	}
	if src.Result != nil {
		return src.Result.Self()
	}
	return src.Value
}

func (src *ContainerFileSource) self() *File {
	if src == nil {
		return nil
	}
	if src.Result != nil {
		return src.Result.Self()
	}
	return src.Value
}

func (src *ContainerDirectorySource) isResultBacked() bool {
	return src != nil && src.Result != nil && src.Result.Self() != nil
}

func (src *ContainerFileSource) isResultBacked() bool {
	return src != nil && src.Result != nil && src.Result.Self() != nil
}

func (*Container) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Container",
		NonNull:   true,
	}
}

func (*Container) TypeDescription() string {
	return "An OCI-compatible container, also known as a Docker container."
}

func (container *Container) PersistedResultID() uint64 {
	if container == nil {
		return 0
	}
	return container.persistedResultID
}

func (container *Container) SetPersistedResultID(resultID uint64) {
	if container != nil {
		container.persistedResultID = resultID
	}
}

func NewContainer(platform Platform) *Container {
	return &Container{
		Platform: platform,
	}
}

func cloneBareDirectoryForContainerChild(src *Directory, source dagql.ObjectResult[*Directory], sourceParentResultID uint64) *Directory {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Services = slices.Clone(cp.Services)
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = true
	cp.snapshotSource = source
	cp.Snapshot = nil
	cp.Lazy = &DirectoryFromSourceLazy{
		LazyState: NewLazyState(),
		Source:    source,
	}
	cp.containerSourceParentResultID = sourceParentResultID
	return &cp
}

func cloneBareFileForContainerChild(src *File, source FileSnapshotSource, sourceParentResultID uint64) *File {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Services = slices.Clone(cp.Services)
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = true
	cp.snapshotSource = source
	cp.Snapshot = nil
	cp.Lazy = &FileFromSourceLazy{
		LazyState: NewLazyState(),
		Source:    source,
	}
	cp.containerSourceParentResultID = sourceParentResultID
	return &cp
}

func cloneDetachedDirectoryForContainerResult(ctx context.Context, src *Directory) (*Directory, error) {
	if src == nil {
		return nil, nil
	}

	src.snapshotMu.RLock()
	ready := src.snapshotReady
	snapshot := src.Snapshot
	source := src.snapshotSource
	lazy := src.Lazy
	src.snapshotMu.RUnlock()

	cp := *src
	cp.Services = slices.Clone(src.Services)
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = ready
	cp.snapshotSource = source
	cp.Snapshot = nil

	switch lazy := lazy.(type) {
	case nil:
		cp.Lazy = nil
	case *DirectoryFromContainerLazy:
		cp.Lazy = &DirectoryFromContainerLazy{Container: lazy.Container}
	case *DirectoryFromSourceLazy:
		cp.Lazy = &DirectoryFromSourceLazy{
			LazyState: NewLazyState(),
			Source:    lazy.Source,
		}
	default:
		return nil, fmt.Errorf("clone detached directory for container result: unsupported lazy %T", lazy)
	}

	if snapshot == nil {
		return &cp, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, err
	}
	cp.Snapshot = reopened
	return &cp, nil
}

func cloneDetachedFileForContainerResult(ctx context.Context, src *File) (*File, error) {
	if src == nil {
		return nil, nil
	}

	src.snapshotMu.RLock()
	ready := src.snapshotReady
	snapshot := src.Snapshot
	source := src.snapshotSource
	lazy := src.Lazy
	src.snapshotMu.RUnlock()

	cp := *src
	cp.Services = slices.Clone(src.Services)
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = ready
	cp.snapshotSource = source
	cp.Snapshot = nil

	switch lazy := lazy.(type) {
	case nil:
		cp.Lazy = nil
	case *FileFromContainerLazy:
		cp.Lazy = &FileFromContainerLazy{Container: lazy.Container}
	case *FileFromSourceLazy:
		cp.Lazy = &FileFromSourceLazy{
			LazyState: NewLazyState(),
			Source:    lazy.Source,
		}
	default:
		return nil, fmt.Errorf("clone detached file for container result: unsupported lazy %T", lazy)
	}

	if snapshot == nil {
		return &cp, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, err
	}
	cp.Snapshot = reopened
	return &cp, nil
}

func NewContainerChild(ctx context.Context, parent dagql.ObjectResult[*Container]) (*Container, error) {
	return newContainerChild(ctx, parent, true)
}

func NewContainerChildWithoutFS(ctx context.Context, parent dagql.ObjectResult[*Container]) (*Container, error) {
	return newContainerChild(ctx, parent, false)
}

func newContainerChild(ctx context.Context, parent dagql.ObjectResult[*Container], cloneFS bool) (*Container, error) {
	if parent.Self() == nil {
		return &Container{}, nil
	}

	cp := *parent.Self()
	cp.Config.ExposedPorts = maps.Clone(cp.Config.ExposedPorts)
	cp.Config.Env = slices.Clone(cp.Config.Env)
	cp.Config.Entrypoint = slices.Clone(cp.Config.Entrypoint)
	cp.Config.Cmd = slices.Clone(cp.Config.Cmd)
	cp.Config.Volumes = maps.Clone(cp.Config.Volumes)
	cp.Config.Labels = maps.Clone(cp.Config.Labels)
	cp.Secrets = slices.Clone(cp.Secrets)
	cp.Sockets = slices.Clone(cp.Sockets)
	cp.Ports = slices.Clone(cp.Ports)
	cp.Services = slices.Clone(cp.Services)
	cp.SystemEnvNames = slices.Clone(cp.SystemEnvNames)
	cp.Lazy = nil
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dagql server: %w", err)
	}
	if cloneFS && cp.FS != nil {
		if cp.FS.isResultBacked() {
			cp.FS = &ContainerDirectorySource{Result: cp.FS.Result}
		} else {
			var source dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, parent, &source, dagql.Selector{Field: "rootfs"}); err != nil {
				return nil, err
			}
			sourceParentResultID, err := persistedContainerParentResultIDFromDirectorySource(source, persistedContainerDirectorySourceOriginRootFS, "")
			if err != nil {
				return nil, err
			}
			cp.FS = newContainerDirectoryValueSource(cloneBareDirectoryForContainerChild(cp.FS.Value, source, sourceParentResultID))
		}
	} else {
		cp.FS = nil
	}
	cp.Mounts = make(ContainerMounts, len(parent.Self().Mounts))
	for i, mnt := range parent.Self().Mounts {
		cp.Mounts[i] = mnt
		if mnt.DirectorySource != nil {
			if mnt.DirectorySource.isResultBacked() {
				cp.Mounts[i].DirectorySource = &ContainerDirectorySource{Result: mnt.DirectorySource.Result}
			} else {
				var source dagql.ObjectResult[*Directory]
				if err := srv.Select(ctx, parent, &source, dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(mnt.Target)},
					},
				}); err != nil {
					return nil, err
				}
				sourceParentResultID, err := persistedContainerParentResultIDFromDirectorySource(source, persistedContainerDirectorySourceOriginDirectoryMount, mnt.Target)
				if err != nil {
					return nil, err
				}
				cp.Mounts[i].DirectorySource = newContainerDirectoryValueSource(cloneBareDirectoryForContainerChild(mnt.DirectorySource.Value, source, sourceParentResultID))
			}
		}
		if mnt.FileSource != nil {
			if mnt.FileSource.isResultBacked() {
				cp.Mounts[i].FileSource = &ContainerFileSource{Result: mnt.FileSource.Result}
			} else {
				var source dagql.ObjectResult[*File]
				if err := srv.Select(ctx, parent, &source, dagql.Selector{
					Field: "file",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(mnt.Target)},
					},
				}); err != nil {
					return nil, err
				}
				sourceParentResultID, err := persistedContainerParentResultIDFromFileSource(FileSnapshotSource{File: source}, persistedContainerFileSourceOriginFileMount, mnt.Target)
				if err != nil {
					return nil, err
				}
				cp.Mounts[i].FileSource = newContainerFileValueSource(cloneBareFileForContainerChild(mnt.FileSource.Value, FileSnapshotSource{File: source}, sourceParentResultID))
			}
		}
	}
	if cloneFS && cp.MetaSnapshot != nil {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, cp.MetaSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
		if err != nil {
			return nil, err
		}
		cp.MetaSnapshot = reopened
	} else {
		cp.MetaSnapshot = nil
	}
	return &cp, nil
}

var _ dagql.OnReleaser = (*Container)(nil)
var _ dagql.HasDependencyResults = (*Container)(nil)
var _ dagql.HasLazyEvaluation = (*Container)(nil)

func (container *Container) LazyEvalFunc() dagql.LazyEvalFunc {
	if container == nil {
		return nil
	}
	topLevelLazy := container.Lazy
	hasPendingChildren := false
	if container.FS != nil && container.FS.Value != nil && container.FS.Value.Lazy != nil {
		hasPendingChildren = true
	}
	if !hasPendingChildren {
		for _, mnt := range container.Mounts {
			switch {
			case mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil && mnt.DirectorySource.Value.Lazy != nil:
				hasPendingChildren = true
			case mnt.FileSource != nil && mnt.FileSource.Value != nil && mnt.FileSource.Value.Lazy != nil:
				hasPendingChildren = true
			}
			if hasPendingChildren {
				break
			}
		}
	}
	if topLevelLazy == nil && !hasPendingChildren {
		return nil
	}
	return func(ctx context.Context) error {
		if topLevelLazy != nil {
			if err := topLevelLazy.Evaluate(ctx, container); err != nil {
				return err
			}
		}
		if container.FS != nil && container.FS.Value != nil && container.FS.Value.Lazy != nil {
			if err := container.FS.Value.Lazy.Evaluate(ctx, container.FS.Value); err != nil {
				return err
			}
		}
		for i := range container.Mounts {
			mnt := &container.Mounts[i]
			if mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil && mnt.DirectorySource.Value.Lazy != nil {
				if err := mnt.DirectorySource.Value.Lazy.Evaluate(ctx, mnt.DirectorySource.Value); err != nil {
					return err
				}
			}
			if mnt.FileSource != nil && mnt.FileSource.Value != nil && mnt.FileSource.Value.Lazy != nil {
				if err := mnt.FileSource.Value.Lazy.Evaluate(ctx, mnt.FileSource.Value); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func (container *Container) Evaluate(ctx context.Context) error {
	if container == nil {
		return nil
	}
	if lazy := container.LazyEvalFunc(); lazy != nil {
		return lazy(ctx)
	}
	return nil
}

func (container *Container) Sync(ctx context.Context) error {
	return container.Evaluate(ctx)
}

func (container *Container) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if container == nil {
		return nil, nil
	}

	lazy := container.Lazy
	owned := make([]dagql.AnyResult, 0, 1+len(container.Mounts))
	if container.FS != nil {
		switch {
		case container.FS.isResultBacked():
			attached, err := attach(*container.FS.Result)
			if err != nil {
				return nil, fmt.Errorf("attach container rootfs: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach container rootfs: unexpected result %T", attached)
			}
			container.FS = newContainerDirectoryResultSource(typed)
			owned = append(owned, typed)
		case container.FS.Value != nil:
			deps, err := container.FS.Value.AttachDependencyResults(ctx, nil, attach)
			if err != nil {
				return nil, fmt.Errorf("attach bare container rootfs: %w", err)
			}
			owned = append(owned, deps...)
		}
	}

	for i := range container.Mounts {
		mnt := &container.Mounts[i]
		switch {
		case mnt.DirectorySource != nil && mnt.DirectorySource.isResultBacked():
			attached, err := attach(*mnt.DirectorySource.Result)
			if err != nil {
				return nil, fmt.Errorf("attach container directory mount %q: %w", mnt.Target, err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach container directory mount %q: unexpected result %T", mnt.Target, attached)
			}
			mnt.DirectorySource = newContainerDirectoryResultSource(typed)
			owned = append(owned, typed)
		case mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil:
			deps, err := mnt.DirectorySource.Value.AttachDependencyResults(ctx, nil, attach)
			if err != nil {
				return nil, fmt.Errorf("attach bare container directory mount %q: %w", mnt.Target, err)
			}
			owned = append(owned, deps...)
		case mnt.FileSource != nil && mnt.FileSource.isResultBacked():
			attached, err := attach(*mnt.FileSource.Result)
			if err != nil {
				return nil, fmt.Errorf("attach container file mount %q: %w", mnt.Target, err)
			}
			typed, ok := attached.(dagql.ObjectResult[*File])
			if !ok {
				return nil, fmt.Errorf("attach container file mount %q: unexpected result %T", mnt.Target, attached)
			}
			mnt.FileSource = newContainerFileResultSource(typed)
			owned = append(owned, typed)
		case mnt.FileSource != nil && mnt.FileSource.Value != nil:
			deps, err := mnt.FileSource.Value.AttachDependencyResults(ctx, nil, attach)
			if err != nil {
				return nil, fmt.Errorf("attach bare container file mount %q: %w", mnt.Target, err)
			}
			owned = append(owned, deps...)
		case mnt.CacheSource != nil && mnt.CacheSource.Volume.Self() != nil:
			attached, err := attach(mnt.CacheSource.Volume)
			if err != nil {
				return nil, fmt.Errorf("attach container cache mount %q: %w", mnt.Target, err)
			}
			typed, ok := attached.(dagql.ObjectResult[*CacheVolume])
			if !ok {
				return nil, fmt.Errorf("attach container cache mount %q: unexpected result %T", mnt.Target, attached)
			}
			mnt.CacheSource.Volume = typed
			owned = append(owned, typed)
		}
	}

	if lazy != nil {
		deps, err := lazy.AttachDependencies(ctx, attach)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}

	return owned, nil
}

func (container *Container) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if container == nil {
		return nil, fmt.Errorf("encode persisted container: nil container")
	}
	// FIXME: remove this restriction immediately after the first cut by adding
	// explicit structural persistence for services, secrets, and sockets.
	if len(container.Services) > 0 {
		return nil, fmt.Errorf("encode persisted container: services are not yet supported")
	}
	if len(container.Secrets) > 0 {
		return nil, fmt.Errorf("encode persisted container: secrets are not yet supported")
	}
	if len(container.Sockets) > 0 {
		return nil, fmt.Errorf("encode persisted container: sockets are not yet supported")
	}

	payload := persistedContainerPayload{
		Form:               persistedContainerFormReady,
		Config:             container.Config,
		EnabledGPUs:        slices.Clone(container.EnabledGPUs),
		Mounts:             make([]persistedContainerMountPayload, 0, len(container.Mounts)),
		Platform:           container.Platform,
		Annotations:        slices.Clone(container.Annotations),
		ImageRef:           container.ImageRef,
		Ports:              slices.Clone(container.Ports),
		DefaultTerminalCmd: container.DefaultTerminalCmd,
		SystemEnvNames:     slices.Clone(container.SystemEnvNames),
		DefaultArgs:        container.DefaultArgs,
	}
	if container.Lazy != nil {
		lazyJSON, err := container.Lazy.EncodePersisted(ctx, cache)
		if err != nil {
			return nil, err
		}
		payload.Form = persistedContainerFormLazy
		payload.LazyJSON = lazyJSON
	}
	if container.FS != nil {
		switch {
		case container.FS.isResultBacked():
			encoded, err := encodePersistedObjectRef(cache, container.FS.Result, "container rootfs")
			if err != nil {
				return nil, err
			}
			payload.FSResultID = encoded
		case container.FS.Value != nil:
			encoded, err := encodePersistedContainerDirectoryValue(
				ctx,
				cache,
				container.FS.Value,
				persistedContainerDirectorySourceOriginRootFS,
				"",
			)
			if err != nil {
				return nil, err
			}
			payload.FSValue = encoded
		}
	}

	for _, mnt := range container.Mounts {
		encoded := persistedContainerMountPayload{
			Target:   mnt.Target,
			Readonly: mnt.Readonly,
		}
		switch {
		case mnt.DirectorySource != nil && mnt.DirectorySource.isResultBacked():
			id, err := encodePersistedObjectRef(cache, mnt.DirectorySource.Result, fmt.Sprintf("directory mount %q", mnt.Target))
			if err != nil {
				return nil, err
			}
			encoded.DirectorySourceResultID = id
		case mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil:
			val, err := encodePersistedContainerDirectoryValue(
				ctx,
				cache,
				mnt.DirectorySource.Value,
				persistedContainerDirectorySourceOriginDirectoryMount,
				mnt.Target,
			)
			if err != nil {
				return nil, err
			}
			encoded.DirectorySourceValue = val
		case mnt.FileSource != nil && mnt.FileSource.isResultBacked():
			id, err := encodePersistedObjectRef(cache, mnt.FileSource.Result, fmt.Sprintf("file mount %q", mnt.Target))
			if err != nil {
				return nil, err
			}
			encoded.FileSourceResultID = id
		case mnt.FileSource != nil && mnt.FileSource.Value != nil:
			val, err := encodePersistedContainerFileValue(
				ctx,
				cache,
				mnt.FileSource.Value,
				persistedContainerFileSourceOriginFileMount,
				mnt.Target,
			)
			if err != nil {
				return nil, err
			}
			encoded.FileSourceValue = val
		case mnt.CacheSource != nil:
			id, err := encodePersistedObjectRef(cache, mnt.CacheSource.Volume, fmt.Sprintf("cache mount %q", mnt.Target))
			if err != nil {
				return nil, err
			}
			encoded.CacheSourceResultID = id
		case mnt.TmpfsSource != nil:
			encoded.TmpfsSize = mnt.TmpfsSource.Size
		default:
			return nil, fmt.Errorf("encode persisted container mount %q: unsupported mount source", mnt.Target)
		}
		payload.Mounts = append(payload.Mounts, encoded)
	}

	enc, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted container payload: %w", err)
	}
	return enc, nil
}

func (*Container) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, call *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedContainerPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted container payload: %w", err)
	}
	if persisted.Form == "" {
		persisted.Form = persistedContainerFormReady
	}

	var rootfs *ContainerDirectorySource
	var decodedRootFS decodedContainerDirectoryValue
	if persisted.FSResultID != 0 {
		rootfsRes, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.FSResultID, "container rootfs")
		if err != nil {
			return nil, err
		}
		rootfs = newContainerDirectoryResultSource(rootfsRes)
	} else if len(persisted.FSValue) > 0 {
		rootfsDir, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, "fs", persisted.FSValue)
		if err != nil {
			return nil, err
		}
		decodedRootFS = rootfsDir
		rootfs = newContainerDirectoryValueSource(rootfsDir.Dir)
	}

	mounts := make(ContainerMounts, 0, len(persisted.Mounts))
	decodedMounts := make([]decodedContainerMount, 0, len(persisted.Mounts))
	for _, persistedMount := range persisted.Mounts {
		mnt := ContainerMount{
			Target:   persistedMount.Target,
			Readonly: persistedMount.Readonly,
		}
		decodedMount := decodedContainerMount{}
		switch {
		case persistedMount.DirectorySourceResultID != 0:
			dirRes, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persistedMount.DirectorySourceResultID, "container mount directory")
			if err != nil {
				return nil, err
			}
			mnt.DirectorySource = newContainerDirectoryResultSource(dirRes)
		case len(persistedMount.DirectorySourceValue) > 0:
			dirVal, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, fmt.Sprintf("mount_dir:%d", len(mounts)), persistedMount.DirectorySourceValue)
			if err != nil {
				return nil, err
			}
			decodedMount.Kind = dirVal.Kind
			mnt.DirectorySource = newContainerDirectoryValueSource(dirVal.Dir)
		case persistedMount.FileSourceResultID != 0:
			fileRes, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persistedMount.FileSourceResultID, "container mount file")
			if err != nil {
				return nil, err
			}
			mnt.FileSource = newContainerFileResultSource(fileRes)
		case len(persistedMount.FileSourceValue) > 0:
			fileVal, err := decodePersistedContainerFileValue(ctx, dag, resultID, fmt.Sprintf("mount_file:%d", len(mounts)), persistedMount.FileSourceValue)
			if err != nil {
				return nil, err
			}
			decodedMount.Kind = fileVal.Kind
			mnt.FileSource = newContainerFileValueSource(fileVal.File)
		case persistedMount.CacheSourceResultID != 0:
			cacheRes, err := loadPersistedObjectResultByResultID[*CacheVolume](ctx, dag, persistedMount.CacheSourceResultID, "container mount cache")
			if err != nil {
				return nil, err
			}
			mnt.CacheSource = &CacheMountSource{Volume: cacheRes}
		case persistedMount.TmpfsSize != 0:
			mnt.TmpfsSource = &TmpfsMountSource{Size: persistedMount.TmpfsSize}
		}
		mounts = append(mounts, mnt)
		decodedMounts = append(decodedMounts, decodedMount)
	}

	var metaSnapshot bkcache.ImmutableRef
	links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "container")
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.Role != "meta" {
			continue
		}
		metaSnapshot, _, err = loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", "meta")
		if err != nil {
			return nil, err
		}
		break
	}

	container := &Container{
		FS:                 rootfs,
		Config:             persisted.Config,
		EnabledGPUs:        slices.Clone(persisted.EnabledGPUs),
		Mounts:             mounts,
		MetaSnapshot:       metaSnapshot,
		Platform:           persisted.Platform,
		Annotations:        slices.Clone(persisted.Annotations),
		ImageRef:           persisted.ImageRef,
		Ports:              slices.Clone(persisted.Ports),
		DefaultTerminalCmd: persisted.DefaultTerminalCmd,
		SystemEnvNames:     slices.Clone(persisted.SystemEnvNames),
		DefaultArgs:        persisted.DefaultArgs,
	}
	if persisted.Form != persistedContainerFormLazy {
		return container, nil
	}
	if call == nil {
		return nil, fmt.Errorf("decode persisted container payload: missing call for lazy form")
	}
	if err := decodePersistedContainerLazy(ctx, dag, call, container, persisted.LazyJSON, decodedRootFS, decodedMounts); err != nil {
		return nil, err
	}
	return container, nil
}

func (container *Container) OnRelease(ctx context.Context) error {
	if container == nil {
		return nil
	}
	var rerr error
	if container.MetaSnapshot != nil {
		rerr = stderrors.Join(rerr, container.MetaSnapshot.Release(ctx))
	}
	if container.FS != nil && container.FS.Value != nil {
		rerr = stderrors.Join(rerr, container.FS.Value.OnRelease(ctx))
	}
	for i := range container.Mounts {
		mnt := &container.Mounts[i]
		if mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil {
			rerr = stderrors.Join(rerr, mnt.DirectorySource.Value.OnRelease(ctx))
		}
		if mnt.FileSource != nil && mnt.FileSource.Value != nil {
			rerr = stderrors.Join(rerr, mnt.FileSource.Value.OnRelease(ctx))
		}
	}
	return rerr
}

// Ownership contains a UID/GID pair resolved from a user/group name or ID pair
// provided via the API. It primarily exists to distinguish an unspecified
// ownership from UID/GID 0 (root) ownership.
type Ownership struct {
	UID int
	GID int
}

// ContainerSecret configures a secret to expose, either as an environment
// variable or mounted to a file path.
type ContainerSecret struct {
	Secret    dagql.ObjectResult[*Secret]
	EnvName   string
	MountPath string
	Owner     *Ownership
	Mode      fs.FileMode
}

// ContainerSocket configures a socket to expose, currently as a Unix socket,
// but potentially as a TCP or UDP address in the future.
type ContainerSocket struct {
	Source        dagql.ObjectResult[*Socket]
	ContainerPath string
	Owner         *Ownership
}

// ContainerMount is a mount point configured in a container.
type ContainerMount struct {
	// The path of the mount within the container.
	Target string

	// Configure the mount as read-only.
	Readonly bool

	// The following fields are mutually exclusive, only one of them should be set.

	// The mounted directory
	DirectorySource *ContainerDirectorySource
	// The mounted file
	FileSource *ContainerFileSource
	// The mounted cache
	CacheSource *CacheMountSource
	// The mounted tmpfs
	TmpfsSource *TmpfsMountSource
}

type CacheMountSource struct {
	// The cache volume backing this mount.
	Volume dagql.ObjectResult[*CacheVolume]
}

type TmpfsMountSource struct {
	// Configure the size of the mounted tmpfs in bytes
	Size int
}

type ContainerMounts []ContainerMount

type persistedContainerMountPayload struct {
	Target                  string          `json:"target"`
	Readonly                bool            `json:"readonly,omitempty"`
	DirectorySourceResultID uint64          `json:"directorySourceResultID,omitempty"`
	DirectorySourceValue    json.RawMessage `json:"directorySourceValue,omitempty"`
	FileSourceResultID      uint64          `json:"fileSourceResultID,omitempty"`
	FileSourceValue         json.RawMessage `json:"fileSourceValue,omitempty"`
	CacheSourceResultID     uint64          `json:"cacheSourceResultID,omitempty"`
	TmpfsSize               int             `json:"tmpfsSize,omitempty"`
}

const (
	persistedContainerValueFormPlain         = "plain"
	persistedContainerValueFormSourceReady   = "sourceReady"
	persistedContainerValueFormSourcePending = "sourcePending"
	persistedContainerValueFormOutputPending = "outputPending"
)

const (
	persistedContainerDirectorySourceOriginRootFS         = "rootfs"
	persistedContainerDirectorySourceOriginDirectoryMount = "directoryMount"
	persistedContainerFileSourceOriginFileMount           = "fileMount"
)

type persistedContainerDirectoryValue struct {
	Form  string          `json:"form"`
	Value json.RawMessage `json:"value"`
}

type persistedContainerFileValue struct {
	Form  string          `json:"form"`
	Value json.RawMessage `json:"value"`
}

type persistedContainerDirectoryOutputValue struct {
	Dir      string          `json:"dir,omitempty"`
	Platform Platform        `json:"platform"`
	Services ServiceBindings `json:"services,omitempty"`
}

type persistedContainerFileOutputValue struct {
	File     string          `json:"file,omitempty"`
	Platform Platform        `json:"platform"`
	Services ServiceBindings `json:"services,omitempty"`
}

type persistedContainerDirectorySourceValue struct {
	Dir                     string          `json:"dir,omitempty"`
	Platform                Platform        `json:"platform"`
	Services                ServiceBindings `json:"services,omitempty"`
	ParentContainerResultID uint64          `json:"parentContainerResultID"`
	OriginKind              string          `json:"originKind"`
	OriginPath              string          `json:"originPath,omitempty"`
}

type persistedContainerFileSourceValue struct {
	File                    string          `json:"file,omitempty"`
	Platform                Platform        `json:"platform"`
	Services                ServiceBindings `json:"services,omitempty"`
	ParentContainerResultID uint64          `json:"parentContainerResultID"`
	OriginKind              string          `json:"originKind"`
	OriginPath              string          `json:"originPath,omitempty"`
}

type decodedContainerDirectoryValue struct {
	Dir  *Directory
	Kind string
}

type decodedContainerFileValue struct {
	File *File
	Kind string
}

type decodedContainerMount struct {
	Kind string
}

type persistedContainerPayload struct {
	Form               string                              `json:"form"`
	FSResultID         uint64                              `json:"fsResultID,omitempty"`
	FSValue            json.RawMessage                     `json:"fsValue,omitempty"`
	Config             dockerspec.DockerOCIImageConfig     `json:"config"`
	EnabledGPUs        []string                            `json:"enabledGPUs,omitempty"`
	Mounts             []persistedContainerMountPayload    `json:"mounts,omitempty"`
	Platform           Platform                            `json:"platform"`
	Annotations        []containerutil.ContainerAnnotation `json:"annotations,omitempty"`
	ImageRef           string                              `json:"imageRef,omitempty"`
	Ports              []Port                              `json:"ports,omitempty"`
	DefaultTerminalCmd DefaultTerminalCmdOpts              `json:"defaultTerminalCmd"`
	SystemEnvNames     []string                            `json:"systemEnvNames,omitempty"`
	DefaultArgs        bool                                `json:"defaultArgs,omitempty"`
	LazyJSON           json.RawMessage                     `json:"lazyJSON,omitempty"`
}

const (
	persistedContainerFormReady = "ready"
	persistedContainerFormLazy  = "lazy"
)

type persistedContainerFromLazy struct {
	CanonicalRef string `json:"canonicalRef"`
}

type persistedContainerWithDirectoryLazy struct {
	ParentResultID                   uint64     `json:"parentResultID"`
	Path                             string     `json:"path"`
	SourceResultID                   uint64     `json:"sourceResultID"`
	Filter                           CopyFilter `json:"filter,omitempty"`
	Owner                            string     `json:"owner,omitempty"`
	Permissions                      *int       `json:"permissions,omitempty"`
	DoNotCreateDestPath              bool       `json:"doNotCreateDestPath,omitempty"`
	AttemptUnpackDockerCompatibility bool       `json:"attemptUnpackDockerCompatibility,omitempty"`
	RequiredSourcePath               string     `json:"requiredSourcePath,omitempty"`
	DestPathHintIsDirectory          bool       `json:"destPathHintIsDirectory,omitempty"`
	CopySourcePathContentsWhenDir    bool       `json:"copySourcePathContentsWhenDir,omitempty"`
}

type persistedContainerWithFileLazy struct {
	ParentResultID                   uint64 `json:"parentResultID"`
	Path                             string `json:"path"`
	SourceResultID                   uint64 `json:"sourceResultID"`
	Permissions                      *int   `json:"permissions,omitempty"`
	Owner                            string `json:"owner,omitempty"`
	DoNotCreateDestPath              bool   `json:"doNotCreateDestPath,omitempty"`
	AttemptUnpackDockerCompatibility bool   `json:"attemptUnpackDockerCompatibility,omitempty"`
}

type persistedContainerWithoutPathLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Path           string `json:"path"`
}

type persistedContainerWithSymlinkLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	LinkPath       string `json:"linkPath"`
}

func (container *Container) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if container == nil {
		return nil
	}
	var links []dagql.PersistedSnapshotRefLink
	if container.MetaSnapshot != nil {
		links = append(links, dagql.PersistedSnapshotRefLink{
			RefKey: container.MetaSnapshot.SnapshotID(),
			Role:   "meta",
		})
	}
	if container.FS != nil && container.FS.Value != nil {
		container.FS.Value.snapshotMu.RLock()
		if container.FS.Value.Snapshot != nil {
			links = append(links, dagql.PersistedSnapshotRefLink{
				RefKey: container.FS.Value.Snapshot.SnapshotID(),
				Role:   "fs",
			})
		}
		container.FS.Value.snapshotMu.RUnlock()
	}
	for i, mnt := range container.Mounts {
		if mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil {
			mnt.DirectorySource.Value.snapshotMu.RLock()
			if mnt.DirectorySource.Value.Snapshot != nil {
				links = append(links, dagql.PersistedSnapshotRefLink{
					RefKey: mnt.DirectorySource.Value.Snapshot.SnapshotID(),
					Role:   fmt.Sprintf("mount_dir:%d", i),
				})
			}
			mnt.DirectorySource.Value.snapshotMu.RUnlock()
		}
		if mnt.FileSource != nil && mnt.FileSource.Value != nil {
			mnt.FileSource.Value.snapshotMu.RLock()
			if mnt.FileSource.Value.Snapshot != nil {
				links = append(links, dagql.PersistedSnapshotRefLink{
					RefKey: mnt.FileSource.Value.Snapshot.SnapshotID(),
					Role:   fmt.Sprintf("mount_file:%d", i),
				})
			}
			mnt.FileSource.Value.snapshotMu.RUnlock()
		}
	}
	return links
}

func encodePersistedContainerDirectoryValue(
	ctx context.Context,
	cache dagql.PersistedObjectCache,
	dir *Directory,
	originKind string,
	originPath string,
) (json.RawMessage, error) {
	if dir == nil {
		return nil, fmt.Errorf("encode persisted container directory value: nil directory")
	}
	form := persistedContainerValueFormPlain
	switch dir.Lazy.(type) {
	case *DirectoryFromContainerLazy:
		value, err := json.Marshal(persistedContainerDirectoryOutputValue{
			Dir:      dir.Dir,
			Platform: dir.Platform,
			Services: slices.Clone(dir.Services),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal persisted container directory output value: %w", err)
		}
		return json.Marshal(persistedContainerDirectoryValue{
			Form:  persistedContainerValueFormOutputPending,
			Value: value,
		})
	}
	dir.snapshotMu.RLock()
	source := dir.snapshotSource
	dir.snapshotMu.RUnlock()
	if source.Self() != nil {
		switch dir.Lazy.(type) {
		case *DirectoryFromSourceLazy:
			form = persistedContainerValueFormSourcePending
		default:
			form = persistedContainerValueFormSourceReady
		}
		parentContainerResultID := dir.containerSourceParentResultID
		var err error
		if parentContainerResultID == 0 {
			parentContainerResultID, err = persistedContainerParentResultIDFromDirectorySource(source, originKind, originPath)
		}
		if err != nil {
			return nil, fmt.Errorf("encode persisted container directory source provenance: %w", err)
		}
		value, err := json.Marshal(persistedContainerDirectorySourceValue{
			Dir:                     dir.Dir,
			Platform:                dir.Platform,
			Services:                slices.Clone(dir.Services),
			ParentContainerResultID: parentContainerResultID,
			OriginKind:              originKind,
			OriginPath:              originPath,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal persisted container directory source value: %w", err)
		}
		return json.Marshal(persistedContainerDirectoryValue{
			Form:  form,
			Value: value,
		})
	}
	value, err := dir.EncodePersistedObject(ctx, cache)
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerDirectoryValue{
		Form:  form,
		Value: value,
	})
}

func encodePersistedContainerFileValue(
	ctx context.Context,
	cache dagql.PersistedObjectCache,
	file *File,
	originKind string,
	originPath string,
) (json.RawMessage, error) {
	if file == nil {
		return nil, fmt.Errorf("encode persisted container file value: nil file")
	}
	form := persistedContainerValueFormPlain
	switch file.Lazy.(type) {
	case *FileFromContainerLazy:
		value, err := json.Marshal(persistedContainerFileOutputValue{
			File:     file.File,
			Platform: file.Platform,
			Services: slices.Clone(file.Services),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal persisted container file output value: %w", err)
		}
		return json.Marshal(persistedContainerFileValue{
			Form:  persistedContainerValueFormOutputPending,
			Value: value,
		})
	}
	file.snapshotMu.RLock()
	source := file.snapshotSource
	file.snapshotMu.RUnlock()
	if source.File.Self() != nil || source.Directory.Self() != nil {
		switch file.Lazy.(type) {
		case *FileFromSourceLazy:
			form = persistedContainerValueFormSourcePending
		default:
			form = persistedContainerValueFormSourceReady
		}
		parentContainerResultID := file.containerSourceParentResultID
		var err error
		if parentContainerResultID == 0 {
			parentContainerResultID, err = persistedContainerParentResultIDFromFileSource(source, originKind, originPath)
		}
		if err != nil {
			return nil, fmt.Errorf("encode persisted container file source provenance: %w", err)
		}
		value, err := json.Marshal(persistedContainerFileSourceValue{
			File:                    file.File,
			Platform:                file.Platform,
			Services:                slices.Clone(file.Services),
			ParentContainerResultID: parentContainerResultID,
			OriginKind:              originKind,
			OriginPath:              originPath,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal persisted container file source value: %w", err)
		}
		return json.Marshal(persistedContainerFileValue{
			Form:  form,
			Value: value,
		})
	}
	value, err := file.EncodePersistedObject(ctx, cache)
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerFileValue{
		Form:  form,
		Value: value,
	})
}

func persistedContainerSourcePathArg(frame *dagql.ResultCall) (string, error) {
	if frame == nil {
		return "", fmt.Errorf("missing source result call")
	}
	for _, arg := range frame.Args {
		if arg == nil || arg.Name != "path" || arg.Value == nil {
			continue
		}
		if arg.Value.Kind != dagql.ResultCallLiteralKindString {
			return "", fmt.Errorf("path arg has kind %q, not string", arg.Value.Kind)
		}
		return arg.Value.StringValue, nil
	}
	return "", fmt.Errorf("missing path arg")
}

func persistedContainerDirectorySourceWithCall(source dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error) {
	cur := source
	for {
		if cur.Self() == nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("directory source is nil")
		}
		if _, err := cur.ResultCall(); err == nil {
			return cur, nil
		}
		cur.Self().snapshotMu.RLock()
		next := cur.Self().snapshotSource
		cur.Self().snapshotMu.RUnlock()
		if next.Self() == nil {
			_, err := cur.ResultCall()
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("directory source result call: %w", err)
		}
		cur = next
	}
}

func persistedContainerFileSourceWithCall(source FileSnapshotSource) (dagql.ObjectResult[*File], error) {
	cur := source
	for {
		if cur.File.Self() == nil {
			return dagql.ObjectResult[*File]{}, fmt.Errorf("file source is not file-backed")
		}
		if _, err := cur.File.ResultCall(); err == nil {
			return cur.File, nil
		}
		cur.File.Self().snapshotMu.RLock()
		next := cur.File.Self().snapshotSource
		cur.File.Self().snapshotMu.RUnlock()
		if next.File.Self() == nil && next.Directory.Self() == nil {
			_, err := cur.File.ResultCall()
			return dagql.ObjectResult[*File]{}, fmt.Errorf("file source result call: %w", err)
		}
		cur = next
	}
}

func persistedContainerSourceParentResultID(
	frame *dagql.ResultCall,
	expectedField string,
	expectedPath string,
) (uint64, error) {
	if frame == nil {
		return 0, fmt.Errorf("missing source result call")
	}
	if frame.Field != expectedField {
		return 0, fmt.Errorf("expected source field %q, got %q", expectedField, frame.Field)
	}
	if frame.Receiver == nil || frame.Receiver.ResultID == 0 {
		return 0, fmt.Errorf("source result missing container receiver result ID")
	}
	if expectedField == "rootfs" {
		return frame.Receiver.ResultID, nil
	}
	gotPath, err := persistedContainerSourcePathArg(frame)
	if err != nil {
		return 0, err
	}
	if gotPath != expectedPath {
		return 0, fmt.Errorf("source path %q does not match expected path %q", gotPath, expectedPath)
	}
	return frame.Receiver.ResultID, nil
}

func persistedContainerParentResultIDFromDirectorySource(
	source dagql.ObjectResult[*Directory],
	originKind string,
	originPath string,
) (uint64, error) {
	sourceWithCall, err := persistedContainerDirectorySourceWithCall(source)
	if err != nil {
		return 0, err
	}
	frame, err := sourceWithCall.ResultCall()
	if err != nil {
		return 0, fmt.Errorf("directory source result call: %w", err)
	}
	switch originKind {
	case persistedContainerDirectorySourceOriginRootFS:
		return persistedContainerSourceParentResultID(frame, "rootfs", "")
	case persistedContainerDirectorySourceOriginDirectoryMount:
		return persistedContainerSourceParentResultID(frame, "directory", originPath)
	default:
		return 0, fmt.Errorf("unsupported directory origin kind %q", originKind)
	}
}

func persistedContainerParentResultIDFromFileSource(
	source FileSnapshotSource,
	originKind string,
	originPath string,
) (uint64, error) {
	switch originKind {
	case persistedContainerFileSourceOriginFileMount:
		sourceWithCall, err := persistedContainerFileSourceWithCall(source)
		if err != nil {
			return 0, err
		}
		frame, err := sourceWithCall.ResultCall()
		if err != nil {
			return 0, fmt.Errorf("file source result call: %w", err)
		}
		return persistedContainerSourceParentResultID(frame, "file", originPath)
	default:
		return 0, fmt.Errorf("unsupported file origin kind %q", originKind)
	}
}

func loadPersistedContainerDirectorySourceByOrigin(
	ctx context.Context,
	dag *dagql.Server,
	parentContainerResultID uint64,
	originKind string,
	originPath string,
	label string,
) (dagql.ObjectResult[*Directory], error) {
	if parentContainerResultID == 0 {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s: missing parent container result ID", label)
	}
	parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, parentContainerResultID, label+" parent")
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}
	var source dagql.ObjectResult[*Directory]
	switch originKind {
	case persistedContainerDirectorySourceOriginRootFS:
		if err := dag.Select(ctx, parent, &source, dagql.Selector{Field: "rootfs"}); err != nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s rootfs select: %w", label, err)
		}
	case persistedContainerDirectorySourceOriginDirectoryMount:
		if err := dag.Select(ctx, parent, &source, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(originPath)},
			},
		}); err != nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s directory select %q: %w", label, originPath, err)
		}
	default:
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("%s: unsupported directory origin kind %q", label, originKind)
	}
	return source, nil
}

func loadPersistedContainerFileSourceByOrigin(
	ctx context.Context,
	dag *dagql.Server,
	parentContainerResultID uint64,
	originKind string,
	originPath string,
	label string,
) (dagql.ObjectResult[*File], error) {
	if parentContainerResultID == 0 {
		return dagql.ObjectResult[*File]{}, fmt.Errorf("%s: missing parent container result ID", label)
	}
	parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, parentContainerResultID, label+" parent")
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	var source dagql.ObjectResult[*File]
	switch originKind {
	case persistedContainerFileSourceOriginFileMount:
		if err := dag.Select(ctx, parent, &source, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(originPath)},
			},
		}); err != nil {
			return dagql.ObjectResult[*File]{}, fmt.Errorf("%s file select %q: %w", label, originPath, err)
		}
	default:
		return dagql.ObjectResult[*File]{}, fmt.Errorf("%s: unsupported file origin kind %q", label, originKind)
	}
	return source, nil
}

func decodePersistedContainerDirectoryValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (decodedContainerDirectoryValue, error) {
	var wrapped persistedContainerDirectoryValue
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory value: %w", err)
	}
	if wrapped.Form == "" {
		wrapped.Form = persistedContainerValueFormPlain
		wrapped.Value = payload
	}

	if wrapped.Form == persistedContainerValueFormOutputPending {
		var shell persistedContainerDirectoryOutputValue
		if err := json.Unmarshal(wrapped.Value, &shell); err != nil {
			return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory output value: %w", err)
		}
		return decodedContainerDirectoryValue{
			Dir: &Directory{
				Dir:      shell.Dir,
				Platform: shell.Platform,
				Services: slices.Clone(shell.Services),
			},
			Kind: wrapped.Form,
		}, nil
	}

	if wrapped.Form == persistedContainerValueFormSourceReady || wrapped.Form == persistedContainerValueFormSourcePending {
		var sourceVal persistedContainerDirectorySourceValue
		if err := json.Unmarshal(wrapped.Value, &sourceVal); err != nil {
			return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory source value: %w", err)
		}
		source, err := loadPersistedContainerDirectorySourceByOrigin(ctx, dag, sourceVal.ParentContainerResultID, sourceVal.OriginKind, sourceVal.OriginPath, "container directory source")
		if err != nil {
			return decodedContainerDirectoryValue{}, err
		}
		dir := &Directory{
			Dir:                           sourceVal.Dir,
			Platform:                      sourceVal.Platform,
			Services:                      slices.Clone(sourceVal.Services),
			containerSourceParentResultID: sourceVal.ParentContainerResultID,
		}
		if err := dir.setSnapshotSource(source); err != nil {
			return decodedContainerDirectoryValue{}, err
		}
		if wrapped.Form == persistedContainerValueFormSourcePending {
			dir.Lazy = &DirectoryFromSourceLazy{
				LazyState: NewLazyState(),
				Source:    source,
			}
		}
		return decodedContainerDirectoryValue{Dir: dir, Kind: wrapped.Form}, nil
	}

	var persisted persistedDirectoryPayload
	if err := json.Unmarshal(wrapped.Value, &persisted); err != nil {
		return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory payload: %w", err)
	}
	dir := &Directory{
		Dir:      persisted.Dir,
		Platform: persisted.Platform,
		Services: slices.Clone(persisted.Services),
	}
	switch persisted.Form {
	case persistedDirectoryFormSnapshot:
		snapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", role)
		if err != nil {
			return decodedContainerDirectoryValue{}, err
		}
		if err := dir.setSnapshot(snapshot); err != nil {
			return decodedContainerDirectoryValue{}, err
		}
	default:
		return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory payload: unsupported form %q", persisted.Form)
	}
	if wrapped.Form == persistedContainerValueFormSourcePending {
		dir.Lazy = &DirectoryFromSourceLazy{
			LazyState: NewLazyState(),
			Source:    dir.snapshotSource,
		}
	}
	return decodedContainerDirectoryValue{Dir: dir, Kind: wrapped.Form}, nil
}

func decodePersistedContainerFileValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (decodedContainerFileValue, error) {
	var wrapped persistedContainerFileValue
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file value: %w", err)
	}
	if wrapped.Form == "" {
		wrapped.Form = persistedContainerValueFormPlain
		wrapped.Value = payload
	}

	if wrapped.Form == persistedContainerValueFormOutputPending {
		var shell persistedContainerFileOutputValue
		if err := json.Unmarshal(wrapped.Value, &shell); err != nil {
			return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file output value: %w", err)
		}
		return decodedContainerFileValue{
			File: &File{
				File:     shell.File,
				Platform: shell.Platform,
				Services: slices.Clone(shell.Services),
			},
			Kind: wrapped.Form,
		}, nil
	}

	if wrapped.Form == persistedContainerValueFormSourceReady || wrapped.Form == persistedContainerValueFormSourcePending {
		var sourceVal persistedContainerFileSourceValue
		if err := json.Unmarshal(wrapped.Value, &sourceVal); err != nil {
			return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file source value: %w", err)
		}
		source, err := loadPersistedContainerFileSourceByOrigin(ctx, dag, sourceVal.ParentContainerResultID, sourceVal.OriginKind, sourceVal.OriginPath, "container file source")
		if err != nil {
			return decodedContainerFileValue{}, err
		}
		file := &File{
			File:                          sourceVal.File,
			Platform:                      sourceVal.Platform,
			Services:                      slices.Clone(sourceVal.Services),
			containerSourceParentResultID: sourceVal.ParentContainerResultID,
		}
		if err := file.setSnapshotSource(FileSnapshotSource{File: source}); err != nil {
			return decodedContainerFileValue{}, err
		}
		if wrapped.Form == persistedContainerValueFormSourcePending {
			file.Lazy = &FileFromSourceLazy{
				LazyState: NewLazyState(),
				Source:    FileSnapshotSource{File: source},
			}
		}
		return decodedContainerFileValue{File: file, Kind: wrapped.Form}, nil
	}

	var persisted persistedFilePayload
	if err := json.Unmarshal(wrapped.Value, &persisted); err != nil {
		return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file payload: %w", err)
	}
	file := &File{
		File:     persisted.File,
		Platform: persisted.Platform,
		Services: slices.Clone(persisted.Services),
	}
	switch persisted.Form {
	case persistedFileFormSnapshot:
		snapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", role)
		if err != nil {
			return decodedContainerFileValue{}, err
		}
		if err := file.setSnapshot(snapshot); err != nil {
			return decodedContainerFileValue{}, err
		}
	default:
		return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file payload: unsupported form %q", persisted.Form)
	}
	if wrapped.Form == persistedContainerValueFormSourcePending {
		file.Lazy = &FileFromSourceLazy{
			LazyState: NewLazyState(),
			Source:    file.snapshotSource,
		}
	}
	return decodedContainerFileValue{File: file, Kind: wrapped.Form}, nil
}

func attachContainerResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*Container], label string) (dagql.ObjectResult[*Container], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*Container]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Container])
	if !ok {
		return dagql.ObjectResult[*Container]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

func bareDirectoryForContainerPath(container *Container, targetPath string) (*Directory, error) {
	mnt, _, err := locatePath(container, targetPath)
	if err != nil {
		return nil, err
	}
	switch {
	case mnt == nil:
		if container.FS == nil || container.FS.Value == nil {
			return nil, fmt.Errorf("missing bare rootfs output for %s", targetPath)
		}
		return container.FS.Value, nil
	case mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil:
		return mnt.DirectorySource.Value, nil
	default:
		return nil, fmt.Errorf("path %s does not resolve to a bare directory output shell", targetPath)
	}
}

func targetParentDirectoryForContainerPath(ctx context.Context, parent dagql.ObjectResult[*Container], current *Container, targetPath string) (dagql.ObjectResult[*Directory], error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("failed to get dagql server: %w", err)
	}
	if current == nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("container lazy current state is nil")
	}
	mnt, _, err := locatePath(current, targetPath)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}
	switch {
	case mnt == nil:
		return containerRootFSSelection(ctx, srv, parent, current.FS)
	case mnt.DirectorySource != nil:
		return containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
	default:
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("path %s does not resolve to a directory target parent", targetPath)
	}
}

func markDirectoryFromContainerLazy(container *Container, targetPath string) error {
	dir, err := bareDirectoryForContainerPath(container, targetPath)
	if err != nil {
		return err
	}
	dir.snapshotMu.Lock()
	if dir.Snapshot != nil {
		dir.snapshotMu.Unlock()
		return fmt.Errorf("path %s resolves to an already-materialized directory output", targetPath)
	}
	dir.snapshotReady = false
	dir.snapshotSource = dagql.ObjectResult[*Directory]{}
	dir.Snapshot = nil
	dir.snapshotMu.Unlock()
	dir.Lazy = &DirectoryFromContainerLazy{Container: container}
	return nil
}

func (lazy *DirectoryFromContainerLazy) Evaluate(ctx context.Context, dir *Directory) error {
	if lazy == nil || lazy.Container == nil || lazy.Container.Lazy == nil {
		return nil
	}
	return lazy.Container.Lazy.Evaluate(ctx, lazy.Container)
}

func (*DirectoryFromContainerLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*DirectoryFromContainerLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("encode persisted directory from-container lazy: unsupported top-level form")
}

func (lazy *FileFromContainerLazy) Evaluate(ctx context.Context, file *File) error {
	if lazy == nil || lazy.Container == nil || lazy.Container.Lazy == nil {
		return nil
	}
	return lazy.Container.Lazy.Evaluate(ctx, lazy.Container)
}

func (*FileFromContainerLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*FileFromContainerLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("encode persisted file from-container lazy: unsupported top-level form")
}

func (lazy *ContainerWithDirectoryLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withDirectory", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent, lazy.Source); err != nil {
			return err
		}
		targetParent, err := targetParentDirectoryForContainerPath(ctx, lazy.Parent, container, lazy.Path)
		if err != nil {
			return err
		}
		dir, err := bareDirectoryForContainerPath(container, lazy.Path)
		if err != nil {
			return err
		}
		if dir.Lazy != nil {
			if wrapper, ok := dir.Lazy.(*DirectoryFromContainerLazy); !ok || wrapper.Container != container {
				if err := dir.Lazy.Evaluate(ctx, dir); err != nil {
					return err
				}
			}
		}
		_, subpath, err := locatePath(container, lazy.Path)
		if err != nil {
			return err
		}
		if err := dir.WithDirectory(ctx, targetParent, subpath, lazy.Source, lazy.Filter, lazy.Owner, lazy.Permissions, lazy.DoNotCreateDestPath, lazy.AttemptUnpackDockerCompatibility, lazy.RequiredSourcePath, lazy.DestPathHintIsDirectory, lazy.CopySourcePathContentsWhenDir); err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithDirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withDirectory parent")
	if err != nil {
		return nil, err
	}
	source, err := attachDirectoryResult(attach, lazy.Source, "attach container withDirectory source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithDirectoryLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withDirectory parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withDirectory source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithDirectoryLazy{
		ParentResultID:                   parentID,
		Path:                             lazy.Path,
		SourceResultID:                   sourceID,
		Filter:                           lazy.Filter,
		Owner:                            lazy.Owner,
		Permissions:                      lazy.Permissions,
		DoNotCreateDestPath:              lazy.DoNotCreateDestPath,
		AttemptUnpackDockerCompatibility: lazy.AttemptUnpackDockerCompatibility,
		RequiredSourcePath:               lazy.RequiredSourcePath,
		DestPathHintIsDirectory:          lazy.DestPathHintIsDirectory,
		CopySourcePathContentsWhenDir:    lazy.CopySourcePathContentsWhenDir,
	})
}

func (lazy *ContainerWithFileLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withFile", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent, lazy.Source); err != nil {
			return err
		}
		targetParent, err := targetParentDirectoryForContainerPath(ctx, lazy.Parent, container, lazy.Path)
		if err != nil {
			return err
		}
		dir, err := bareDirectoryForContainerPath(container, lazy.Path)
		if err != nil {
			return err
		}
		if dir.Lazy != nil {
			if wrapper, ok := dir.Lazy.(*DirectoryFromContainerLazy); !ok || wrapper.Container != container {
				if err := dir.Lazy.Evaluate(ctx, dir); err != nil {
					return err
				}
			}
		}
		_, subpath, err := locatePath(container, lazy.Path)
		if err != nil {
			return err
		}
		if err := dir.WithFile(ctx, targetParent, subpath, lazy.Source, lazy.Permissions, lazy.Owner, lazy.DoNotCreateDestPath, lazy.AttemptUnpackDockerCompatibility); err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withFile parent")
	if err != nil {
		return nil, err
	}
	source, err := attachFileResult(attach, lazy.Source, "attach container withFile source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithFileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withFile parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withFile source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithFileLazy{
		ParentResultID:                   parentID,
		Path:                             lazy.Path,
		SourceResultID:                   sourceID,
		Permissions:                      lazy.Permissions,
		Owner:                            lazy.Owner,
		DoNotCreateDestPath:              lazy.DoNotCreateDestPath,
		AttemptUnpackDockerCompatibility: lazy.AttemptUnpackDockerCompatibility,
	})
}

func (lazy *ContainerWithoutPathLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withoutPath", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}
		targetParent, err := targetParentDirectoryForContainerPath(ctx, lazy.Parent, container, lazy.Path)
		if err != nil {
			return err
		}
		dir, err := bareDirectoryForContainerPath(container, lazy.Path)
		if err != nil {
			return err
		}
		if dir.Lazy != nil {
			if wrapper, ok := dir.Lazy.(*DirectoryFromContainerLazy); !ok || wrapper.Container != container {
				if err := dir.Lazy.Evaluate(ctx, dir); err != nil {
					return err
				}
			}
		}
		_, subpath, err := locatePath(container, lazy.Path)
		if err != nil {
			return err
		}
		if err := dir.Without(ctx, targetParent, dagql.CurrentCall(ctx), subpath); err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithoutPathLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withoutPath parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerWithoutPathLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withoutPath parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithoutPathLazy{
		ParentResultID: parentID,
		Path:           lazy.Path,
	})
}

func (lazy *ContainerWithSymlinkLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withSymlink", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}
		targetParent, err := targetParentDirectoryForContainerPath(ctx, lazy.Parent, container, lazy.LinkPath)
		if err != nil {
			return err
		}
		dir, err := bareDirectoryForContainerPath(container, lazy.LinkPath)
		if err != nil {
			return err
		}
		if dir.Lazy != nil {
			if wrapper, ok := dir.Lazy.(*DirectoryFromContainerLazy); !ok || wrapper.Container != container {
				if err := dir.Lazy.Evaluate(ctx, dir); err != nil {
					return err
				}
			}
		}
		_, subpath, err := locatePath(container, lazy.LinkPath)
		if err != nil {
			return err
		}
		if err := dir.WithSymlink(ctx, targetParent, lazy.Target, subpath); err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithSymlinkLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withSymlink parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerWithSymlinkLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withSymlink parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithSymlinkLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		LinkPath:       lazy.LinkPath,
	})
}

func decodePersistedContainerLazy(
	ctx context.Context,
	dag *dagql.Server,
	call *dagql.ResultCall,
	container *Container,
	payload json.RawMessage,
	decodedRootFS decodedContainerDirectoryValue,
	decodedMounts []decodedContainerMount,
) error {
	switch call.Field {
	case "from":
		var persisted persistedContainerFromLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container from lazy payload: %w", err)
		}
		container.Lazy = &ContainerFromImageRefLazy{
			LazyState:    NewLazyState(),
			CanonicalRef: persisted.CanonicalRef,
			ResolveMode:  serverresolver.ResolveModeDefault,
		}
		if container.FS != nil && container.FS.Value != nil {
			container.FS.Value.Lazy = &DirectoryFromContainerLazy{Container: container}
		}
		return nil
	case "withDirectory":
		var persisted persistedContainerWithDirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withDirectory lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withDirectory parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "container withDirectory source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithDirectoryLazy{
			LazyState:                        NewLazyState(),
			Parent:                           parent,
			Path:                             persisted.Path,
			Source:                           source,
			Filter:                           persisted.Filter,
			Owner:                            persisted.Owner,
			Permissions:                      persisted.Permissions,
			DoNotCreateDestPath:              persisted.DoNotCreateDestPath,
			AttemptUnpackDockerCompatibility: persisted.AttemptUnpackDockerCompatibility,
			RequiredSourcePath:               persisted.RequiredSourcePath,
			DestPathHintIsDirectory:          persisted.DestPathHintIsDirectory,
			CopySourcePathContentsWhenDir:    persisted.CopySourcePathContentsWhenDir,
		}
		return markDirectoryFromContainerLazy(container, persisted.Path)
	case "withFile":
		var persisted persistedContainerWithFileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withFile lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withFile parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.SourceResultID, "container withFile source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithFileLazy{
			LazyState:                        NewLazyState(),
			Parent:                           parent,
			Path:                             persisted.Path,
			Source:                           source,
			Permissions:                      persisted.Permissions,
			Owner:                            persisted.Owner,
			DoNotCreateDestPath:              persisted.DoNotCreateDestPath,
			AttemptUnpackDockerCompatibility: persisted.AttemptUnpackDockerCompatibility,
		}
		return markDirectoryFromContainerLazy(container, persisted.Path)
	case "withoutDirectory", "withoutFile", "withoutFiles":
		var persisted persistedContainerWithoutPathLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withoutPath lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withoutPath parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithoutPathLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Path:      persisted.Path,
		}
		return markDirectoryFromContainerLazy(container, persisted.Path)
	case "withSymlink":
		var persisted persistedContainerWithSymlinkLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withSymlink lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withSymlink parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithSymlinkLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			LinkPath:  persisted.LinkPath,
		}
		return markDirectoryFromContainerLazy(container, persisted.LinkPath)
	case "withExec":
		return decodePersistedContainerExecLazy(ctx, dag, container, payload, decodedRootFS, decodedMounts)
	default:
		return fmt.Errorf("decode persisted container lazy payload: unsupported field %q", call.Field)
	}
}

func (mnts ContainerMounts) With(newMnt ContainerMount) ContainerMounts {
	mntsCp := make(ContainerMounts, 0, len(mnts))

	// NB: this / might need to change on Windows, but I'm not even sure how
	// mounts work on Windows, so...
	parent := newMnt.Target + "/"

	for _, mnt := range mnts {
		if mnt.Target == newMnt.Target || strings.HasPrefix(mnt.Target, parent) {
			continue
		}

		mntsCp = append(mntsCp, mnt)
	}

	mntsCp = append(mntsCp, newMnt)

	return mntsCp
}

func (mnts ContainerMounts) Replace(newMnt ContainerMount) (ContainerMounts, error) {
	mntsCp := make(ContainerMounts, 0, len(mnts))
	found := false
	for _, mnt := range mnts {
		if mnt.Target == newMnt.Target {
			mntsCp = append(mntsCp, newMnt)
			found = true
		} else {
			mntsCp = append(mntsCp, mnt)
		}
	}
	if !found {
		return nil, fmt.Errorf("failed to replace %s: %w", newMnt.Target, ErrMountNotExist)
	}
	return mntsCp, nil
}

func (container *Container) FromRefString(ctx context.Context, addr string) (dagql.ObjectResult[*Container], error) {
	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return dagql.ObjectResult[*Container]{}, fmt.Errorf("failed to parse image address %s: %w", addr, err)
	}

	// add a default :latest if no tag or digest, otherwise this is a no-op
	refName = reference.TagNameOnly(refName)

	var containerArgs []dagql.NamedInput
	if container.Platform.OS != "" {
		containerArgs = append(containerArgs, dagql.NamedInput{Name: "platform", Value: dagql.Opt(container.Platform)})
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Container]{}, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("from %s", addr),
		telemetry.Internal(),
	)
	defer telemetry.EndWithCause(span, nil)

	var ctr dagql.ObjectResult[*Container]
	err = srv.Select(ctx, srv.Root(), &ctr,
		dagql.Selector{
			Field: "container",
			Args:  containerArgs,
		},
		dagql.Selector{
			Field: "from",
			Args: []dagql.NamedInput{
				{Name: "address", Value: dagql.String(refName.String())},
			},
		},
	)
	if err != nil {
		return dagql.ObjectResult[*Container]{}, err
	}

	return ctr, nil
}

// FromCanonicalRef resolves a digest-addressed image pull and updates only the
// rootfs snapshot state.
func (container *Container) FromCanonicalRef(
	ctx context.Context,
	refName reference.Canonical,
) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	platform := container.Platform
	refStr := refName.String()

	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return fmt.Errorf("failed to get registry resolver: %w", err)
	}
	pulled, err := rslvr.Pull(ctx, refStr, serverresolver.PullOpts{
		Platform:    platform.Spec(),
		ResolveMode: serverresolver.ResolveModeDefault,
	})
	if err != nil {
		return fmt.Errorf("pull image %q: %w", refStr, err)
	}
	defer pulled.Release(context.WithoutCancel(ctx))

	ref, err := query.SnapshotManager().ImportImage(ctx, &bkcache.ImportedImage{
		Ref:          pulled.Ref,
		ManifestDesc: pulled.ManifestDesc,
		ConfigDesc:   pulled.ConfigDesc,
		Layers:       pulled.Layers,
		Nonlayers:    pulled.Nonlayers,
	}, bkcache.ImportImageOpts{
		ImageRef:   pulled.Ref,
		RecordType: bkclient.UsageRecordTypeRegular,
	})
	if err != nil {
		return fmt.Errorf("import image %q: %w", refStr, err)
	}

	if container.FS == nil || container.FS.self() == nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return fmt.Errorf("missing rootfs directory for fromCanonicalRef")
	}
	rootfsDir := container.FS.self()
	if rootfsDir.Dir == "" {
		rootfsDir.Dir = "/"
	} else if rootfsDir.Dir != "/" {
		_ = ref.Release(context.WithoutCancel(ctx))
		return fmt.Errorf("SetFSFromCanonicalRef got %s as dir; however it will be lost", rootfsDir.Dir)
	}
	if err := rootfsDir.setSnapshot(ref); err != nil {
		return err
	}

	return nil
}

const defaultDockerfileName = "Dockerfile"

func isKnownDockerfileSyntaxFrontend(syntaxRef string) bool {
	ref := strings.TrimSpace(strings.ToLower(syntaxRef))
	if ref == "" {
		return false
	}

	known := []string{
		"docker/dockerfile",
		"docker/dockerfile-upstream",
		"docker.io/docker/dockerfile",
		"docker.io/docker/dockerfile-upstream",
		"index.docker.io/docker/dockerfile",
		"index.docker.io/docker/dockerfile-upstream",
		"moby/dockerfile",
		"moby/dockerfile-upstream",
		"docker.io/moby/dockerfile",
		"docker.io/moby/dockerfile-upstream",
		"index.docker.io/moby/dockerfile",
		"index.docker.io/moby/dockerfile-upstream",
	}
	for _, prefix := range known {
		if ref == prefix || strings.HasPrefix(ref, prefix+":") || strings.HasPrefix(ref, prefix+"@") {
			return true
		}
	}
	return false
}

func (container *Container) Build(
	ctx context.Context,
	dockerfileDir *Directory,
	// contextDirID is dockerfileDir with files excluded as per dockerignore file,
	// expressed as a recipe-form ID so llbtodagger can append to it.
	contextDirID *call.ID,
	dockerfile string,
	buildArgs []BuildArg,
	target string,
	secrets []dagql.ObjectResult[*Secret],
	noInit bool,
	sshSocket dagql.ObjectResult[*Socket],
) (*Container, error) {
	dockerfilePath := dockerfile
	if dockerfilePath == "" {
		dockerfilePath = defaultDockerfileName
	}
	dockerfileRef, err := dockerfileDir.getSnapshot()
	if err != nil {
		return nil, fmt.Errorf("failed to get Dockerfile directory snapshot: %w", err)
	}
	if dockerfileRef == nil {
		return nil, fmt.Errorf("failed to load Dockerfile %q: directory is empty", dockerfilePath)
	}
	var dockerfileBytes []byte
	err = MountRef(ctx, dockerfileRef, func(root string, _ *mount.Mount) error {
		resolvedDockerfilePath, err := containerdfs.RootPath(root, path.Join(dockerfileDir.Dir, dockerfilePath))
		if err != nil {
			return err
		}
		dockerfileBytes, err = os.ReadFile(resolvedDockerfilePath)
		if err != nil {
			return TrimErrPathPrefix(err, root)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read Dockerfile %q: %w", dockerfilePath, err)
	}
	if syntaxRef, _, _, ok := dockerfileparser.DetectSyntax(dockerfileBytes); ok && !isKnownDockerfileSyntaxFrontend(syntaxRef) {
		return nil, fmt.Errorf("dockerBuild syntax frontend %q is unsupported in hard-cutover path", syntaxRef)
	}
	mainContext := llbtodagger.DockerfileMainContextSentinelState()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get registry resolver: %w", err)
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	buildArgMap := make(map[string]string, len(buildArgs))
	for _, buildArg := range buildArgs {
		buildArgMap[buildArg.Name] = buildArg.Value
	}
	secretIDsByLLBID := make(map[string]*call.ID, len(secrets))
	returnedSecretMounts := make([]ContainerSecret, 0, len(secrets))
	for _, secret := range secrets {
		secretRecipeID, err := secret.RecipeID(ctx)
		if err != nil {
			return nil, fmt.Errorf("get dockerBuild secret recipe ID: %w", err)
		}
		secretName, err := secret.Self().Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("get dockerBuild secret name: %w", err)
		}
		if secretName == "" {
			return nil, fmt.Errorf("secret has no name and cannot be referenced from Dockerfile secret id")
		}
		if existing, found := secretIDsByLLBID[secretName]; found {
			if existing.Digest() != secretRecipeID.Digest() {
				return nil, fmt.Errorf("multiple secrets provided for dockerBuild secret id %q", secretName)
			}
			continue
		}
		secretIDsByLLBID[secretName] = secretRecipeID
		returnedSecretMounts = append(returnedSecretMounts, ContainerSecret{
			Secret:    secret,
			MountPath: fmt.Sprintf("/run/secrets/%s", secretName),
		})
	}
	sshSocketIDsByLLBID := map[string]*call.ID{}
	if sshSocket.Self() != nil {
		sshSocketRecipeID, err := sshSocket.RecipeID(ctx)
		if err != nil {
			return nil, fmt.Errorf("get dockerBuild ssh socket recipe ID: %w", err)
		}
		sshSocketIDsByLLBID[""] = sshSocketRecipeID
	}

	convertOpt := dockerfile2llb.ConvertOpt{
		Config: dockerui.Config{
			BuildArgs: buildArgMap,
			Target:    target,
		},
		MainContext:    &mainContext,
		TargetPlatform: ptr(container.Platform.Spec()),
		MetaResolver:   dockerfileImageMetaResolver{resolver: rslvr},
	}

	st, img, _, _, err := dockerfile2llb.Dockerfile2LLB(ctx, dockerfileBytes, convertOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Dockerfile to LLB: %w", err)
	}
	def, err := st.Marshal(ctx, llb.Platform(container.Platform.Spec()))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Dockerfile LLB: %w", err)
	}
	containerID, err := llbtodagger.DefinitionToIDWithOptions(def.ToPB(), img, llbtodagger.DefinitionToIDOptions{
		MainContextDirectoryID: contextDirID,
		SecretIDsByLLBID:       secretIDsByLLBID,
		SSHSocketIDsByLLBID:    sshSocketIDsByLLBID,
		NoInit:                 noInit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to convert Dockerfile LLB to Dagger ID: %w", err)
	}
	loadedContainerRes, err := dagql.NewID[*Container](containerID).Load(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("failed to load container from converted ID: %w", err)
	}
	loadedContainer := loadedContainerRes.Self()
	if loadedContainer == nil {
		return nil, fmt.Errorf("failed to load container from converted ID: nil container")
	}
	loadedContainerCopy := *loadedContainer
	loadedContainerCopy.Secrets = slices.Clone(loadedContainerCopy.Secrets)
	loadedContainer = &loadedContainerCopy
	loadedContainer.Secrets = append(loadedContainer.Secrets, returnedSecretMounts...)

	if err := loadedContainer.Sync(ctx); err != nil {
		return nil, fmt.Errorf("failed to sync loaded container: %w", err)
	}

	return loadedContainer, nil
}

func (container *Container) RootFS(ctx context.Context) (dagql.ObjectResult[*Directory], error) {
	if container.FS != nil {
		switch {
		case container.FS.isResultBacked():
			return *container.FS.Result, nil
		case container.FS.Value != nil:
			srv, err := CurrentDagqlServer(ctx)
			if err != nil {
				return dagql.ObjectResult[*Directory]{}, fmt.Errorf("failed to get dagql server: %w", err)
			}
			dirVal, err := cloneDetachedDirectoryForContainerResult(ctx, container.FS.Value)
			if err != nil {
				return dagql.ObjectResult[*Directory]{}, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, dirVal)
		default:
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("container rootfs source is nil")
		}
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("failed to get dagql server: %w", err)
	}

	var rootfs dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &rootfs, dagql.Selector{
		Field: "directory",
	}); err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}

	return rootfs, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithRootFS(ctx context.Context, dir dagql.ObjectResult[*Directory]) (*Container, error) {
	container.FS = newContainerDirectoryResultSource(dir)
	container.ImageRef = ""
	return container, nil
}

func (container *Container) setBareRootFS(dir *Directory) *Container {
	container.FS = newContainerDirectoryValueSource(dir)
	container.ImageRef = ""
	return container
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	subdir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
	permissions *int,
	doNotCreateDestPath bool,
	attemptUnpackDockerCompatibility bool,
	requiredSourcePath string,
	destPathHintIsDirectory bool,
	copySourcePathContentsWhenDir bool,
) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, subdir)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", subdir, err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	// if the path being overwritten is an exact mount point for a file, then we need to unmount it
	// and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && mnt.FileSource != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithDirectory(ctx, parent, subdir, src, filter, owner, permissions, doNotCreateDestPath, attemptUnpackDockerCompatibility, requiredSourcePath, destPathHintIsDirectory, copySourcePathContentsWhenDir)
	}

	resolvedOwner := owner
	if owner != "" {
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		resolvedOwner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	srcID, err := src.ID()
	if err != nil {
		return nil, fmt.Errorf("resolve mounted directory ID: %w", err)
	}
	args = append(args, dagql.NamedInput{Name: "source", Value: dagql.NewID[*Directory](srcID)})
	if len(filter.Exclude) > 0 {
		args = append(args, dagql.NamedInput{Name: "exclude", Value: asArrayInput(filter.Exclude, dagql.NewString)})
	}
	if len(filter.Include) > 0 {
		args = append(args, dagql.NamedInput{Name: "include", Value: asArrayInput(filter.Include, dagql.NewString)})
	}
	if filter.Gitignore {
		args = append(args, dagql.NamedInput{Name: "gitignore", Value: dagql.Boolean(true)})
	}
	if resolvedOwner != "" {
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(resolvedOwner)})
	}
	if permissions != nil {
		args = append(args, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(*permissions))})
	}
	if doNotCreateDestPath {
		args = append(args, dagql.NamedInput{Name: "doNotCreateDestPath", Value: dagql.Boolean(true)})
	}
	if attemptUnpackDockerCompatibility {
		args = append(args, dagql.NamedInput{Name: "attemptUnpackDockerCompatibility", Value: dagql.Boolean(true)})
	}
	if requiredSourcePath != "" {
		args = append(args, dagql.NamedInput{Name: "requiredSourcePath", Value: dagql.String(requiredSourcePath)})
	}
	if destPathHintIsDirectory {
		args = append(args, dagql.NamedInput{Name: "destPathHintIsDirectory", Value: dagql.Boolean(true)})
	}
	if copySourcePathContentsWhenDir {
		args = append(args, dagql.NamedInput{Name: "copySourcePathContentsWhenDir", Value: dagql.Boolean(true)})
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		rootfsParent, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return nil, err
		}
		if container.FS != nil && container.FS.Value != nil {
			container.Lazy = &ContainerWithDirectoryLazy{
				LazyState:                        NewLazyState(),
				Parent:                           parent,
				Path:                             subdir,
				Source:                           src,
				Filter:                           filter,
				Owner:                            resolvedOwner,
				Permissions:                      permissions,
				DoNotCreateDestPath:              doNotCreateDestPath,
				AttemptUnpackDockerCompatibility: attemptUnpackDockerCompatibility,
				RequiredSourcePath:               requiredSourcePath,
				DestPathHintIsDirectory:          destPathHintIsDirectory,
				CopySourcePathContentsWhenDir:    copySourcePathContentsWhenDir,
			}
			if err := markDirectoryFromContainerLazy(container, subdir); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newRootfs dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, rootfsParent, &newRootfs, dagql.Selector{Field: "withDirectory", Args: args}); err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			container.Lazy = &ContainerWithDirectoryLazy{
				LazyState:                        NewLazyState(),
				Parent:                           parent,
				Path:                             subdir,
				Source:                           src,
				Filter:                           filter,
				Owner:                            resolvedOwner,
				Permissions:                      permissions,
				DoNotCreateDestPath:              doNotCreateDestPath,
				AttemptUnpackDockerCompatibility: attemptUnpackDockerCompatibility,
				RequiredSourcePath:               requiredSourcePath,
				DestPathHintIsDirectory:          destPathHintIsDirectory,
				CopySourcePathContentsWhenDir:    copySourcePathContentsWhenDir,
			}
			if err := markDirectoryFromContainerLazy(container, subdir); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &newDir, dagql.Selector{
			Field: "withDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", subdir)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", subdir)
	}
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	srv *dagql.Server,
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
	doNotCreateDestPath bool,
	attemptUnpackDockerCompatibility bool,
) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", destPath, err)
	}

	// if the path being overwritten is an exact mount point, then we need to unmount
	// it and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithFile(ctx, parent, srv, destPath, src, permissions, owner, doNotCreateDestPath, attemptUnpackDockerCompatibility)
	}

	resolvedOwner := owner
	if owner != "" {
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		resolvedOwner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	srcID, err := src.ID()
	if err != nil {
		return nil, fmt.Errorf("resolve mounted file ID: %w", err)
	}
	args = append(args, dagql.NamedInput{Name: "source", Value: dagql.NewID[*File](srcID)})
	if permissions != nil {
		args = append(args, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(*permissions))})
	}
	if resolvedOwner != "" {
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(resolvedOwner)})
	}
	if doNotCreateDestPath {
		args = append(args, dagql.NamedInput{Name: "doNotCreateDestPath", Value: dagql.Boolean(true)})
	}
	if attemptUnpackDockerCompatibility {
		args = append(args, dagql.NamedInput{Name: "attemptUnpackDockerCompatibility", Value: dagql.Boolean(true)})
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		rootfsParent, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return nil, err
		}
		if container.FS != nil && container.FS.Value != nil {
			container.Lazy = &ContainerWithFileLazy{
				LazyState:                        NewLazyState(),
				Parent:                           parent,
				Path:                             destPath,
				Source:                           src,
				Permissions:                      permissions,
				Owner:                            resolvedOwner,
				DoNotCreateDestPath:              doNotCreateDestPath,
				AttemptUnpackDockerCompatibility: attemptUnpackDockerCompatibility,
			}
			if err := markDirectoryFromContainerLazy(container, destPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, rootfsParent, &newRootfs, dagql.Selector{
			Field: "withFile",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			container.Lazy = &ContainerWithFileLazy{
				LazyState:                        NewLazyState(),
				Parent:                           parent,
				Path:                             destPath,
				Source:                           src,
				Permissions:                      permissions,
				Owner:                            resolvedOwner,
				DoNotCreateDestPath:              doNotCreateDestPath,
				AttemptUnpackDockerCompatibility: attemptUnpackDockerCompatibility,
			}
			if err := markDirectoryFromContainerLazy(container, destPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &newDir, dagql.Selector{
			Field: "withFile",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", destPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", destPath)
	}
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutPaths(ctx context.Context, parent dagql.ObjectResult[*Container], srv *dagql.Server, destPaths ...string) (*Container, error) {
	for _, destPath := range destPaths {
		var err error
		container, err = container.withoutPath(ctx, parent, srv, destPath)
		if err != nil {
			return nil, fmt.Errorf("failed to remove path %q: %w", destPath, err)
		}
	}
	return container, nil
}

// assumes that container is already cloned by caller
func (container *Container) withoutPath(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	srv *dagql.Server,
	destPath string,
) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", destPath, err)
	}

	// if the path being removed is an exact mount point, then we need to unmount it and then
	// (recursively) remove the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.withoutPath(ctx, parent, srv, destPath)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}

	// Directory.withoutDirectory and Directory.withoutFile are actually the same thing, so choose one arbitrarily
	switch {
	case mnt == nil: // rootfs
		if container.FS == nil {
			// rootfs is an empty dir, nothing to do
			return container, nil
		}
		if container.FS.Value != nil {
			container.Lazy = &ContainerWithoutPathLazy{
				LazyState: NewLazyState(),
				Parent:    parent,
				Path:      destPath,
			}
			if err := markDirectoryFromContainerLazy(container, destPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, *container.FS.Result, &newRootfs, dagql.Selector{
			Field: "withoutDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			container.Lazy = &ContainerWithoutPathLazy{
				LazyState: NewLazyState(),
				Parent:    parent,
				Path:      destPath,
			}
			if err := markDirectoryFromContainerLazy(container, destPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &newDir, dagql.Selector{
			Field: "withoutDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// This should be handled by the check above for whether the path being removed is an exact mount point
		return nil, fmt.Errorf("invalid mount source for %s", destPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", destPath)
	}
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFiles(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	srv *dagql.Server,
	destDir string,
	src []dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
	for _, file := range src {
		destPath := filepath.Join(destDir, filepath.Base(file.Self().File))
		var err error
		container, err = container.WithFile(ctx, parent, srv, destPath, file, permissions, owner, false, false)
		if err != nil {
			return nil, fmt.Errorf("failed to add file %s: %w", destPath, err)
		}
	}

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithNewFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	dest string,
	content []byte,
	permissions fs.FileMode,
	owner string,
) (*Container, error) {
	_, fileName := filepath.Split(filepath.Clean(dest))

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	var newFile dagql.ObjectResult[*File]
	args := []dagql.NamedInput{
		{Name: "name", Value: dagql.String(fileName)},
		{Name: "contents", Value: dagql.String(string(content))},
	}
	if permissions != 0 {
		args = append(args, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(int(permissions)))})
	}
	err = srv.Select(ctx, srv.Root(), &newFile, dagql.Selector{
		Field: "file",
		Args:  args,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new file %s: %w", dest, err)
	}

	return container.WithFile(ctx, parent, srv, dest, newFile, nil, owner, false, false)
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithSymlink(ctx context.Context, parent dagql.ObjectResult[*Container], srv *dagql.Server, target, linkPath string) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, linkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", linkPath, err)
	}

	// if the path being overwritten is an exact mount point, then we need to unmount it and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithSymlink(ctx, parent, srv, target, linkPath)
	}

	args := []dagql.NamedInput{
		{Name: "target", Value: dagql.String(target)},
		{Name: "linkName", Value: dagql.String(mntSubpath)},
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		rootfsParent, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return nil, err
		}
		if container.FS != nil && container.FS.Value != nil {
			container.Lazy = &ContainerWithSymlinkLazy{
				LazyState: NewLazyState(),
				Parent:    parent,
				Target:    target,
				LinkPath:  linkPath,
			}
			if err := markDirectoryFromContainerLazy(container, linkPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, rootfsParent, &newRootfs, dagql.Selector{
			Field: "withSymlink",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			container.Lazy = &ContainerWithSymlinkLazy{
				LazyState: NewLazyState(),
				Parent:    parent,
				Target:    target,
				LinkPath:  linkPath,
			}
			if err := markDirectoryFromContainerLazy(container, linkPath); err != nil {
				return nil, err
			}
			return container, nil
		}
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &newDir, dagql.Selector{
			Field: "withSymlink",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", linkPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", linkPath)
	}
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	target string,
	dir dagql.ObjectResult[*Directory],
	owner string,
	readonly bool,
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var err error
	if owner != "" {
		dir, err = container.chownDir(ctx, parent, dir, owner)
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(ContainerMount{
		DirectorySource: newContainerDirectoryResultSource(dir),
		Target:          target,
		Readonly:        readonly,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	target string,
	file dagql.ObjectResult[*File],
	owner string,
	readonly bool,
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var err error
	if owner != "" {
		file, err = container.chownFile(ctx, parent, file, owner)
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(ContainerMount{
		FileSource: newContainerFileResultSource(file),
		Target:     target,
		Readonly:   readonly,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedCache(
	ctx context.Context,
	target string,
	cache dagql.ObjectResult[*CacheVolume],
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	cacheSelf := cache.Self()
	if cacheSelf == nil {
		return nil, errors.New("cache volume is nil")
	}
	if cacheSelf.getSnapshot() == nil {
		if err := cacheSelf.InitializeSnapshot(ctx); err != nil {
			return nil, fmt.Errorf("initialize cache volume snapshot: %w", err)
		}
	}

	mount := ContainerMount{
		Target: target,
		CacheSource: &CacheMountSource{
			Volume: cache,
		},
	}
	container.Mounts = container.Mounts.With(mount)

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedTemp(ctx context.Context, target string, size int) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	container.Mounts = container.Mounts.With(ContainerMount{
		Target: target,
		TmpfsSource: &TmpfsMountSource{
			Size: size,
		},
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedSecret(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	target string,
	source dagql.ObjectResult[*Secret],
	owner string,
	mode fs.FileMode,
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, parent, owner)
	if err != nil {
		return nil, err
	}

	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:    source,
		MountPath: target,
		Owner:     ownership,
		Mode:      mode,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutMount(ctx context.Context, target string) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var found bool
	var foundIdx int
	for i := len(container.Mounts) - 1; i >= 0; i-- {
		if container.Mounts[i].Target == target {
			found = true
			foundIdx = i
			break
		}
	}

	if found {
		container.Mounts = slices.Delete(container.Mounts, foundIdx, foundIdx+1)
	}

	var secretFound bool
	var secretFoundIdx int
	for i := len(container.Secrets) - 1; i >= 0; i-- {
		if container.Secrets[i].MountPath == target {
			secretFound = true
			secretFoundIdx = i
			break
		}
	}

	if secretFound {
		container.Secrets = slices.Delete(container.Secrets, secretFoundIdx, secretFoundIdx+1)
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) MountTargets(ctx context.Context) ([]string, error) {
	mounts := []string{}
	for _, mnt := range container.Mounts {
		mounts = append(mounts, mnt.Target)
	}

	return mounts, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithUnixSocket(ctx context.Context, target string, source dagql.ObjectResult[*Socket], owner string) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)
	return container.WithUnixSocketFromParent(ctx, dagql.ObjectResult[*Container]{}, target, source, owner)
}

func (container *Container) WithUnixSocketFromParent(ctx context.Context, parent dagql.ObjectResult[*Container], target string, source dagql.ObjectResult[*Socket], owner string) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, parent, owner)
	if err != nil {
		return nil, err
	}

	newSocket := ContainerSocket{
		Source:        source,
		ContainerPath: target,
		Owner:         ownership,
	}

	var replaced bool
	for i, sock := range container.Sockets {
		if sock.ContainerPath == target {
			container.Sockets[i] = newSocket
			replaced = true
			break
		}
	}

	if !replaced {
		container.Sockets = append(container.Sockets, newSocket)
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutUnixSocket(ctx context.Context, target string) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	for i, sock := range container.Sockets {
		if sock.ContainerPath == target {
			container.Sockets = slices.Delete(container.Sockets, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithSecretVariable(
	ctx context.Context,
	name string,
	secret dagql.ObjectResult[*Secret],
) (*Container, error) {
	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:  secret,
		EnvName: name,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutSecretVariable(ctx context.Context, name string) (*Container, error) {
	for i, secret := range container.Secrets {
		if secret.EnvName == name {
			container.Secrets = slices.Delete(container.Secrets, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Directory(ctx context.Context, parent dagql.ObjectResult[*Container], dirPath string) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]

	mnt, subpath, err := locatePath(container, dirPath)
	if err != nil {
		return dir, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dir, fmt.Errorf("failed to get dagql server: %w", err)
	}

	directorySelector := dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(subpath)},
		},
	}

	switch {
	case mnt == nil: // rootfs
		if container.FS != nil && container.FS.Value != nil {
			if subpath == "" || subpath == "." {
				dirVal, err := cloneDetachedDirectoryForContainerResult(ctx, container.FS.Value)
				if err != nil {
					return dir, err
				}
				return dagql.NewObjectResultForCurrentCall(ctx, srv, dirVal)
			}
			rootfs, err := containerRootFSSelection(ctx, srv, parent, container.FS)
			if err != nil {
				return dir, err
			}
			bareDir, err := container.FS.Value.Subdirectory(ctx, rootfs, subpath)
			if err != nil {
				return dir, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, bareDir)
		}
		rootfs, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return dir, err
		}
		if subpath == "" || subpath == "." {
			return rootfs, nil
		}
		err = srv.Select(ctx, rootfs, &dir, directorySelector)
	case mnt.DirectorySource != nil: // mounted directory
		if mnt.DirectorySource.Value != nil {
			if subpath == "" || subpath == "." {
				dirVal, err := cloneDetachedDirectoryForContainerResult(ctx, mnt.DirectorySource.Value)
				if err != nil {
					return dir, err
				}
				return dagql.NewObjectResultForCurrentCall(ctx, srv, dirVal)
			}
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return dir, err
			}
			bareDir, err := mnt.DirectorySource.Value.Subdirectory(ctx, parentDir, subpath)
			if err != nil {
				return dir, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, bareDir)
		}
		if subpath == "" || subpath == "." {
			return *mnt.DirectorySource.Result, nil
		}
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &dir, directorySelector)
	case mnt.FileSource != nil: // mounted file
		return dir, fmt.Errorf("path %s is a file, not a directory", dirPath)
	default:
		return dir, fmt.Errorf("invalid path %s in container mounts", dirPath)
	}

	switch {
	case err == nil:
		return dir, nil
	case errors.As(err, &notADirectoryError{}):
		// fix the error message to use dirPath rather than subpath
		return dir, notADirectoryError{fmt.Errorf("path %s is a file, not a directory", dirPath)}
	default:
		return dir, err
	}
}

func (container *Container) File(ctx context.Context, parent dagql.ObjectResult[*Container], filePath string) (dagql.ObjectResult[*File], error) {
	var f dagql.ObjectResult[*File]

	mnt, subpath, err := locatePath(container, filePath)
	if err != nil {
		return f, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return f, fmt.Errorf("failed to get dagql server: %w", err)
	}

	fileSelector := dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(subpath)},
		},
	}

	switch {
	case mnt == nil: // rootfs
		rootfs, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return f, err
		}
		if container.FS != nil && container.FS.Value != nil {
			bareFile, err := container.FS.Value.Subfile(ctx, rootfs, subpath)
			if err != nil {
				return f, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, bareFile)
		}
		err = srv.Select(ctx, rootfs, &f, fileSelector)
	case mnt.DirectorySource != nil: // mounted directory
		if mnt.DirectorySource.Value != nil {
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return f, err
			}
			bareFile, err := mnt.DirectorySource.Value.Subfile(ctx, parentDir, subpath)
			if err != nil {
				return f, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, bareFile)
		}
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &f, fileSelector)
		err = RestoreErrPath(err, filePath) // preserve the full filePath, rather than subpath
	case mnt.FileSource != nil: // mounted file
		if mnt.FileSource.Value != nil {
			fileVal, err := cloneDetachedFileForContainerResult(ctx, mnt.FileSource.Value)
			if err != nil {
				return f, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, fileVal)
		}
		return *mnt.FileSource.Result, nil
	default:
		return f, fmt.Errorf("invalid path %s in container mounts", filePath)
	}

	switch {
	case err == nil:
		return f, nil
	case errors.As(err, &notAFileError{}):
		// fix the error message to use filePath rather than subpath
		return f, notAFileError{fmt.Errorf("path %s is a directory, not a file", filePath)}
	default:
		return f, err
	}
}

// locatePath finds the mount that contains the given container path. It returns
// the mount and the subpath of containerPath relative to the mountpoint.
func locatePath(
	container *Container,
	containerPath string,
) (*ContainerMount, string, error) {
	containerPath = absPath(container.Config.WorkingDir, containerPath)

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(container.Mounts) - 1; i >= 0; i-- {
		mnt := container.Mounts[i]

		if containerPath == mnt.Target || strings.HasPrefix(containerPath, mnt.Target+"/") {
			if mnt.TmpfsSource != nil {
				return nil, "", fmt.Errorf("%s: cannot retrieve path from tmpfs", containerPath)
			}

			if mnt.CacheSource != nil {
				return nil, "", fmt.Errorf("%s: cannot retrieve path from cache", containerPath)
			}

			relPath, err := filepath.Rel(mnt.Target, containerPath)
			if err != nil {
				return nil, "", err
			}
			return &mnt, relPath, nil
		}
	}

	// Not found in a mount
	return nil, containerPath, nil
}

func containerRootFSSelection(ctx context.Context, srv *dagql.Server, parent dagql.ObjectResult[*Container], src *ContainerDirectorySource) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	switch {
	case src != nil && src.isResultBacked():
		return *src.Result, nil
	case src != nil && src.Value != nil:
		src.Value.snapshotMu.RLock()
		source := src.Value.snapshotSource
		src.Value.snapshotMu.RUnlock()
		if source.Self() != nil {
			return source, nil
		}
		if err := srv.Select(ctx, parent, &dir, dagql.Selector{Field: "rootfs"}); err != nil {
			return dir, err
		}
		return dir, nil
	default:
		if err := srv.Select(ctx, srv.Root(), &dir, dagql.Selector{Field: "directory"}); err != nil {
			return dir, err
		}
		return dir, nil
	}
}

func containerDirectoryMountSelection(ctx context.Context, srv *dagql.Server, parent dagql.ObjectResult[*Container], src *ContainerDirectorySource, target string) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	switch {
	case src != nil && src.isResultBacked():
		return *src.Result, nil
	case src != nil && src.Value != nil:
		src.Value.snapshotMu.RLock()
		source := src.Value.snapshotSource
		src.Value.snapshotMu.RUnlock()
		if source.Self() != nil {
			return source, nil
		}
		err := srv.Select(ctx, parent, &dir, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(target)},
			},
		})
		return dir, err
	default:
		return dir, fmt.Errorf("missing directory mount source for %s", target)
	}
}

func containerFileMountSelection(ctx context.Context, srv *dagql.Server, parent dagql.ObjectResult[*Container], src *ContainerFileSource, target string) (dagql.ObjectResult[*File], error) {
	var file dagql.ObjectResult[*File]
	switch {
	case src != nil && src.isResultBacked():
		return *src.Result, nil
	case src != nil && src.Value != nil:
		src.Value.snapshotMu.RLock()
		source := src.Value.snapshotSource
		src.Value.snapshotMu.RUnlock()
		if source.File.Self() != nil {
			return source.File, nil
		}
		err := srv.Select(ctx, parent, &file, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(target)},
			},
		})
		return file, err
	default:
		return file, fmt.Errorf("missing file mount source for %s", target)
	}
}

func (container *Container) replaceMount(
	target string,
	dir dagql.ObjectResult[*Directory],
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var err error
	container.Mounts, err = container.Mounts.Replace(ContainerMount{
		DirectorySource: newContainerDirectoryResultSource(dir),
		Target:          target,
	})
	if err != nil {
		return nil, err
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) chownDir(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	src dagql.ObjectResult[*Directory],
	owner string,
) (res dagql.ObjectResult[*Directory], err error) {
	ownership, err := container.ownership(ctx, parent, owner)
	if err != nil {
		return res, err
	}

	if ownership == nil {
		return src, nil
	}

	// Directory.chown only knows uid/gid ints, provide those
	owner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get dagql server: %w", err)
	}

	err = srv.Select(ctx, src, &res, dagql.Selector{
		Field: "chown",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(".")},
			{Name: "owner", Value: dagql.String(owner)},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to chown directory: %w", err)
	}

	return res, nil
}

func (container *Container) chownFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	src dagql.ObjectResult[*File],
	owner string,
) (res dagql.ObjectResult[*File], err error) {
	ownership, err := container.ownership(ctx, parent, owner)
	if err != nil {
		return res, err
	}

	if ownership == nil {
		return src, nil
	}

	// File.chown only knows uid/gid ints, provide those
	owner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get dagql server: %w", err)
	}

	err = srv.Select(ctx, src, &res, dagql.Selector{
		Field: "chown",
		Args: []dagql.NamedInput{
			{Name: "owner", Value: dagql.String(owner)},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to chown file: %w", err)
	}

	return res, nil
}

func (container *Container) ImageConfig(ctx context.Context) (dockerspec.DockerOCIImageConfig, error) {
	return container.Config, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig) (*Container, error) {
	container.Config = updateFn(container.Config)
	container.ImageRef = ""
	return container, nil
}

type ContainerGPUOpts struct {
	Devices []string
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithGPU(ctx context.Context, gpuOpts ContainerGPUOpts) (*Container, error) {
	container.EnabledGPUs = gpuOpts.Devices
	return container, nil
}

func (container *Container) Exists(ctx context.Context, parent dagql.ObjectResult[*Container], srv *dagql.Server, targetPath string, targetType ExistsType, doNotFollowSymlinks bool) (bool, error) {
	mnt, mntSubpath, err := locatePath(container, targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to locate path %s: %w", targetPath, err)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	if targetType != "" {
		args = append(args, dagql.NamedInput{Name: "expectedType", Value: dagql.Opt[ExistsType](targetType)})
	}
	if doNotFollowSymlinks {
		args = append(args, dagql.NamedInput{Name: "doNotFollowSymlinks", Value: dagql.Opt[dagql.Boolean](dagql.Boolean(doNotFollowSymlinks))})
	}

	var exists bool
	switch {
	case mnt == nil: // rootfs
		rootfs, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return false, err
		}
		if container.FS != nil && container.FS.Value != nil {
			return container.FS.Value.Exists(ctx, srv, mntSubpath, targetType, doNotFollowSymlinks)
		}
		err = srv.Select(ctx, rootfs, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			return mnt.DirectorySource.Value.Exists(ctx, srv, mntSubpath, targetType, doNotFollowSymlinks)
		}
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.FileSource != nil: // file mount
		if mnt.FileSource.Value != nil {
			if targetType == "" {
				return true, nil
			}
			return targetType == ExistsTypeRegular, nil
		}
		if targetType == "" {
			return true, nil
		}
		return targetType == ExistsTypeRegular, nil

	default:
		return false, fmt.Errorf("invalid mount source for %s", targetPath)
	}

	return exists, nil
}

func (container *Container) Stat(ctx context.Context, parent dagql.ObjectResult[*Container], srv *dagql.Server, targetPath string, doNotFollowSymlinks bool) (*Stat, error) {
	mnt, mntSubpath, err := locatePath(container, targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", targetPath, err)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	if doNotFollowSymlinks {
		args = append(args, dagql.NamedInput{Name: "doNotFollowSymlinks", Value: dagql.Opt[dagql.Boolean](dagql.Boolean(doNotFollowSymlinks))})
	}

	var stat *Stat
	switch {
	case mnt == nil: // rootfs
		rootfs, err := containerRootFSSelection(ctx, srv, parent, container.FS)
		if err != nil {
			return nil, err
		}
		if container.FS != nil && container.FS.Value != nil {
			return container.FS.Value.Stat(ctx, srv, mntSubpath, doNotFollowSymlinks)
		}
		err = srv.Select(ctx, rootfs, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			return mnt.DirectorySource.Value.Stat(ctx, srv, mntSubpath, doNotFollowSymlinks)
		}
		err = srv.Select(ctx, *mnt.DirectorySource.Result, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.FileSource != nil: // file mount
		if mnt.FileSource.Value != nil {
			return mnt.FileSource.Value.Stat(ctx)
		}
		err = srv.Select(ctx, *mnt.FileSource.Result, &stat, dagql.Selector{
			Field: "stat",
		})
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("invalid mount source for %s", targetPath)
	}

	return stat, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithAnnotation(ctx context.Context, key, value string) (*Container, error) {
	container.Annotations = append(container.Annotations, containerutil.ContainerAnnotation{
		Key:   key,
		Value: value,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutAnnotation(ctx context.Context, name string) (*Container, error) {
	for i, annotation := range container.Annotations {
		if annotation.Key == name {
			container.Annotations = slices.Delete(container.Annotations, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Publish(
	ctx context.Context,
	ref string,
	platformVariants []*Container,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) (string, error) {
	variants := filterEmptyContainers(append([]*Container{container}, platformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return "", err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}

	resp, err := bk.PublishContainerImage(ctx, inputByPlatform, ref, useOCIMediaTypes(mediaTypes), string(forcedCompression))
	if err != nil {
		return "", err
	}

	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", err
	}

	withDig, err := reference.WithDigest(refName, resp.RootDesc.Digest)
	if err != nil {
		return "", fmt.Errorf("with digest: %w", err)
	}
	return withDig.String(), nil
}

type ExportOpts struct {
	Dest              string
	PlatformVariants  []*Container
	ForcedCompression ImageLayerCompression
	MediaTypes        ImageMediaTypes
	Tar               bool
	LeaseID           string
}

func useOCIMediaTypes(mediaTypes ImageMediaTypes) bool {
	if mediaTypes == "" {
		// Modern registry implementations support oci types and docker daemons
		// have been capable of pulling them since 2018:
		// https://github.com/moby/moby/pull/37359
		// So they are a safe default.
		mediaTypes = OCIMediaTypes
	}
	return mediaTypes == OCIMediaTypes
}

func filterEmptyContainers(containers []*Container) []*Container {
	var l []*Container
	for _, c := range containers {
		if c.FS == nil {
			continue
		}
		rootFS := c.FS.self()
		if rootFS == nil {
			continue
		}
		l = append(l, c)
	}
	return l
}

func getVariantRefs(ctx context.Context, variants []*Container) (map[string]buildkit.ContainerExport, error) {
	inputByPlatform := map[string]buildkit.ContainerExport{}
	var eg errgroup.Group
	var mu sync.Mutex
	for _, variant := range variants {
		if variant.FS == nil {
			continue
		}
		rootFS := variant.FS.self()
		if rootFS == nil {
			continue
		}

		platformString := variant.Platform.Format()
		if _, ok := inputByPlatform[platformString]; ok {
			return nil, fmt.Errorf("duplicate platform %q", platformString)
		}

		eg.Go(func() error {
			fsRef, err := rootFS.getSnapshot()
			if err != nil {
				if rootFS.Lazy != nil {
					if evalErr := rootFS.Lazy.Evaluate(ctx, rootFS); evalErr != nil {
						return fmt.Errorf("evaluate variant rootfs for platform %s: %w", platformString, evalErr)
					}
					fsRef, err = rootFS.getSnapshot()
				}
				if err != nil {
					return fmt.Errorf("get variant rootfs snapshot for platform %s: %w", platformString, err)
				}
			}

			mu.Lock()
			defer mu.Unlock()

			inputByPlatform[platformString] = buildkit.ContainerExport{
				Ref:         fsRef,
				Config:      variant.Config,
				Annotations: variant.Annotations,
			}
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}
	if len(inputByPlatform) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return nil, errors.New("no containers to export")
	}
	return inputByPlatform, nil
}

func (container *Container) Export(ctx context.Context, opts ExportOpts) (*specs.Descriptor, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	variants := filterEmptyContainers(append([]*Container{container}, opts.PlatformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return nil, err
	}

	resp, err := bk.ExportContainerImage(ctx, opts.Dest, inputByPlatform, string(opts.ForcedCompression), opts.Tar, opts.LeaseID, useOCIMediaTypes(opts.MediaTypes))
	if err != nil {
		return nil, err
	}
	return &resp.RootDesc, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExposedPort(port Port) (*Container, error) {
	// replace existing port to avoid duplicates
	gotOne := false

	for i, p := range container.Ports {
		if p.Port == port.Port && p.Protocol == port.Protocol {
			container.Ports[i] = port
			gotOne = true
			break
		}
	}

	if !gotOne {
		container.Ports = append(container.Ports, port)
	}

	if container.Config.ExposedPorts == nil {
		container.Config.ExposedPorts = map[string]struct{}{}
	}

	ociPort := fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network())
	container.Config.ExposedPorts[ociPort] = struct{}{}

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutExposedPort(port int, protocol NetworkProtocol) (*Container, error) {
	filtered := []Port{}
	filteredOCI := map[string]struct{}{}
	for _, p := range container.Ports {
		if p.Port != port || p.Protocol != protocol {
			filtered = append(filtered, p)
			ociPort := fmt.Sprintf("%d/%s", p.Port, p.Protocol.Network())
			filteredOCI[ociPort] = struct{}{}
		}
	}

	container.Ports = filtered
	container.Config.ExposedPorts = filteredOCI

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithServiceBinding(ctx context.Context, svc dagql.ObjectResult[*Service], alias string) (*Container, error) {
	svcDig, err := svc.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, err
	}
	host, err := svc.Self().Hostname(ctx, svcDig)
	if err != nil {
		return nil, err
	}

	var aliases AliasSet
	if alias != "" {
		aliases = AliasSet{alias}
	}

	container.Services.Merge(ServiceBindings{
		{
			Service:  svc,
			Hostname: host,
			Aliases:  aliases,
		},
	})

	return container, nil
}

func (container *Container) ImageRefOrErr(ctx context.Context) (string, error) {
	imgRef := container.ImageRef
	if imgRef != "" {
		return imgRef, nil
	}

	return "", errors.Errorf("Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
}

type ContainerAsServiceArgs struct {
	// Command to run instead of the container's default command
	Args []string `default:"[]"`

	// If the container has an entrypoint, prepend it to this exec's args
	UseEntrypoint bool `default:"false"`

	// Provide the executed command access back to the Dagger API
	ExperimentalPrivilegedNesting bool `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities bool `default:"false"`

	// Expand the environment variables in args
	Expand bool `default:"false"`

	// Skip the init process injected into containers by default so that the
	// user's process is PID 1
	NoInit bool `default:"false"`
}

func (container *Container) AsService(ctx context.Context, containerRes dagql.ObjectResult[*Container], args ContainerAsServiceArgs) (*Service, error) {
	if containerRes.Self() == nil {
		return nil, fmt.Errorf("container result is nil")
	}
	if len(args.Args) == 0 &&
		len(container.Config.Cmd) == 0 &&
		len(container.Config.Entrypoint) == 0 {
		return nil, ErrNoSvcCommand
	}

	useEntrypoint := args.UseEntrypoint
	if len(container.Config.Entrypoint) > 0 && !container.DefaultArgs {
		useEntrypoint = true
	}

	var cmdargs = container.Config.Cmd
	if len(args.Args) > 0 {
		cmdargs = args.Args
		if !args.UseEntrypoint {
			useEntrypoint = false
		}
	}

	if len(container.Config.Entrypoint) > 0 && useEntrypoint {
		cmdargs = append(container.Config.Entrypoint, cmdargs...)
	}

	return &Service{
		Creator:                       trace.SpanContextFromContext(ctx),
		Container:                     containerRes,
		Args:                          cmdargs,
		ExperimentalPrivilegedNesting: args.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      args.InsecureRootCapabilities,
		NoInit:                        args.NoInit,
	}, nil
}

func (container *Container) openFile(ctx context.Context, parent dagql.ObjectResult[*Container], path string) (io.ReadCloser, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	path = absPath(container.Config.WorkingDir, path)

	var fileSource dagql.ObjectResult[*File]
	if err := srv.Select(ctx, parent, &fileSource, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
		},
	}); err != nil {
		return nil, err
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, fileSource); err != nil {
		return nil, err
	}
	return fileSource.Self().Open(ctx)
}

func (container *Container) ownership(ctx context.Context, parent dagql.ObjectResult[*Container], owner string) (*Ownership, error) {
	if owner == "" {
		// do not change ownership
		return nil, nil
	}

	uidOrName, gidOrName, hasGroup := strings.Cut(owner, ":")

	var uid, gid int
	var uname, gname string

	uid, err := parseUID(uidOrName)
	if err != nil {
		uname = uidOrName
	}

	if hasGroup {
		gid, err = parseUID(gidOrName)
		if err != nil {
			gname = gidOrName
		}
	}

	if uname != "" {
		f, err := container.openFile(ctx, parent, "/etc/passwd")
		if err != nil {
			return nil, fmt.Errorf("open /etc/passwd: %w", err)
		}
		defer f.Close()
		uid, err = findUID(f, uname)
		if err != nil {
			return nil, fmt.Errorf("find uid: %w", err)
		}
	}

	if gname != "" {
		f, err := container.openFile(ctx, parent, "/etc/group")
		if err != nil {
			return nil, fmt.Errorf("open /etc/passwd: %w", err)
		}
		defer f.Close()
		gid, err = findGID(f, gname)
		if err != nil {
			return nil, fmt.Errorf("find gid: %w", err)
		}
	}

	if !hasGroup {
		gid = uid
	}

	return &Ownership{uid, gid}, nil
}

func (container *Container) ResolveOwnership(ctx context.Context, parent dagql.ObjectResult[*Container], owner string) (string, error) {
	ownership, err := container.ownership(ctx, parent, owner)
	if err != nil {
		return "", err
	}
	if ownership == nil {
		return "", nil
	}
	return strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID), nil
}

func (container *Container) command(opts ContainerExecOpts) ([]string, error) {
	cfg := container.Config
	args := opts.Args

	if len(args) == 0 {
		// we use the default args if no new default args are passed
		args = cfg.Cmd
	}

	if len(cfg.Entrypoint) > 0 && opts.UseEntrypoint {
		args = append(cfg.Entrypoint, args...)
	}

	if len(args) == 0 {
		return nil, ErrNoCommand
	}

	return args, nil
}

type BuildArg struct {
	Name  string `field:"true" doc:"The build argument name."`
	Value string `field:"true" doc:"The build argument value."`
}

func (BuildArg) TypeName() string {
	return "BuildArg"
}

func (BuildArg) TypeDescription() string {
	return "Key value object that represents a build argument."
}

// OCI manifest annotation that specifies an image's tag
const ociTagAnnotation = "org.opencontainers.image.ref.name"

func ResolveIndex(ctx context.Context, store content.Store, desc specs.Descriptor, platform specs.Platform, tag string) (*specs.Descriptor, error) {
	return resolveIndex(ctx, store, desc, platform, tag)
}

func resolveIndex(ctx context.Context, store content.Store, desc specs.Descriptor, platform specs.Platform, tag string) (*specs.Descriptor, error) {
	if desc.MediaType != specs.MediaTypeImageIndex {
		return nil, fmt.Errorf("expected index, got %s", desc.MediaType)
	}

	indexBlob, err := content.ReadBlob(ctx, store, desc)
	if err != nil {
		return nil, fmt.Errorf("read index blob: %w", err)
	}

	var idx specs.Index
	err = json.Unmarshal(indexBlob, &idx)
	if err != nil {
		return nil, fmt.Errorf("unmarshal index: %w", err)
	}

	matcher := platforms.Only(platform)

	for _, m := range idx.Manifests {
		if m.Platform != nil {
			if !matcher.Match(*m.Platform) {
				// incompatible
				continue
			}
		}

		if tag != "" {
			if m.Annotations == nil {
				continue
			}

			manifestTag, found := m.Annotations[ociTagAnnotation]
			if !found || manifestTag != tag {
				continue
			}
		}

		switch m.MediaType {
		case specs.MediaTypeImageManifest, // OCI
			images.MediaTypeDockerSchema2Manifest: // Docker
			return &m, nil

		case specs.MediaTypeImageIndex, // OCI
			images.MediaTypeDockerSchema2ManifestList: // Docker
			return resolveIndex(ctx, store, m, platform, tag)

		default:
			return nil, fmt.Errorf("expected manifest or index, got %s", m.MediaType)
		}
	}

	return nil, fmt.Errorf("no manifest for platform %s and tag %s", platforms.Format(platform), tag)
}

type ImageLayerCompression string

var ImageLayerCompressions = dagql.NewEnum[ImageLayerCompression]()

var (
	CompressionGzip         = ImageLayerCompressions.Register("Gzip")
	_                       = ImageLayerCompressions.AliasView("GZIP", "Gzip", enumView)
	CompressionZstd         = ImageLayerCompressions.Register("Zstd")
	_                       = ImageLayerCompressions.AliasView("ZSTD", "Zstd", enumView)
	CompressionEStarGZ      = ImageLayerCompressions.Register("EStarGZ")
	_                       = ImageLayerCompressions.AliasView("ESTARGZ", "EStarGZ", enumView)
	CompressionUncompressed = ImageLayerCompressions.Register("Uncompressed")
	_                       = ImageLayerCompressions.AliasView("UNCOMPRESSED", "Uncompressed", enumView)
)

func (proto ImageLayerCompression) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ImageLayerCompression",
		NonNull:   true,
	}
}

func (proto ImageLayerCompression) TypeDescription() string {
	return "Compression algorithm to use for image layers."
}

func (proto ImageLayerCompression) Decoder() dagql.InputDecoder {
	return ImageLayerCompressions
}

func (proto ImageLayerCompression) ToLiteral() call.Literal {
	return ImageLayerCompressions.Literal(proto)
}

type ImageMediaTypes string

var ImageMediaTypesEnum = dagql.NewEnum[ImageMediaTypes]()

var (
	OCIMediaTypes    = ImageMediaTypesEnum.Register("OCIMediaTypes")
	_                = ImageMediaTypesEnum.AliasView("OCI", "OCIMediaTypes", enumView)
	DockerMediaTypes = ImageMediaTypesEnum.Register("DockerMediaTypes")
	_                = ImageMediaTypesEnum.AliasView("DOCKER", "DockerMediaTypes", enumView)
)

func (proto ImageMediaTypes) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ImageMediaTypes",
		NonNull:   true,
	}
}

func (proto ImageMediaTypes) TypeDescription() string {
	return "Mediatypes to use in published or exported image metadata."
}

func (proto ImageMediaTypes) Decoder() dagql.InputDecoder {
	return ImageMediaTypesEnum
}

func (proto ImageMediaTypes) ToLiteral() call.Literal {
	return ImageMediaTypesEnum.Literal(proto)
}

type ReturnTypes string

var ReturnTypesEnum = dagql.NewEnum[ReturnTypes]()

var (
	ReturnSuccess = ReturnTypesEnum.Register("SUCCESS",
		`A successful execution (exit code 0)`,
	)
	ReturnFailure = ReturnTypesEnum.Register("FAILURE",
		`A failed execution (exit codes 1-127 and 192-255)`,
	)
	ReturnAny = ReturnTypesEnum.Register("ANY",
		`Any execution (exit codes 0-127 and 192-255)`,
	)
)

func (expect ReturnTypes) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ReturnType",
		NonNull:   true,
	}
}

func (expect ReturnTypes) TypeDescription() string {
	return "Expected return type of an execution"
}

func (expect ReturnTypes) Decoder() dagql.InputDecoder {
	return ReturnTypesEnum
}

func (expect ReturnTypes) ToLiteral() call.Literal {
	return ReturnTypesEnum.Literal(expect)
}

// ReturnCodes gets the valid exit codes allowed for a specific return status
//
// NOTE: exit status codes 128-191 are likely from exiting via a signal - we
// shouldn't try and handle these. Codes 192-255 are safe to handle to support
// tools that return exit codes >127, such as AWS CLI.
func (expect ReturnTypes) ReturnCodes() []int {
	switch expect {
	case ReturnSuccess:
		return []int{0}
	case ReturnFailure:
		codes := make([]int, 0, 127+64)
		for i := 1; i <= 127; i++ {
			codes = append(codes, i)
		}
		for i := 192; i <= 255; i++ {
			codes = append(codes, i)
		}
		return codes
	case ReturnAny:
		codes := make([]int, 0, 128+64)
		for i := 0; i <= 127; i++ {
			codes = append(codes, i)
		}
		for i := 192; i <= 255; i++ {
			codes = append(codes, i)
		}
		return codes
	default:
		return nil
	}
}

type TerminalLegacy struct{}

func (*TerminalLegacy) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Terminal",
		NonNull:   true,
	}
}

func (*TerminalLegacy) TypeDescription() string {
	return "An interactive terminal that clients can connect to."
}

func (*TerminalLegacy) Evaluate(ctx context.Context) error {
	return nil
}

func (terminal *TerminalLegacy) Sync(ctx context.Context) error {
	return terminal.Evaluate(ctx)
}

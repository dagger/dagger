package core

import (
	"context"
	"encoding/base64"
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
	"github.com/containerd/containerd/v2/core/transfer/archive"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/containersource"
	"github.com/dagger/dagger/engine/buildkit/exporter/containerimage/exptypes"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	dockerfileparser "github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/parser"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerui"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/util/containerutil"
	"github.com/dagger/dagger/util/llbtodagger"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
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

	LazyState

	persistedResultID uint64
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
		Platform:  platform,
		LazyState: NewLazyState(),
	}
}

func cloneBareDirectoryForContainerChild(src *Directory, source dagql.ObjectResult[*Directory]) *Directory {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Services = slices.Clone(cp.Services)
	cp.LazyState = NewLazyState()
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = false
	cp.snapshotSource = source
	cp.Snapshot = nil
	return &cp
}

func cloneBareFileForContainerChild(src *File, source FileSnapshotSource) *File {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Services = slices.Clone(cp.Services)
	cp.LazyState = NewLazyState()
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = false
	cp.snapshotSource = source
	cp.Snapshot = nil
	return &cp
}

func NewContainerChild(ctx context.Context, parent dagql.ObjectResult[*Container]) (*Container, error) {
	return newContainerChild(ctx, parent, true)
}

func NewContainerChildWithoutFS(ctx context.Context, parent dagql.ObjectResult[*Container]) (*Container, error) {
	return newContainerChild(ctx, parent, false)
}

func newContainerChild(ctx context.Context, parent dagql.ObjectResult[*Container], cloneFS bool) (*Container, error) {
	if parent.Self() == nil {
		return &Container{
			LazyState: NewLazyState(),
		}, nil
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
	cp.LazyState = NewLazyState()
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
			cp.FS = newContainerDirectoryValueSource(cloneBareDirectoryForContainerChild(cp.FS.Value, source))
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
				cp.Mounts[i].DirectorySource = newContainerDirectoryValueSource(cloneBareDirectoryForContainerChild(mnt.DirectorySource.Value, source))
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
				cp.Mounts[i].FileSource = newContainerFileValueSource(cloneBareFileForContainerChild(mnt.FileSource.Value, FileSnapshotSource{File: source}))
			}
		}
	}
	if cloneFS && cp.MetaSnapshot != nil {
		cp.MetaSnapshot = cp.MetaSnapshot.Clone()
	} else {
		cp.MetaSnapshot = nil
	}
	cp.configureBareSourceLazyInit(parent)
	return &cp, nil
}

func (container *Container) configureBareSourceLazyInit(parent dagql.ObjectResult[*Container]) {
	if container == nil || container.LazyEvalFunc() != nil {
		return
	}
	type pendingDirSource struct {
		target string
		dir    *Directory
	}
	type pendingFileSource struct {
		target string
		file   *File
	}
	var pendingRootFS *Directory
	var pendingDirs []pendingDirSource
	var pendingFiles []pendingFileSource
	if container.FS != nil && container.FS.Value != nil && !container.FS.Value.snapshotReady {
		pendingRootFS = container.FS.Value
	}
	for _, mnt := range container.Mounts {
		if mnt.DirectorySource != nil && mnt.DirectorySource.Value != nil && !mnt.DirectorySource.Value.snapshotReady {
			pendingDirs = append(pendingDirs, pendingDirSource{
				target: mnt.Target,
				dir:    mnt.DirectorySource.Value,
			})
		}
		if mnt.FileSource != nil && mnt.FileSource.Value != nil && !mnt.FileSource.Value.snapshotReady {
			pendingFiles = append(pendingFiles, pendingFileSource{
				target: mnt.Target,
				file:   mnt.FileSource.Value,
			})
		}
	}
	if pendingRootFS == nil && len(pendingDirs) == 0 && len(pendingFiles) == 0 {
		return
	}
	if parent.Self() == nil {
		return
	}
	container.LazyInit = func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		var evalResults []dagql.AnyResult
		if pendingRootFS != nil {
			pendingRootFS.snapshotMu.RLock()
			source := pendingRootFS.snapshotSource
			pendingRootFS.snapshotMu.RUnlock()
			if source.Self() == nil {
				return fmt.Errorf("missing preserved rootfs snapshot source")
			}
			evalResults = append(evalResults, source)
		}
		for _, pending := range pendingDirs {
			if pending.dir != nil && !pending.dir.snapshotReady {
				pending.dir.snapshotMu.RLock()
				source := pending.dir.snapshotSource
				pending.dir.snapshotMu.RUnlock()
				if source.Self() == nil {
					return fmt.Errorf("missing preserved directory mount snapshot source for %s", pending.target)
				}
				evalResults = append(evalResults, source)
			}
		}
		for _, pending := range pendingFiles {
			if pending.file != nil && !pending.file.snapshotReady {
				pending.file.snapshotMu.RLock()
				source := pending.file.snapshotSource
				pending.file.snapshotMu.RUnlock()
				if source.File.Self() == nil {
					return fmt.Errorf("missing preserved file mount snapshot source for %s", pending.target)
				}
				evalResults = append(evalResults, source.File)
			}
		}
		if len(evalResults) > 0 {
			if err := cache.Evaluate(ctx, evalResults...); err != nil {
				return err
			}
		}
		if pendingRootFS != nil && !pendingRootFS.snapshotReady {
			pendingRootFS.snapshotMu.Lock()
			pendingRootFS.snapshotReady = true
			pendingRootFS.LazyInit = nil
			pendingRootFS.snapshotMu.Unlock()
		}
		for _, pending := range pendingDirs {
			if pending.dir != nil && !pending.dir.snapshotReady {
				pending.dir.snapshotMu.Lock()
				pending.dir.snapshotReady = true
				pending.dir.LazyInit = nil
				pending.dir.snapshotMu.Unlock()
			}
		}
		for _, pending := range pendingFiles {
			if pending.file != nil && !pending.file.snapshotReady {
				pending.file.snapshotMu.Lock()
				pending.file.snapshotReady = true
				pending.file.LazyInit = nil
				pending.file.snapshotMu.Unlock()
			}
		}
		container.LazyInit = nil
		return nil
	}
}

var _ dagql.OnReleaser = (*Container)(nil)
var _ dagql.HasDependencyResults = (*Container)(nil)

func (container *Container) Evaluate(ctx context.Context) error {
	if container == nil {
		return nil
	}
	return container.LazyState.Evaluate(ctx, "Container")
}

func (container *Container) Sync(ctx context.Context) error {
	if err := container.Evaluate(ctx); err != nil {
		return err
	}
	if container == nil {
		return nil
	}
	if container.FS != nil && container.FS.self() != nil {
		if err := container.FS.self().LazyState.Evaluate(ctx, "Directory"); err != nil {
			return err
		}
	}
	return nil
}

func (container *Container) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if container == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 1+len(container.Mounts))
	if container.FS != nil && container.FS.isResultBacked() {
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

	return owned, nil
}

func (container *Container) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if container == nil {
		return nil, fmt.Errorf("encode persisted container: nil container")
	}
	if len(container.Services) > 0 {
		return nil, fmt.Errorf("encode persisted container: services are not yet supported")
	}
	if len(container.Secrets) > 0 {
		return nil, fmt.Errorf("encode persisted container: secrets are not yet supported")
	}

	payload := persistedContainerPayload{
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
	if container.FS != nil {
		switch {
		case container.FS.isResultBacked():
			encoded, err := encodePersistedObjectRef(cache, container.FS.Result, "container rootfs")
			if err != nil {
				return nil, err
			}
			payload.FSResultID = encoded
		case container.FS.Value != nil:
			encoded, err := container.FS.Value.EncodePersistedObject(ctx, cache)
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
			val, err := mnt.DirectorySource.Value.EncodePersistedObject(ctx, cache)
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
			val, err := mnt.FileSource.Value.EncodePersistedObject(ctx, cache)
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

func (*Container) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedContainerPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted container payload: %w", err)
	}

	var rootfs *ContainerDirectorySource
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
		rootfs = newContainerDirectoryValueSource(rootfsDir)
	}

	mounts := make(ContainerMounts, 0, len(persisted.Mounts))
	for _, persistedMount := range persisted.Mounts {
		mnt := ContainerMount{
			Target:   persistedMount.Target,
			Readonly: persistedMount.Readonly,
		}
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
			mnt.DirectorySource = newContainerDirectoryValueSource(dirVal)
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
			mnt.FileSource = newContainerFileValueSource(fileVal)
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

	return &Container{
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
		LazyState:          NewLazyState(),
	}, nil
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
	Source        *Socket
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

type persistedContainerPayload struct {
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

func decodePersistedContainerDirectoryValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (*Directory, error) {
	var persisted persistedDirectoryPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted container directory payload: %w", err)
	}
	dir := &Directory{
		Dir:       persisted.Dir,
		Platform:  persisted.Platform,
		LazyState: NewLazyState(),
	}
	switch persisted.Form {
	case persistedDirectoryFormSnapshot:
		snapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", role)
		if err != nil {
			return nil, err
		}
		if err := dir.setSnapshot(snapshot); err != nil {
			return nil, err
		}
	case persistedDirectoryFormSource:
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "container directory snapshot source")
		if err != nil {
			return nil, err
		}
		if err := dir.setSnapshotSource(source); err != nil {
			return nil, err
		}
		if source.Self() != nil {
			dir.Services = slices.Clone(source.Self().Services)
		}
	default:
		return nil, fmt.Errorf("decode persisted container directory payload: unsupported form %q", persisted.Form)
	}
	return dir, nil
}

func decodePersistedContainerFileValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (*File, error) {
	var persisted persistedFilePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted container file payload: %w", err)
	}
	file := &File{
		File:      persisted.File,
		Platform:  persisted.Platform,
		LazyState: NewLazyState(),
	}
	switch persisted.Form {
	case persistedFileFormSnapshot:
		snapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", role)
		if err != nil {
			return nil, err
		}
		if err := file.setSnapshot(snapshot); err != nil {
			return nil, err
		}
	case persistedFileFormSource:
		switch {
		case persisted.DirectorySourceResultID != 0 && persisted.FileSourceResultID != 0:
			return nil, fmt.Errorf("decode persisted container file payload: both source result IDs set")
		case persisted.DirectorySourceResultID != 0:
			source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.DirectorySourceResultID, "container file directory source")
			if err != nil {
				return nil, err
			}
			if err := file.setSnapshotSource(FileSnapshotSource{Directory: source}); err != nil {
				return nil, err
			}
			if source.Self() != nil {
				file.Services = slices.Clone(source.Self().Services)
			}
		case persisted.FileSourceResultID != 0:
			source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.FileSourceResultID, "container file source")
			if err != nil {
				return nil, err
			}
			if err := file.setSnapshotSource(FileSnapshotSource{File: source}); err != nil {
				return nil, err
			}
			if source.Self() != nil {
				file.Services = slices.Clone(source.Self().Services)
			}
		default:
			return nil, fmt.Errorf("decode persisted container file payload: missing source result ID")
		}
	default:
		return nil, fmt.Errorf("decode persisted container file payload: unsupported form %q", persisted.Form)
	}
	return file, nil
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

func (container *Container) FromRefString(ctx context.Context, addr string) (*Container, error) {
	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image address %s: %w", addr, err)
	}

	// add a default :latest if no tag or digest, otherwise this is a no-op
	refName = reference.TagNameOnly(refName)

	var containerArgs []dagql.NamedInput
	if container.Platform.OS != "" {
		containerArgs = append(containerArgs, dagql.NamedInput{Name: "platform", Value: dagql.Opt(container.Platform)})
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dagger server: %w", err)
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
		return nil, err
	}

	return ctr.Self(), nil
}

// FromCanonicalRef returns a lazy initializer for digest-addressed image pulls.
// It updates only rootfs snapshot state.
func (container *Container) FromCanonicalRef(
	ctx context.Context,
	refName reference.Canonical,
) (LazyInitFunc, error) {
	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}

		platform := container.Platform
		refStr := refName.String()

		bk, err := query.Buildkit(ctx)
		if err != nil {
			return fmt.Errorf("failed to get buildkit client: %w", err)
		}

		hsm, err := containersource.NewSource(containersource.SourceOpt{
			Snapshotter:   bk.Worker.Snapshotter,
			ContentStore:  bk.Worker.ContentStore(),
			ImageStore:    bk.Worker.ImageStore,
			CacheAccessor: query.BuildkitCache(),
			RegistryHosts: bk.Worker.RegistryHosts,
			ResolverType:  containersource.ResolverTypeRegistry,
			LeaseManager:  bk.Worker.LeaseManager(),
		})
		if err != nil {
			return err
		}

		attrs := map[string]string{}
		id, err := hsm.Identifier(refStr, attrs, &pb.Platform{
			Architecture: platform.Architecture,
			OS:           platform.OS,
			Variant:      platform.Variant,
			OSVersion:    platform.OSVersion,
			OSFeatures:   platform.OSFeatures,
		})
		if err != nil {
			return err
		}

		src, err := hsm.Resolve(ctx, id, query.BuildkitSession())
		if err != nil {
			return err
		}

		bkSessionGroup := NewSessionGroup(bk.ID())
		ref, err := src.Snapshot(ctx, bkSessionGroup)
		if err != nil {
			return err
		}

		if container.FS == nil || container.FS.self() == nil {
			return fmt.Errorf("missing rootfs directory for fromCanonicalRef")
		}
		rootfsDir := container.FS.self()
		if rootfsDir.Dir == "" {
			rootfsDir.Dir = "/"
		} else if rootfsDir.Dir != "/" {
			return fmt.Errorf("SetFSFromCanonicalRef got %s as dir; however it will be lost", rootfsDir.Dir)
		}
		if err := rootfsDir.setSnapshot(ref); err != nil {
			return err
		}

		return nil
	}, nil
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
	secretStore *SecretStore,
	noInit bool,
	sshSocketID *call.ID,
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
	err = MountRef(ctx, dockerfileRef, nil, func(root string, _ *mount.Mount) error {
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
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
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
		secretDgst := SecretDigest(ctx, secret)
		if secretDgst == "" {
			return nil, fmt.Errorf("get dockerBuild secret digest: missing secret digest")
		}
		secretName, ok := secretStore.GetSecretName(secretDgst)
		if !ok {
			return nil, fmt.Errorf("secret not found: %s", secretDgst)
		}
		if secretName == "" {
			return nil, fmt.Errorf("secret %s has no name and cannot be referenced from Dockerfile secret id", secretDgst)
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
	if sshSocketID != nil {
		sshSocketRecipeID := sshSocketID
		if sshSocketRecipeID.IsHandle() {
			socketRes, err := dagql.NewID[*Socket](sshSocketID).Load(ctx, srv)
			if err != nil {
				return nil, fmt.Errorf("get dockerBuild ssh socket: %w", err)
			}
			sshSocketRecipeID, err = socketRes.RecipeID(ctx)
			if err != nil {
				return nil, fmt.Errorf("get dockerBuild ssh socket recipe ID: %w", err)
			}
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
		MetaResolver:   bk,
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

func (container *Container) RootFS(ctx context.Context) (*Directory, error) {
	if container.FS != nil {
		return container.FS.self(), nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dagql server: %w", err)
	}

	var rootfs dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &rootfs, dagql.Selector{
		Field: "directory",
	}); err != nil {
		return nil, err
	}

	return rootfs.Self(), nil
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
	if owner != "" {
		// directories only handle int uid/gid, so make sure we resolve names if needed
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		owner := strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(owner)})
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
			if container.FS.Value.LazyEvalFunc() != nil {
				cache, err := dagql.EngineCache(ctx)
				if err != nil {
					return nil, err
				}
				if err := cache.Evaluate(ctx, rootfsParent, src); err != nil {
					return nil, err
				}
			}
			if container.LazyInit, err = container.FS.Value.WithDirectory(ctx, rootfsParent, mntSubpath, src, filter, owner, permissions, doNotCreateDestPath, attemptUnpackDockerCompatibility, requiredSourcePath, destPathHintIsDirectory, copySourcePathContentsWhenDir); err != nil {
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
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
			if mnt.DirectorySource.Value.LazyEvalFunc() != nil {
				cache, err := dagql.EngineCache(ctx)
				if err != nil {
					return nil, err
				}
				if err := cache.Evaluate(ctx, parentDir, src); err != nil {
					return nil, err
				}
			}
			if container.LazyInit, err = mnt.DirectorySource.Value.WithDirectory(ctx, parentDir, mntSubpath, src, filter, owner, permissions, doNotCreateDestPath, attemptUnpackDockerCompatibility, requiredSourcePath, destPathHintIsDirectory, copySourcePathContentsWhenDir); err != nil {
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
	if owner != "" {
		// files only handle int uid/gid, so make sure we resolve names if needed
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		owner := strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(owner)})
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
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, rootfsParent, src); err != nil {
				return nil, err
			}
			if container.LazyInit, err = container.FS.Value.WithFile(ctx, rootfsParent, mntSubpath, src, permissions, owner, doNotCreateDestPath, attemptUnpackDockerCompatibility); err != nil {
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
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, parentDir, src); err != nil {
				return nil, err
			}
			if container.LazyInit, err = mnt.DirectorySource.Value.WithFile(ctx, parentDir, mntSubpath, src, permissions, owner, doNotCreateDestPath, attemptUnpackDockerCompatibility); err != nil {
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
			rootfsParent, err := containerRootFSSelection(ctx, srv, parent, container.FS)
			if err != nil {
				return nil, err
			}
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, rootfsParent); err != nil {
				return nil, err
			}
			if container.LazyInit, err = container.FS.Value.Without(ctx, rootfsParent, dagql.CurrentCall(ctx), mntSubpath); err != nil {
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
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, parentDir); err != nil {
				return nil, err
			}
			if container.LazyInit, err = mnt.DirectorySource.Value.Without(ctx, parentDir, dagql.CurrentCall(ctx), mntSubpath); err != nil {
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
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, rootfsParent); err != nil {
				return nil, err
			}
			if container.LazyInit, err = container.FS.Value.WithSymlink(ctx, rootfsParent, target, mntSubpath); err != nil {
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
			parentDir, err := containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return nil, err
			}
			if err := cache.Evaluate(ctx, parentDir); err != nil {
				return nil, err
			}
			if container.LazyInit, err = mnt.DirectorySource.Value.WithSymlink(ctx, parentDir, target, mntSubpath); err != nil {
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
func (container *Container) WithUnixSocket(ctx context.Context, target string, source *Socket, owner string) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)
	return container.WithUnixSocketFromParent(ctx, dagql.ObjectResult[*Container]{}, target, source, owner)
}

func (container *Container) WithUnixSocketFromParent(ctx context.Context, parent dagql.ObjectResult[*Container], target string, source *Socket, owner string) (*Container, error) {
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
				return dagql.NewObjectResultForCurrentCall(ctx, srv, container.FS.Value)
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
				return dagql.NewObjectResultForCurrentCall(ctx, srv, mnt.DirectorySource.Value)
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
			return dagql.NewObjectResultForCurrentCall(ctx, srv, mnt.FileSource.Value)
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

	imageDigest, found := resp[exptypes.ExporterImageDigestKey]
	if found {
		dig, err := digest.Parse(imageDigest)
		if err != nil {
			return "", fmt.Errorf("parse digest: %w", err)
		}

		withDig, err := reference.WithDigest(refName, dig)
		if err != nil {
			return "", fmt.Errorf("with digest: %w", err)
		}

		return withDig.String(), nil
	}

	return ref, nil
}

func (container *Container) AsTarball(
	ctx context.Context,
	platformVariants []*Container,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
	filePath string,
) (f *File, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	engineHostPlatform := query.Platform()

	if mediaTypes == "" {
		mediaTypes = OCIMediaTypes
	}

	variants := filterEmptyContainers(append([]*Container{container}, platformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return nil, err
	}

	bkref, err := query.BuildkitCache().New(ctx, nil, nil,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("dagop.fs container.asTarball "+filePath),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()
	err = MountRef(ctx, bkref, nil, func(out string, _ *mount.Mount) error {
		err = bk.ContainerImageToTarball(ctx, engineHostPlatform.Spec(), filepath.Join(out, filePath), inputByPlatform, useOCIMediaTypes(mediaTypes), string(forcedCompression))
		if err != nil {
			return fmt.Errorf("container image to tarball file conversion failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("container image to tarball file conversion failed: %w", err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	f, err = NewFileWithSnapshot(filePath, query.Platform(), nil, snap)
	if err != nil {
		return nil, err
	}
	return f, nil
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
				return err
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

	encodedDesc, ok := resp[exptypes.ExporterImageDescriptorKey]
	if !ok {
		return nil, fmt.Errorf("exporter response missing %s", exptypes.ExporterImageDescriptorKey)
	}
	rawDesc, err := base64.StdEncoding.DecodeString(encodedDesc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding descriptor: %w", err)
	}

	var desc specs.Descriptor
	err = json.Unmarshal(rawDesc, &desc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding descriptor: %w", err)
	}
	return &desc, nil
}

func (container *Container) Import(
	ctx context.Context,
	tarball io.Reader,
	tag string,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	store := query.OCIStore()
	lm := query.LeaseManager()

	var release func(context.Context) error
	loadManifest := func(ctx context.Context) (*specs.Descriptor, error) {
		// override outer ctx with release ctx and set release
		ctx, release, err = leaseutil.WithLease(ctx, lm, leaseutil.MakeTemporary)
		if err != nil {
			return nil, err
		}

		stream := archive.NewImageImportStream(tarball, "")

		desc, err := stream.Import(ctx, store)
		if err != nil {
			return nil, fmt.Errorf("image archive import: %w", err)
		}

		return resolveIndex(ctx, store, desc, container.Platform.Spec(), tag)
	}
	defer func() {
		if release != nil {
			release(ctx)
		}
	}()

	manifestDesc, err := loadManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover: %w", err)
	}

	ctr, err := container.FromInternal(ctx, *manifestDesc)
	if err != nil {
		return nil, err
	}
	if ctr.FS == nil || ctr.FS.self() == nil {
		return nil, fmt.Errorf("missing rootfs directory for import")
	}
	if err := ctr.FS.self().LazyState.Evaluate(ctx, "Directory"); err != nil {
		return nil, fmt.Errorf("evaluate imported rootfs: %w", err)
	}
	return ctr, nil
}

// FromInternal creates a Container from an OCI image descriptor, loading the
// image directly from the main worker OCI store.
func (container *Container) FromInternal(
	ctx context.Context,
	desc specs.Descriptor,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	hsm, err := containersource.NewSource(containersource.SourceOpt{
		Snapshotter:   bk.Worker.Snapshotter,
		ContentStore:  bk.Worker.ContentStore(),
		ImageStore:    bk.Worker.ImageStore,
		CacheAccessor: query.BuildkitCache(),
		RegistryHosts: bk.Worker.RegistryHosts,
		ResolverType:  containersource.ResolverTypeOCILayout,
		LeaseManager:  bk.Worker.LeaseManager(),
	})
	if err != nil {
		return nil, err
	}

	refStr := fmt.Sprintf("dagger/import@%s", desc.Digest)
	attrs := map[string]string{
		pb.AttrOCILayoutStoreID:   buildkit.OCIStoreName,
		pb.AttrOCILayoutSessionID: bk.ID(),
	}
	id, err := hsm.Identifier(refStr, attrs, &pb.Platform{
		Architecture: container.Platform.Architecture,
		OS:           container.Platform.OS,
		Variant:      container.Platform.Variant,
		OSVersion:    container.Platform.OSVersion,
		OSFeatures:   container.Platform.OSFeatures,
	})
	if err != nil {
		return nil, err
	}
	src, err := hsm.Resolve(ctx, id, query.BuildkitSession())
	if err != nil {
		return nil, err
	}

	rootfsDir := &Directory{
		Dir:       "/",
		Platform:  container.Platform,
		Services:  container.Services,
		LazyState: NewLazyState(),
	}
	container.setBareRootFS(rootfsDir)
	rootfsDir.LazyInit = func(ctx context.Context) error {
		bkSessionGroup := NewSessionGroup(bk.ID())
		ref, err := src.Snapshot(ctx, bkSessionGroup)
		if err != nil {
			return err
		}
		return rootfsDir.setSnapshot(ref)
	}

	manifestBlob, err := content.ReadBlob(ctx, query.OCIStore(), desc)
	if err != nil {
		return nil, fmt.Errorf("image archive read manifest blob: %w", err)
	}
	var man specs.Manifest
	err = json.Unmarshal(manifestBlob, &man)
	if err != nil {
		return nil, fmt.Errorf("image archive unmarshal manifest: %w", err)
	}
	configBlob, err := content.ReadBlob(ctx, query.OCIStore(), man.Config)
	if err != nil {
		return nil, fmt.Errorf("image archive read image config blob %s: %w", man.Config.Digest, err)
	}
	var imgSpec dockerspec.DockerOCIImage
	err = json.Unmarshal(configBlob, &imgSpec)
	if err != nil {
		return nil, fmt.Errorf("load image config: %w", err)
	}
	container.Config = imgSpec.Config

	return container, nil
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

	mnt, mntSubpath, err := locatePath(container, path)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", path, err)
	}

	var fileSource dagql.ObjectResult[*File]
	var directorySource dagql.ObjectResult[*Directory]
	var bareFile *File

	switch {
	case mnt == nil: // rootfs
		if container.FS != nil && container.FS.Value != nil {
			directorySource, err = containerRootFSSelection(ctx, srv, parent, container.FS)
			if err != nil {
				return nil, err
			}
		} else {
			directorySource, err = containerRootFSSelection(ctx, srv, parent, container.FS)
			if err != nil {
				return nil, err
			}
		}

	case mnt.DirectorySource != nil: // directory mount
		if mnt.DirectorySource.Value != nil {
			directorySource, err = containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
		} else {
			directorySource, err = containerDirectoryMountSelection(ctx, srv, parent, mnt.DirectorySource, mnt.Target)
			if err != nil {
				return nil, err
			}
		}

	case mnt.FileSource != nil: // file mount
		if mnt.FileSource.Value != nil {
			fileSource, err = containerFileMountSelection(ctx, srv, parent, mnt.FileSource, mnt.Target)
			if err != nil {
				return nil, err
			}
		} else {
			fileSource = *mnt.FileSource.Result
		}

	default:
		return nil, fmt.Errorf("invalid mount source for %s", path)
	}

	if bareFile != nil {
		return bareFile.Open(ctx)
	}

	if fileSource.Self() == nil {
		err = srv.Select(ctx, directorySource, &fileSource, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mntSubpath)},
			},
		})
		if err != nil {
			return nil, err
		}
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

package core

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

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
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/engineutil"
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
	FS *LazyAccessor[*Directory, *Container]

	// Image configuration (env, workdir, etc)
	Config dockerspec.DockerOCIImageConfig

	// List of GPU devices that will be exposed to the container
	EnabledGPUs []string

	// Mount points configured for the container.
	Mounts ContainerMounts

	// MetaSnapshot is the internal exec metadata snapshot containing stdout,
	// stderr, combined output, and exit code files. It will be nil if nothing
	// has run yet.
	MetaSnapshot *LazyAccessor[bkcache.ImmutableRef, *Container]

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
}

type ContainerCloneStateLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
}

type ContainerRootFSLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
}

type ContainerWithRootFSLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Source dagql.ObjectResult[*Directory]
}

type ContainerDirectoryLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Path   string
}

type ContainerFileLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Path   string
}

type ContainerWithDirectoryLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Path   string
	Source dagql.ObjectResult[*Directory]
	Filter CopyFilter
	Owner  string
}

type ContainerWithFileLazy struct {
	LazyState
	Parent      dagql.ObjectResult[*Container]
	Path        string
	Source      dagql.ObjectResult[*File]
	Permissions *int
	Owner       string
}

type ContainerWithMountedDirectoryLazy struct {
	LazyState
	Parent   dagql.ObjectResult[*Container]
	Target   string
	Source   dagql.ObjectResult[*Directory]
	Owner    string
	Readonly bool
}

type ContainerWithMountedFileLazy struct {
	LazyState
	Parent   dagql.ObjectResult[*Container]
	Target   string
	Source   dagql.ObjectResult[*File]
	Owner    string
	Readonly bool
}

type ContainerWithMountedCacheLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
	Cache  dagql.ObjectResult[*CacheVolume]
}

type ContainerWithMountedTempLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
	Size   int
}

type ContainerWithMountedSecretLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
	Source dagql.ObjectResult[*Secret]
	Owner  string
	Mode   fs.FileMode
}

type ContainerWithoutMountLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
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

type ContainerWithUnixSocketLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
	Source dagql.ObjectResult[*Socket]
	Owner  string
}

type ContainerWithoutUnixSocketLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Target string
}

type ContainerImportLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Container]
	Source dagql.ObjectResult[*File]
	Tag    string
}

// ContainerMount is a mount point configured in a container.
type ContainerMount struct {
	// The path of the mount within the container.
	Target string

	// Configure the mount as read-only.
	Readonly bool

	// The following fields are mutually exclusive, only one of them should be set.

	// The mounted directory.
	DirectorySource *LazyAccessor[*Directory, *Container]
	// The mounted file.
	FileSource *LazyAccessor[*File, *Container]
	// The mounted cache.
	CacheSource *CacheMountSource
	// The mounted tmpfs.
	TmpfsSource *TmpfsMountSource
}

type CacheMountSource struct {
	// The cache volume backing this mount.
	Volume dagql.ObjectResult[*CacheVolume]
}

type TmpfsMountSource struct {
	// Configure the size of the mounted tmpfs in bytes.
	Size int
}

type ContainerMounts []ContainerMount

type persistedContainerMountPayload struct {
	Target              string          `json:"target"`
	Readonly            bool            `json:"readonly,omitempty"`
	Kind                string          `json:"kind"`
	Value               json.RawMessage `json:"value,omitempty"`
	CacheSourceResultID uint64          `json:"cacheSourceResultID,omitempty"`
	TmpfsSize           int             `json:"tmpfsSize,omitempty"`
}

const (
	persistedContainerValueFormMaterialized = "materialized"
	persistedContainerValueFormPending      = "pending"
)

const (
	persistedContainerMountKindDirectory = "directory"
	persistedContainerMountKindFile      = "file"
	persistedContainerMountKindCache     = "cache"
	persistedContainerMountKindTmpfs     = "tmpfs"
)

type persistedContainerDirectoryValue struct {
	Form  string          `json:"form"`
	Value json.RawMessage `json:"value"`
}

type persistedContainerFileValue struct {
	Form  string          `json:"form"`
	Value json.RawMessage `json:"value"`
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
	FS                 json.RawMessage                     `json:"fs,omitempty"`
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

type persistedContainerCloneStateLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
}

type persistedContainerFromLazy struct {
	CanonicalRef string `json:"canonicalRef"`
}

type persistedContainerWithRootFSLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	SourceResultID uint64 `json:"sourceResultID"`
}

type persistedContainerWithDirectoryLazy struct {
	ParentResultID uint64     `json:"parentResultID"`
	Path           string     `json:"path"`
	SourceResultID uint64     `json:"sourceResultID"`
	Filter         CopyFilter `json:"filter,omitempty"`
	Owner          string     `json:"owner,omitempty"`
}

type persistedContainerWithFileLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Path           string `json:"path"`
	SourceResultID uint64 `json:"sourceResultID"`
	Permissions    *int   `json:"permissions,omitempty"`
	Owner          string `json:"owner,omitempty"`
}

type persistedContainerWithMountedDirectoryLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	SourceResultID uint64 `json:"sourceResultID"`
	Owner          string `json:"owner,omitempty"`
	Readonly       bool   `json:"readonly,omitempty"`
}

type persistedContainerWithMountedFileLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	SourceResultID uint64 `json:"sourceResultID"`
	Owner          string `json:"owner,omitempty"`
	Readonly       bool   `json:"readonly,omitempty"`
}

type persistedContainerWithMountedCacheLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	CacheResultID  uint64 `json:"cacheResultID"`
}

type persistedContainerWithMountedTempLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	Size           int    `json:"size,omitempty"`
}

type persistedContainerWithMountedSecretLazy struct {
	ParentResultID uint64      `json:"parentResultID"`
	Target         string      `json:"target"`
	SourceResultID uint64      `json:"sourceResultID"`
	Owner          string      `json:"owner,omitempty"`
	Mode           fs.FileMode `json:"mode,omitempty"`
}

type persistedContainerWithoutMountLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
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

type persistedContainerWithUnixSocketLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
	SourceResultID uint64 `json:"sourceResultID"`
	Owner          string `json:"owner,omitempty"`
}

type persistedContainerWithoutUnixSocketLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Target         string `json:"target"`
}

type persistedContainerImportLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	SourceResultID uint64 `json:"sourceResultID"`
	Tag            string `json:"tag,omitempty"`
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

func NewContainer(platform Platform) *Container {
	return &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Platform:     platform,
	}
}

func cloneDetachedDirectoryForContainerResult(ctx context.Context, src *Directory) (*Directory, error) {
	if src == nil {
		return nil, nil
	}
	if src.Lazy != nil {
		return nil, fmt.Errorf("clone detached directory for container result: directory must be materialized, got lazy %T", src.Lazy)
	}

	cp := &Directory{
		Platform: src.Platform,
		Services: slices.Clone(src.Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	if dirPath, ok := src.Dir.Peek(); ok {
		cp.Dir.setValue(dirPath)
	}

	snapshot, ok := src.Snapshot.Peek()
	if !ok || snapshot == nil {
		return cp, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, err
	}
	cp.Snapshot.setValue(reopened)
	return cp, nil
}

func cloneDetachedFileForContainerResult(ctx context.Context, src *File) (*File, error) {
	if src == nil {
		return nil, nil
	}
	if src.Lazy != nil {
		return nil, fmt.Errorf("clone detached file for container result: file must be materialized, got lazy %T", src.Lazy)
	}

	cp := &File{
		Platform: src.Platform,
		Services: slices.Clone(src.Services),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	if filePath, ok := src.File.Peek(); ok {
		cp.File.setValue(filePath)
	}

	snapshot, ok := src.Snapshot.Peek()
	if !ok || snapshot == nil {
		return cp, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, err
	}
	cp.Snapshot.setValue(reopened)
	return cp, nil
}

func CloneContainerMetaSnapshot(ctx context.Context, src *LazyAccessor[bkcache.ImmutableRef, *Container]) (*LazyAccessor[bkcache.ImmutableRef, *Container], error) {
	if src == nil {
		return nil, nil
	}
	cp := new(LazyAccessor[bkcache.ImmutableRef, *Container])
	snapshot, ok := src.Peek()
	if !ok || snapshot == nil {
		return cp, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, err
	}
	cp.setValue(reopened)
	return cp, nil
}

func CloneContainerDirectoryAccessor(ctx context.Context, src *LazyAccessor[*Directory, *Container]) (*LazyAccessor[*Directory, *Container], error) {
	if src == nil {
		return nil, nil
	}
	cp := new(LazyAccessor[*Directory, *Container])
	dir, ok := src.Peek()
	if !ok || dir == nil {
		return cp, nil
	}
	detached, err := cloneDetachedDirectoryForContainerResult(ctx, dir)
	if err != nil {
		return nil, err
	}
	cp.setValue(detached)
	return cp, nil
}

func CloneContainerFileAccessor(ctx context.Context, src *LazyAccessor[*File, *Container]) (*LazyAccessor[*File, *Container], error) {
	if src == nil {
		return nil, nil
	}
	cp := new(LazyAccessor[*File, *Container])
	file, ok := src.Peek()
	if !ok || file == nil {
		return cp, nil
	}
	detached, err := cloneDetachedFileForContainerResult(ctx, file)
	if err != nil {
		return nil, err
	}
	cp.setValue(detached)
	return cp, nil
}

func CloneContainerMounts(ctx context.Context, mounts ContainerMounts) (ContainerMounts, error) {
	if mounts == nil {
		return nil, nil
	}
	cp := make(ContainerMounts, len(mounts))
	for i, mnt := range mounts {
		cp[i] = mnt
		var err error
		cp[i].DirectorySource, err = CloneContainerDirectoryAccessor(ctx, mnt.DirectorySource)
		if err != nil {
			return nil, err
		}
		cp[i].FileSource, err = CloneContainerFileAccessor(ctx, mnt.FileSource)
		if err != nil {
			return nil, err
		}
	}
	return cp, nil
}

func materializeContainerStateFromParent(ctx context.Context, dst *Container, parent dagql.ObjectResult[*Container]) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}

	clonedFS, err := CloneContainerDirectoryAccessor(ctx, parent.Self().FS)
	if err != nil {
		return err
	}
	clonedMounts, err := CloneContainerMounts(ctx, parent.Self().Mounts)
	if err != nil {
		return err
	}
	clonedMeta, err := CloneContainerMetaSnapshot(ctx, parent.Self().MetaSnapshot)
	if err != nil {
		return err
	}

	dst.FS = clonedFS
	dst.Mounts = clonedMounts
	dst.MetaSnapshot = clonedMeta
	return nil
}

var _ dagql.OnReleaser = (*Container)(nil)
var _ dagql.HasDependencyResults = (*Container)(nil)
var _ dagql.HasLazyEvaluation = (*Container)(nil)

func (container *Container) LazyEvalFunc() dagql.LazyEvalFunc {
	if container == nil {
		return nil
	}
	if container.Lazy == nil {
		return nil
	}
	return func(ctx context.Context) error {
		return container.Lazy.Evaluate(ctx, container)
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
	owned := make([]dagql.AnyResult, 0, len(container.Mounts))
	for i := range container.Mounts {
		mnt := &container.Mounts[i]
		switch {
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

func (container *Container) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if container == nil {
		return nil
	}
	var links []dagql.PersistedSnapshotRefLink
	if container.MetaSnapshot != nil {
		if snapshot, ok := container.MetaSnapshot.Peek(); ok && snapshot != nil {
			links = append(links, dagql.PersistedSnapshotRefLink{
				RefKey: snapshot.SnapshotID(),
				Role:   "meta",
			})
		}
	}
	if container.FS != nil {
		if dir, ok := container.FS.Peek(); ok && dir != nil {
			if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
				links = append(links, dagql.PersistedSnapshotRefLink{
					RefKey: snapshot.SnapshotID(),
					Role:   "fs",
				})
			}
		}
	}
	for i, mnt := range container.Mounts {
		if mnt.DirectorySource != nil {
			if dir, ok := mnt.DirectorySource.Peek(); ok && dir != nil {
				if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
					links = append(links, dagql.PersistedSnapshotRefLink{
						RefKey: snapshot.SnapshotID(),
						Role:   fmt.Sprintf("mount_dir:%d", i),
					})
				}
			}
		}
		if mnt.FileSource != nil {
			if file, ok := mnt.FileSource.Peek(); ok && file != nil {
				if snapshot, ok := file.Snapshot.Peek(); ok && snapshot != nil {
					links = append(links, dagql.PersistedSnapshotRefLink{
						RefKey: snapshot.SnapshotID(),
						Role:   fmt.Sprintf("mount_file:%d", i),
					})
				}
			}
		}
	}
	return links
}

func (container *Container) CacheUsageIdentities() []string {
	if container == nil {
		return nil
	}

	seen := make(map[string]struct{})
	identities := make([]string, 0, 1+len(container.Mounts)*2)
	add := func(identity string) {
		if identity == "" {
			return
		}
		if _, ok := seen[identity]; ok {
			return
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}

	if container.MetaSnapshot != nil {
		if snapshot, ok := container.MetaSnapshot.Peek(); ok && snapshot != nil {
			add(snapshot.SnapshotID())
		}
	}
	if container.FS != nil {
		if dir, ok := container.FS.Peek(); ok && dir != nil {
			if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
				add(snapshot.SnapshotID())
			}
		}
	}
	for _, mnt := range container.Mounts {
		if mnt.DirectorySource != nil {
			if dir, ok := mnt.DirectorySource.Peek(); ok && dir != nil {
				if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
					add(snapshot.SnapshotID())
				}
			}
		}
		if mnt.FileSource != nil {
			if file, ok := mnt.FileSource.Peek(); ok && file != nil {
				if snapshot, ok := file.Snapshot.Peek(); ok && snapshot != nil {
					add(snapshot.SnapshotID())
				}
			}
		}
	}

	slices.Sort(identities)
	return identities
}

func (container *Container) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if container == nil || identity == "" {
		return 0, false, nil
	}

	if container.MetaSnapshot != nil {
		if snapshot, ok := container.MetaSnapshot.Peek(); ok && snapshot != nil && snapshot.SnapshotID() == identity {
			size, err := snapshot.Size(ctx)
			if err != nil {
				return 0, false, err
			}
			return size, true, nil
		}
	}

	if container.FS != nil {
		if dir, ok := container.FS.Peek(); ok && dir != nil {
			if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil && snapshot.SnapshotID() == identity {
				size, err := snapshot.Size(ctx)
				if err != nil {
					return 0, false, err
				}
				return size, true, nil
			}
		}
	}

	for _, mnt := range container.Mounts {
		if mnt.DirectorySource != nil {
			if dir, ok := mnt.DirectorySource.Peek(); ok && dir != nil {
				if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil && snapshot.SnapshotID() == identity {
					size, err := snapshot.Size(ctx)
					if err != nil {
						return 0, false, err
					}
					return size, true, nil
				}
			}
		}
		if mnt.FileSource != nil {
			if file, ok := mnt.FileSource.Peek(); ok && file != nil {
				if snapshot, ok := file.Snapshot.Peek(); ok && snapshot != nil && snapshot.SnapshotID() == identity {
					size, err := snapshot.Size(ctx)
					if err != nil {
						return 0, false, err
					}
					return size, true, nil
				}
			}
		}
	}

	owned := false
	for _, ownedIdentity := range container.CacheUsageIdentities() {
		if ownedIdentity == identity {
			owned = true
			break
		}
	}
	if !owned {
		return 0, false, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return 0, false, err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, identity, bkcache.NoUpdateLastUsed)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = ref.Release(context.WithoutCancel(ctx))
	}()
	size, err := ref.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func encodePersistedContainerDirectoryValue(ctx context.Context, cache dagql.PersistedObjectCache, dir *Directory) (json.RawMessage, error) {
	if dir == nil {
		encoded, err := json.Marshal(persistedContainerDirectoryValue{Form: persistedContainerValueFormPending})
		if err != nil {
			return nil, fmt.Errorf("marshal pending container directory value: %w", err)
		}
		return encoded, nil
	}
	value, err := dir.EncodePersistedObject(ctx, cache)
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerDirectoryValue{
		Form:  persistedContainerValueFormMaterialized,
		Value: value,
	})
}

func encodePersistedContainerFileValue(ctx context.Context, cache dagql.PersistedObjectCache, file *File) (json.RawMessage, error) {
	if file == nil {
		encoded, err := json.Marshal(persistedContainerFileValue{Form: persistedContainerValueFormPending})
		if err != nil {
			return nil, fmt.Errorf("marshal pending container file value: %w", err)
		}
		return encoded, nil
	}
	value, err := file.EncodePersistedObject(ctx, cache)
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerFileValue{
		Form:  persistedContainerValueFormMaterialized,
		Value: value,
	})
}

func decodePersistedContainerDirectoryValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (decodedContainerDirectoryValue, error) {
	var wrapped persistedContainerDirectoryValue
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory value: %w", err)
	}
	if wrapped.Form == "" {
		wrapped.Form = persistedContainerValueFormMaterialized
		wrapped.Value = payload
	}

	switch wrapped.Form {
	case persistedContainerValueFormPending:
		return decodedContainerDirectoryValue{Dir: nil, Kind: wrapped.Form}, nil
	case persistedContainerValueFormMaterialized:
		typed, err := new(Directory).DecodePersistedObject(ctx, dag, resultID, nil, wrapped.Value)
		if err != nil {
			return decodedContainerDirectoryValue{}, err
		}
		dir, ok := typed.(*Directory)
		if !ok {
			return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory value: unexpected typed value %T", typed)
		}
		return decodedContainerDirectoryValue{Dir: dir, Kind: wrapped.Form}, nil
	default:
		return decodedContainerDirectoryValue{}, fmt.Errorf("decode persisted container directory value: unsupported form %q", wrapped.Form)
	}
}

func decodePersistedContainerFileValue(ctx context.Context, dag *dagql.Server, resultID uint64, role string, payload json.RawMessage) (decodedContainerFileValue, error) {
	var wrapped persistedContainerFileValue
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file value: %w", err)
	}
	if wrapped.Form == "" {
		wrapped.Form = persistedContainerValueFormMaterialized
		wrapped.Value = payload
	}

	switch wrapped.Form {
	case persistedContainerValueFormPending:
		return decodedContainerFileValue{File: nil, Kind: wrapped.Form}, nil
	case persistedContainerValueFormMaterialized:
		typed, err := new(File).DecodePersistedObject(ctx, dag, resultID, nil, wrapped.Value)
		if err != nil {
			return decodedContainerFileValue{}, err
		}
		file, ok := typed.(*File)
		if !ok {
			return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file value: unexpected typed value %T", typed)
		}
		return decodedContainerFileValue{File: file, Kind: wrapped.Form}, nil
	default:
		return decodedContainerFileValue{}, fmt.Errorf("decode persisted container file value: unsupported form %q", wrapped.Form)
	}
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
		fsValue, ok := container.FS.Peek()
		if ok && fsValue != nil {
			encoded, err := encodePersistedContainerDirectoryValue(ctx, cache, fsValue)
			if err != nil {
				return nil, err
			}
			payload.FS = encoded
		}
	}

	for _, mnt := range container.Mounts {
		encoded := persistedContainerMountPayload{
			Target:   mnt.Target,
			Readonly: mnt.Readonly,
		}
		switch {
		case mnt.DirectorySource != nil:
			encoded.Kind = persistedContainerMountKindDirectory
			if dir, ok := mnt.DirectorySource.Peek(); ok && dir != nil {
				val, err := encodePersistedContainerDirectoryValue(ctx, cache, dir)
				if err != nil {
					return nil, err
				}
				encoded.Value = val
			}
		case mnt.FileSource != nil:
			encoded.Kind = persistedContainerMountKindFile
			if file, ok := mnt.FileSource.Peek(); ok && file != nil {
				val, err := encodePersistedContainerFileValue(ctx, cache, file)
				if err != nil {
					return nil, err
				}
				encoded.Value = val
			}
		case mnt.CacheSource != nil:
			encoded.Kind = persistedContainerMountKindCache
			id, err := encodePersistedObjectRef(cache, mnt.CacheSource.Volume, fmt.Sprintf("cache mount %q", mnt.Target))
			if err != nil {
				return nil, err
			}
			encoded.CacheSourceResultID = id
		case mnt.TmpfsSource != nil:
			encoded.Kind = persistedContainerMountKindTmpfs
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

	fs := new(LazyAccessor[*Directory, *Container])
	var decodedRootFS decodedContainerDirectoryValue
	if len(persisted.FS) > 0 {
		rootfs, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, "fs", persisted.FS)
		if err != nil {
			return nil, err
		}
		decodedRootFS = rootfs
		if rootfs.Dir != nil {
			fs.setValue(rootfs.Dir)
		}
	}

	mounts := make(ContainerMounts, 0, len(persisted.Mounts))
	decodedMounts := make([]decodedContainerMount, 0, len(persisted.Mounts))
	for _, persistedMount := range persisted.Mounts {
		mnt := ContainerMount{
			Target:   persistedMount.Target,
			Readonly: persistedMount.Readonly,
		}
		decodedMount := decodedContainerMount{Kind: persistedMount.Kind}
		switch persistedMount.Kind {
		case persistedContainerMountKindDirectory:
			mnt.DirectorySource = new(LazyAccessor[*Directory, *Container])
			if len(persistedMount.Value) > 0 {
				dirVal, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, fmt.Sprintf("mount_dir:%d", len(mounts)), persistedMount.Value)
				if err != nil {
					return nil, err
				}
				decodedMount.Kind = dirVal.Kind
				if dirVal.Dir != nil {
					mnt.DirectorySource.setValue(dirVal.Dir)
				}
			}
		case persistedContainerMountKindFile:
			mnt.FileSource = new(LazyAccessor[*File, *Container])
			if len(persistedMount.Value) > 0 {
				fileVal, err := decodePersistedContainerFileValue(ctx, dag, resultID, fmt.Sprintf("mount_file:%d", len(mounts)), persistedMount.Value)
				if err != nil {
					return nil, err
				}
				decodedMount.Kind = fileVal.Kind
				if fileVal.File != nil {
					mnt.FileSource.setValue(fileVal.File)
				}
			}
		case persistedContainerMountKindCache:
			cacheRes, err := loadPersistedObjectResultByResultID[*CacheVolume](ctx, dag, persistedMount.CacheSourceResultID, "container mount cache")
			if err != nil {
				return nil, err
			}
			mnt.CacheSource = &CacheMountSource{Volume: cacheRes}
		case persistedContainerMountKindTmpfs:
			mnt.TmpfsSource = &TmpfsMountSource{Size: persistedMount.TmpfsSize}
		default:
			return nil, fmt.Errorf("decode persisted container mount %q: unsupported kind %q", persistedMount.Target, persistedMount.Kind)
		}
		mounts = append(mounts, mnt)
		decodedMounts = append(decodedMounts, decodedMount)
	}

	metaAccessor := new(LazyAccessor[bkcache.ImmutableRef, *Container])
	links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "container")
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.Role != "meta" {
			continue
		}
		metaSnapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "container", "meta")
		if err != nil {
			return nil, err
		}
		metaAccessor.setValue(metaSnapshot)
		break
	}

	container := &Container{
		FS:                 fs,
		Config:             persisted.Config,
		EnabledGPUs:        slices.Clone(persisted.EnabledGPUs),
		Mounts:             mounts,
		MetaSnapshot:       metaAccessor,
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
		if snapshot, ok := container.MetaSnapshot.Peek(); ok && snapshot != nil {
			rerr = stderrors.Join(rerr, snapshot.Release(ctx))
		}
	}
	if container.FS != nil {
		if dir, ok := container.FS.Peek(); ok && dir != nil {
			rerr = stderrors.Join(rerr, dir.OnRelease(ctx))
		}
	}
	for i := range container.Mounts {
		mnt := &container.Mounts[i]
		if mnt.DirectorySource != nil {
			if dir, ok := mnt.DirectorySource.Peek(); ok && dir != nil {
				rerr = stderrors.Join(rerr, dir.OnRelease(ctx))
			}
		}
		if mnt.FileSource != nil {
			if file, ok := mnt.FileSource.Peek(); ok && file != nil {
				rerr = stderrors.Join(rerr, file.OnRelease(ctx))
			}
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

func attachSecretResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*Secret], label string) (dagql.ObjectResult[*Secret], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*Secret]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Secret])
	if !ok {
		return dagql.ObjectResult[*Secret]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

func attachSocketResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*Socket], label string) (dagql.ObjectResult[*Socket], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*Socket]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Socket])
	if !ok {
		return dagql.ObjectResult[*Socket]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

func attachCacheVolumeResult(attach func(dagql.AnyResult) (dagql.AnyResult, error), res dagql.ObjectResult[*CacheVolume], label string) (dagql.ObjectResult[*CacheVolume], error) {
	attached, err := attach(res)
	if err != nil {
		return dagql.ObjectResult[*CacheVolume]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*CacheVolume])
	if !ok {
		return dagql.ObjectResult[*CacheVolume]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
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
		if container.FS == nil {
			return nil, fmt.Errorf("missing bare rootfs output for %s", targetPath)
		}
		dir, ok := container.FS.Peek()
		if !ok || dir == nil {
			return nil, fmt.Errorf("missing bare rootfs output for %s", targetPath)
		}
		return dir, nil
	case mnt.DirectorySource != nil:
		dir, ok := mnt.DirectorySource.Peek()
		if !ok || dir == nil {
			return nil, fmt.Errorf("missing bare directory output for %s", targetPath)
		}
		return dir, nil
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
		var dir dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, parent, &dir, dagql.Selector{Field: "rootfs"}); err != nil {
			return dagql.ObjectResult[*Directory]{}, err
		}
		return dir, nil
	case mnt.DirectorySource != nil:
		var dir dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, parent, &dir, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mnt.Target)},
			},
		}); err != nil {
			return dagql.ObjectResult[*Directory]{}, err
		}
		return dir, nil
	default:
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("path %s does not resolve to a directory target parent", targetPath)
	}
}

func (lazy *ContainerCloneStateLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.cloneState", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerCloneStateLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container cloneState parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerCloneStateLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container cloneState parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerCloneStateLazy{
		ParentResultID: parentID,
	})
}

func (lazy *ContainerRootFSLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Container.rootfs", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}

		parent := lazy.Parent.Self()
		if parent == nil {
			return fmt.Errorf("container rootfs lazy: nil parent container")
		}

		if parent.FS != nil {
			if src, ok := parent.FS.Peek(); ok && src != nil {
				detached, err := cloneDetachedDirectoryForContainerResult(ctx, src)
				if err != nil {
					return err
				}
				dir.Platform = detached.Platform
				dir.Services = slices.Clone(detached.Services)
				if dirPath, ok := detached.Dir.Peek(); ok {
					dir.Dir.setValue(dirPath)
				}
				if snapshot, ok := detached.Snapshot.Peek(); ok && snapshot != nil {
					dir.Snapshot.setValue(snapshot)
				}
				dir.Lazy = nil
				return nil
			}
		}

		scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		dir.Dir.setValue(scratchDir)
		dir.Snapshot.setValue(scratchSnapshot)
		dir.Lazy = nil
		return nil
	})
}

func (lazy *ContainerRootFSLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container rootfs parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (*ContainerRootFSLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("encode persisted container rootfs lazy: unsupported top-level form")
}

func (lazy *ContainerWithRootFSLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withRootfs", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithRootFS(ctx, lazy.Source)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithRootFSLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withRootfs parent")
	if err != nil {
		return nil, err
	}
	source, err := attachDirectoryResult(attach, lazy.Source, "attach container withRootfs source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithRootFSLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withRootfs parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withRootfs source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithRootFSLazy{
		ParentResultID: parentID,
		SourceResultID: sourceID,
	})
}

func (lazy *ContainerDirectoryLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Container.directory", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}

		parent := lazy.Parent.Self()
		if parent == nil {
			return fmt.Errorf("container directory lazy: nil parent container")
		}

		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}

		mnt, subpath, err := locatePath(parent, lazy.Path)
		if err != nil {
			return err
		}

		var resolved dagql.ObjectResult[*Directory]
		switch {
		case mnt == nil:
			var rootfs dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, lazy.Parent, &rootfs, dagql.Selector{Field: "rootfs"}); err != nil {
				return err
			}
			if subpath == "" || subpath == "." || subpath == "/" {
				resolved = rootfs
			} else {
				if err := srv.Select(ctx, rootfs, &resolved, dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(subpath)},
					},
				}); err != nil {
					return err
				}
			}
		case mnt.DirectorySource != nil:
			mountedDir, ok := mnt.DirectorySource.Peek()
			if !ok || mountedDir == nil {
				return fmt.Errorf("container directory lazy: missing mounted directory source for %s", mnt.Target)
			}
			if subpath == "" || subpath == "." {
				detached, err := cloneDetachedDirectoryForContainerResult(ctx, mountedDir)
				if err != nil {
					return err
				}
				dir.Platform = detached.Platform
				dir.Services = slices.Clone(detached.Services)
				if dirPath, ok := detached.Dir.Peek(); ok {
					dir.Dir.setValue(dirPath)
				}
				if snapshot, ok := detached.Snapshot.Peek(); ok && snapshot != nil {
					dir.Snapshot.setValue(snapshot)
				}
				dir.Lazy = nil
				return nil
			}
			mountedDirPath, ok := mountedDir.Dir.Peek()
			if !ok {
				return fmt.Errorf("container directory lazy: missing mounted directory path for %s", mnt.Target)
			}
			mountedSnapshot, ok := mountedDir.Snapshot.Peek()
			if !ok || mountedSnapshot == nil {
				return fmt.Errorf("container directory lazy: missing mounted directory snapshot for %s", mnt.Target)
			}
			finalDir := path.Join(mountedDirPath, subpath)
			if err := MountRef(ctx, mountedSnapshot, func(root string, _ *mount.Mount) error {
				resolvedPath, err := containerdfs.RootPath(root, finalDir)
				if err != nil {
					return err
				}
				info, err := os.Lstat(resolvedPath)
				if err != nil {
					return TrimErrPathPrefix(err, root)
				}
				if !info.IsDir() {
					return notADirectoryError{fmt.Errorf("path %s is a file, not a directory", lazy.Path)}
				}
				return nil
			}); err != nil {
				return RestoreErrPath(err, lazy.Path)
			}
			query, err := CurrentQuery(ctx)
			if err != nil {
				return err
			}
			reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, mountedSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
			if err != nil {
				return err
			}
			dir.Platform = mountedDir.Platform
			dir.Services = slices.Clone(mountedDir.Services)
			dir.Dir.setValue(finalDir)
			dir.Snapshot.setValue(reopened)
			dir.Lazy = nil
			return nil
		case mnt.FileSource != nil:
			return notADirectoryError{fmt.Errorf("path %s is a file, not a directory", lazy.Path)}
		default:
			return fmt.Errorf("container directory lazy: invalid path %s in container mounts", lazy.Path)
		}

		if err := cache.Evaluate(ctx, resolved); err != nil {
			return err
		}

		src := resolved.Self()
		if src == nil {
			return fmt.Errorf("container directory lazy: nil resolved directory")
		}
		detached, err := cloneDetachedDirectoryForContainerResult(ctx, src)
		if err != nil {
			return err
		}
		dir.Platform = detached.Platform
		dir.Services = slices.Clone(detached.Services)
		if dirPath, ok := detached.Dir.Peek(); ok {
			dir.Dir.setValue(dirPath)
		}
		if snapshot, ok := detached.Snapshot.Peek(); ok && snapshot != nil {
			dir.Snapshot.setValue(snapshot)
		}
		dir.Lazy = nil
		return nil
	})
}

func (lazy *ContainerDirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container directory parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (*ContainerDirectoryLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("encode persisted container directory lazy: unsupported top-level form")
}

func (lazy *ContainerFileLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "Container.file", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}

		parent := lazy.Parent.Self()
		if parent == nil {
			return fmt.Errorf("container file lazy: nil parent container")
		}

		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}

		mnt, subpath, err := locatePath(parent, lazy.Path)
		if err != nil {
			return err
		}

		var resolved dagql.ObjectResult[*File]
		switch {
		case mnt == nil:
			var rootfs dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, lazy.Parent, &rootfs, dagql.Selector{Field: "rootfs"}); err != nil {
				return err
			}
			if err := srv.Select(ctx, rootfs, &resolved, dagql.Selector{
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(subpath)},
				},
			}); err != nil {
				return err
			}
		case mnt.DirectorySource != nil:
			mountedDir, ok := mnt.DirectorySource.Peek()
			if !ok || mountedDir == nil {
				return fmt.Errorf("container file lazy: missing mounted directory source for %s", mnt.Target)
			}
			mountedDirPath, ok := mountedDir.Dir.Peek()
			if !ok {
				return fmt.Errorf("container file lazy: missing mounted directory path for %s", mnt.Target)
			}
			mountedSnapshot, ok := mountedDir.Snapshot.Peek()
			if !ok || mountedSnapshot == nil {
				return fmt.Errorf("container file lazy: missing mounted directory snapshot for %s", mnt.Target)
			}
			finalFile := path.Join(mountedDirPath, subpath)
			if err := MountRef(ctx, mountedSnapshot, func(root string, _ *mount.Mount) error {
				resolvedPath, err := containerdfs.RootPath(root, finalFile)
				if err != nil {
					return err
				}
				info, err := os.Lstat(resolvedPath)
				if err != nil {
					return TrimErrPathPrefix(err, root)
				}
				if info.IsDir() {
					return notAFileError{fmt.Errorf("path %s is a directory, not a file", lazy.Path)}
				}
				return nil
			}); err != nil {
				return RestoreErrPath(err, lazy.Path)
			}
			query, err := CurrentQuery(ctx)
			if err != nil {
				return err
			}
			reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, mountedSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
			if err != nil {
				return err
			}
			file.Platform = mountedDir.Platform
			file.Services = slices.Clone(mountedDir.Services)
			file.File.setValue(finalFile)
			file.Snapshot.setValue(reopened)
			file.Lazy = nil
			return nil
		case mnt.FileSource != nil:
			mountedFile, ok := mnt.FileSource.Peek()
			if !ok || mountedFile == nil {
				return fmt.Errorf("container file lazy: missing mounted file source for %s", mnt.Target)
			}
			if subpath != "" && subpath != "." {
				return notAFileError{fmt.Errorf("path %s is a directory, not a file", lazy.Path)}
			}
			detached, err := cloneDetachedFileForContainerResult(ctx, mountedFile)
			if err != nil {
				return err
			}
			file.Platform = detached.Platform
			file.Services = slices.Clone(detached.Services)
			if filePath, ok := detached.File.Peek(); ok {
				file.File.setValue(filePath)
			}
			if snapshot, ok := detached.Snapshot.Peek(); ok && snapshot != nil {
				file.Snapshot.setValue(snapshot)
			}
			file.Lazy = nil
			return nil
		default:
			return fmt.Errorf("container file lazy: invalid path %s in container mounts", lazy.Path)
		}

		if err := cache.Evaluate(ctx, resolved); err != nil {
			return err
		}

		src := resolved.Self()
		if src == nil {
			return fmt.Errorf("container file lazy: nil resolved file")
		}
		detached, err := cloneDetachedFileForContainerResult(ctx, src)
		if err != nil {
			return err
		}
		file.Platform = detached.Platform
		file.Services = slices.Clone(detached.Services)
		if filePath, ok := detached.File.Peek(); ok {
			file.File.setValue(filePath)
		}
		if snapshot, ok := detached.Snapshot.Peek(); ok && snapshot != nil {
			file.Snapshot.setValue(snapshot)
		}
		file.Lazy = nil
		return nil
	})
}

func (lazy *ContainerFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container file parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (*ContainerFileLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("encode persisted container file lazy: unsupported top-level form")
}

func (lazy *ContainerWithDirectoryLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withDirectory", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithDirectory(ctx, lazy.Parent, lazy.Path, lazy.Source, lazy.Filter, lazy.Owner)
		if err != nil {
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
		ParentResultID: parentID,
		Path:           lazy.Path,
		SourceResultID: sourceID,
		Filter:         lazy.Filter,
		Owner:          lazy.Owner,
	})
}

func (lazy *ContainerWithFileLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withFile", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithFile(ctx, lazy.Parent, lazy.Path, lazy.Source, lazy.Permissions, lazy.Owner)
		if err != nil {
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
		ParentResultID: parentID,
		Path:           lazy.Path,
		SourceResultID: sourceID,
		Permissions:    lazy.Permissions,
		Owner:          lazy.Owner,
	})
}

func (lazy *ContainerWithMountedDirectoryLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withMountedDirectory", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithMountedDirectory(ctx, lazy.Parent, lazy.Target, lazy.Source, lazy.Owner, lazy.Readonly)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithMountedDirectoryLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withMountedDirectory parent")
	if err != nil {
		return nil, err
	}
	source, err := attachDirectoryResult(attach, lazy.Source, "attach container withMountedDirectory source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithMountedDirectoryLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withMountedDirectory parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withMountedDirectory source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithMountedDirectoryLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		SourceResultID: sourceID,
		Owner:          lazy.Owner,
		Readonly:       lazy.Readonly,
	})
}

func (lazy *ContainerWithMountedFileLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withMountedFile", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithMountedFile(ctx, lazy.Parent, lazy.Target, lazy.Source, lazy.Owner, lazy.Readonly)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithMountedFileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withMountedFile parent")
	if err != nil {
		return nil, err
	}
	source, err := attachFileResult(attach, lazy.Source, "attach container withMountedFile source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithMountedFileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withMountedFile parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withMountedFile source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithMountedFileLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		SourceResultID: sourceID,
		Owner:          lazy.Owner,
		Readonly:       lazy.Readonly,
	})
}

func (lazy *ContainerWithMountedCacheLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withMountedCache", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithMountedCache(ctx, lazy.Target, lazy.Cache)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithMountedCacheLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withMountedCache parent")
	if err != nil {
		return nil, err
	}
	cacheVolume, err := attachCacheVolumeResult(attach, lazy.Cache, "attach container withMountedCache cache")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Cache = cacheVolume
	return []dagql.AnyResult{parent, cacheVolume}, nil
}

func (lazy *ContainerWithMountedCacheLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withMountedCache parent")
	if err != nil {
		return nil, err
	}
	cacheID, err := encodePersistedObjectRef(cache, lazy.Cache, "container withMountedCache cache")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithMountedCacheLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		CacheResultID:  cacheID,
	})
}

func (lazy *ContainerWithMountedTempLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withMountedTemp", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithMountedTemp(ctx, lazy.Target, lazy.Size)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithMountedTempLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withMountedTemp parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerWithMountedTempLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withMountedTemp parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithMountedTempLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		Size:           lazy.Size,
	})
}

func (lazy *ContainerWithMountedSecretLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withMountedSecret", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithMountedSecret(ctx, lazy.Parent, lazy.Target, lazy.Source, lazy.Owner, lazy.Mode)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithMountedSecretLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withMountedSecret parent")
	if err != nil {
		return nil, err
	}
	source, err := attachSecretResult(attach, lazy.Source, "attach container withMountedSecret source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithMountedSecretLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withMountedSecret parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withMountedSecret source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithMountedSecretLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		SourceResultID: sourceID,
		Owner:          lazy.Owner,
		Mode:           lazy.Mode,
	})
}

func (lazy *ContainerWithoutMountLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withoutMount", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithoutMount(ctx, lazy.Target)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithoutMountLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withoutMount parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerWithoutMountLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withoutMount parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithoutMountLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
	})
}

func (lazy *ContainerWithoutPathLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withoutPath", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithoutPaths(ctx, lazy.Parent, lazy.Path)
		if err != nil {
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
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithSymlink(ctx, lazy.Parent, lazy.Target, lazy.LinkPath)
		if err != nil {
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

func (lazy *ContainerWithUnixSocketLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withUnixSocket", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithUnixSocketFromParent(ctx, lazy.Parent, lazy.Target, lazy.Source, lazy.Owner)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithUnixSocketLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withUnixSocket parent")
	if err != nil {
		return nil, err
	}
	source, err := attachSocketResult(attach, lazy.Source, "attach container withUnixSocket source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerWithUnixSocketLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withUnixSocket parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container withUnixSocket source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithUnixSocketLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
		SourceResultID: sourceID,
		Owner:          lazy.Owner,
	})
}

func (lazy *ContainerWithoutUnixSocketLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.withoutUnixSocket", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		_, err := container.WithoutUnixSocket(ctx, lazy.Target)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerWithoutUnixSocketLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container withoutUnixSocket parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *ContainerWithoutUnixSocketLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container withoutUnixSocket parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerWithoutUnixSocketLazy{
		ParentResultID: parentID,
		Target:         lazy.Target,
	})
}

func (lazy *ContainerImportLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.import", func(ctx context.Context) error {
		if err := materializeContainerStateFromParent(ctx, container, lazy.Parent); err != nil {
			return err
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Source); err != nil {
			return err
		}
		r, err := lazy.Source.Self().Open(ctx, lazy.Source)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = container.Import(ctx, r, lazy.Tag)
		if err != nil {
			return err
		}
		container.Lazy = nil
		return nil
	})
}

func (lazy *ContainerImportLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachContainerResult(attach, lazy.Parent, "attach container import parent")
	if err != nil {
		return nil, err
	}
	source, err := attachFileResult(attach, lazy.Source, "attach container import source")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	lazy.Source = source
	return []dagql.AnyResult{parent, source}, nil
}

func (lazy *ContainerImportLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "container import parent")
	if err != nil {
		return nil, err
	}
	sourceID, err := encodePersistedObjectRef(cache, lazy.Source, "container import source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedContainerImportLazy{
		ParentResultID: parentID,
		SourceResultID: sourceID,
		Tag:            lazy.Tag,
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
	case "__withImageConfigMetadata",
		"withAnnotation",
		"withoutAnnotation",
		"withWorkdir",
		"withoutWorkdir",
		"withEnvVariable",
		"withEnvFileVariables",
		"__withSystemEnvVariable",
		"withoutEnvVariable",
		"withSecretVariable",
		"withoutSecretVariable",
		"withLabel",
		"withoutLabel",
		"withDockerHealthcheck",
		"withoutDockerHealthcheck",
		"withServiceBinding",
		"withExposedPort",
		"withoutExposedPort",
		"withDefaultTerminalCmd",
		"experimentalWithGPU",
		"experimentalWithAllGPUs":
		var persisted persistedContainerCloneStateLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container cloneState lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container cloneState parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerCloneStateLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
		}
		return nil
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
		return nil
	case "withRootfs":
		var persisted persistedContainerWithRootFSLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withRootfs lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withRootfs parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "container withRootfs source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithRootFSLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Source:    source,
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
			LazyState: NewLazyState(),
			Parent:    parent,
			Path:      persisted.Path,
			Source:    source,
			Filter:    persisted.Filter,
			Owner:     persisted.Owner,
		}
		return nil
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
			LazyState:   NewLazyState(),
			Parent:      parent,
			Path:        persisted.Path,
			Source:      source,
			Permissions: persisted.Permissions,
			Owner:       persisted.Owner,
		}
		return nil
	case "withMountedDirectory":
		var persisted persistedContainerWithMountedDirectoryLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withMountedDirectory lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withMountedDirectory parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "container withMountedDirectory source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithMountedDirectoryLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Source:    source,
			Owner:     persisted.Owner,
			Readonly:  persisted.Readonly,
		}
		return nil
	case "withMountedFile":
		var persisted persistedContainerWithMountedFileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withMountedFile lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withMountedFile parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.SourceResultID, "container withMountedFile source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithMountedFileLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Source:    source,
			Owner:     persisted.Owner,
			Readonly:  persisted.Readonly,
		}
		return nil
	case "withMountedCache":
		var persisted persistedContainerWithMountedCacheLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withMountedCache lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withMountedCache parent")
		if err != nil {
			return err
		}
		cacheVolume, err := loadPersistedObjectResultByResultID[*CacheVolume](ctx, dag, persisted.CacheResultID, "container withMountedCache cache")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithMountedCacheLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Cache:     cacheVolume,
		}
		return nil
	case "withMountedTemp":
		var persisted persistedContainerWithMountedTempLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withMountedTemp lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withMountedTemp parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithMountedTempLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Size:      persisted.Size,
		}
		return nil
	case "withMountedSecret":
		var persisted persistedContainerWithMountedSecretLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withMountedSecret lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withMountedSecret parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*Secret](ctx, dag, persisted.SourceResultID, "container withMountedSecret source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithMountedSecretLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Source:    source,
			Owner:     persisted.Owner,
			Mode:      persisted.Mode,
		}
		return nil
	case "withoutMount":
		var persisted persistedContainerWithoutMountLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withoutMount lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withoutMount parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithoutMountLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
		}
		return nil
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
		return nil
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
		return nil
	case "withUnixSocket":
		var persisted persistedContainerWithUnixSocketLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withUnixSocket lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withUnixSocket parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*Socket](ctx, dag, persisted.SourceResultID, "container withUnixSocket source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithUnixSocketLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
			Source:    source,
			Owner:     persisted.Owner,
		}
		return nil
	case "withoutUnixSocket":
		var persisted persistedContainerWithoutUnixSocketLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container withoutUnixSocket lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container withoutUnixSocket parent")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerWithoutUnixSocketLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Target:    persisted.Target,
		}
		return nil
	case "import":
		var persisted persistedContainerImportLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return fmt.Errorf("decode persisted container import lazy payload: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.ParentResultID, "container import parent")
		if err != nil {
			return err
		}
		source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.SourceResultID, "container import source")
		if err != nil {
			return err
		}
		container.Lazy = &ContainerImportLazy{
			LazyState: NewLazyState(),
			Parent:    parent,
			Source:    source,
			Tag:       persisted.Tag,
		}
		return nil
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
	// Desugar through the canonical server so entrypoint proxies on the
	// outer Query root cannot shadow the core container constructor.
	coreSrv := srv.Canonical()

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("from %s", addr),
		telemetry.Internal(),
	)
	defer telemetry.EndWithCause(span, nil)

	var ctr dagql.ObjectResult[*Container]
	err = coreSrv.Select(ctx, coreSrv.Root(), &ctr,
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

	if container.FS == nil {
		container.FS = new(LazyAccessor[*Directory, *Container])
	}
	rootfsDir := &Directory{
		Platform: container.Platform,
		Services: slices.Clone(container.Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	rootfsDir.Dir.setValue("/")
	rootfsDir.Snapshot.setValue(ref)
	container.FS.setValue(rootfsDir)

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
	dockerfileDir dagql.ObjectResult[*Directory],
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
	dagqlCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := dagqlCache.Evaluate(ctx, dockerfileDir); err != nil {
		return nil, fmt.Errorf("failed to evaluate Dockerfile directory: %w", err)
	}
	dockerfileRef, err := dockerfileDir.Self().Snapshot.GetOrEval(ctx, dockerfileDir.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dockerfile directory snapshot: %w", err)
	}
	if dockerfileRef == nil {
		return nil, fmt.Errorf("failed to load Dockerfile %q: directory is empty", dockerfilePath)
	}
	dockerfileSelector, err := dockerfileDir.Self().Dir.GetOrEval(ctx, dockerfileDir.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dockerfile directory selector: %w", err)
	}
	var dockerfileBytes []byte
	err = MountRef(ctx, dockerfileRef, func(root string, _ *mount.Mount) error {
		resolvedDockerfilePath, err := containerdfs.RootPath(root, path.Join(dockerfileSelector, dockerfilePath))
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithRootFS(ctx context.Context, dir dagql.ObjectResult[*Directory]) (*Container, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, dir); err != nil {
		return nil, err
	}
	detached, err := cloneDetachedDirectoryForContainerResult(ctx, dir.Self())
	if err != nil {
		return nil, err
	}
	if container.FS == nil {
		container.FS = new(LazyAccessor[*Directory, *Container])
	}
	container.FS.setValue(detached)
	container.ImageRef = ""
	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	subdir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, subdir)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", subdir, err)
	}

	// if the path being overwritten is an exact mount point for a file, then we need to unmount it
	// and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && mnt.FileSource != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithDirectory(ctx, parent, subdir, src, filter, owner)
	}

	resolvedOwner := owner
	if owner != "" {
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		resolvedOwner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
	}

	if mnt == nil {
		if container.FS == nil {
			container.FS = new(LazyAccessor[*Directory, *Container])
		}
		if _, ok := container.FS.Peek(); !ok {
			scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
			if err != nil {
				return nil, err
			}
			rootfs := &Directory{
				Platform: container.Platform,
				Services: slices.Clone(container.Services),
				Dir:      new(LazyAccessor[string, *Directory]),
				Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			}
			rootfs.Dir.setValue(scratchDir)
			rootfs.Snapshot.setValue(scratchSnapshot)
			container.FS.setValue(rootfs)
		}
	}

	targetParent, err := targetParentDirectoryForContainerPath(ctx, parent, container, subdir)
	if err != nil {
		return nil, err
	}
	dir, err := bareDirectoryForContainerPath(container, subdir)
	if err != nil {
		return nil, err
	}
	if err := dir.WithDirectory(ctx, targetParent, mntSubpath, src, filter, resolvedOwner, nil); err != nil {
		return nil, err
	}
	container.ImageRef = ""
	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
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
		return container.WithFile(ctx, parent, destPath, src, permissions, owner)
	}

	resolvedOwner := owner
	if owner != "" {
		ownership, err := container.ownership(ctx, parent, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		resolvedOwner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
	}

	if mnt == nil {
		if container.FS == nil {
			container.FS = new(LazyAccessor[*Directory, *Container])
		}
		if _, ok := container.FS.Peek(); !ok {
			scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
			if err != nil {
				return nil, err
			}
			rootfs := &Directory{
				Platform: container.Platform,
				Services: slices.Clone(container.Services),
				Dir:      new(LazyAccessor[string, *Directory]),
				Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			}
			rootfs.Dir.setValue(scratchDir)
			rootfs.Snapshot.setValue(scratchSnapshot)
			container.FS.setValue(rootfs)
		}
	}

	targetParent, err := targetParentDirectoryForContainerPath(ctx, parent, container, destPath)
	if err != nil {
		return nil, err
	}
	dir, err := bareDirectoryForContainerPath(container, destPath)
	if err != nil {
		return nil, err
	}
	if err := dir.WithFile(ctx, targetParent, mntSubpath, src, permissions, resolvedOwner, false, false); err != nil {
		return nil, err
	}
	container.ImageRef = ""
	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutPaths(ctx context.Context, parent dagql.ObjectResult[*Container], destPaths ...string) (*Container, error) {
	for _, destPath := range destPaths {
		var err error
		container, err = container.withoutPath(ctx, parent, destPath)
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
		return container.withoutPath(ctx, parent, destPath)
	}

	if mnt == nil {
		if container.FS == nil {
			return container, nil
		}
		if _, ok := container.FS.Peek(); !ok {
			return container, nil
		}
	}

	targetParent, err := targetParentDirectoryForContainerPath(ctx, parent, container, destPath)
	if err != nil {
		return nil, err
	}
	dir, err := bareDirectoryForContainerPath(container, destPath)
	if err != nil {
		return nil, err
	}
	if err := dir.Without(ctx, targetParent, dagql.CurrentCall(ctx), mntSubpath); err != nil {
		return nil, err
	}
	container.ImageRef = ""
	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFiles(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	destDir string,
	src []dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
	for _, file := range src {
		filePath, err := file.Self().File.GetOrEval(ctx, file.Result)
		if err != nil {
			return nil, fmt.Errorf("failed to get source file path: %w", err)
		}
		destPath := filepath.Join(destDir, filepath.Base(filePath))
		container, err = container.WithFile(ctx, parent, destPath, file, permissions, owner)
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

	return container.WithFile(ctx, parent, dest, newFile, nil, owner)
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithSymlink(ctx context.Context, parent dagql.ObjectResult[*Container], target, linkPath string) (*Container, error) {
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
		return container.WithSymlink(ctx, parent, target, linkPath)
	}

	if mnt == nil {
		if container.FS == nil {
			container.FS = new(LazyAccessor[*Directory, *Container])
		}
		if _, ok := container.FS.Peek(); !ok {
			scratchDir, scratchSnapshot, err := loadCanonicalScratchDirectory(ctx)
			if err != nil {
				return nil, err
			}
			rootfs := &Directory{
				Platform: container.Platform,
				Services: slices.Clone(container.Services),
				Dir:      new(LazyAccessor[string, *Directory]),
				Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			}
			rootfs.Dir.setValue(scratchDir)
			rootfs.Snapshot.setValue(scratchSnapshot)
			container.FS.setValue(rootfs)
		}
	}

	targetParent, err := targetParentDirectoryForContainerPath(ctx, parent, container, linkPath)
	if err != nil {
		return nil, err
	}
	dir, err := bareDirectoryForContainerPath(container, linkPath)
	if err != nil {
		return nil, err
	}
	if err := dir.WithSymlink(ctx, targetParent, target, mntSubpath); err != nil {
		return nil, err
	}
	container.ImageRef = ""
	return container, nil
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

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, dir); err != nil {
		return nil, err
	}
	detached, err := cloneDetachedDirectoryForContainerResult(ctx, dir.Self())
	if err != nil {
		return nil, err
	}
	source := new(LazyAccessor[*Directory, *Container])
	source.setValue(detached)

	container.Mounts = container.Mounts.With(ContainerMount{
		DirectorySource: source,
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

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, file); err != nil {
		return nil, err
	}
	detached, err := cloneDetachedFileForContainerResult(ctx, file.Self())
	if err != nil {
		return nil, err
	}
	source := new(LazyAccessor[*File, *Container])
	source.setValue(detached)

	container.Mounts = container.Mounts.With(ContainerMount{
		FileSource: source,
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
		var rootfs dagql.ObjectResult[*Directory]
		err := srv.Select(ctx, parent, &rootfs, dagql.Selector{Field: "rootfs"})
		if err != nil {
			return false, err
		}
		err = srv.Select(ctx, rootfs, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.DirectorySource != nil: // directory mount
		var parentDir dagql.ObjectResult[*Directory]
		err := srv.Select(ctx, parent, &parentDir, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mnt.Target)},
			},
		})
		if err != nil {
			return false, err
		}
		err = srv.Select(ctx, parentDir, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.FileSource != nil: // file mount
		if mntSubpath != "" && mntSubpath != "." {
			return false, nil
		}
		var fileRes dagql.ObjectResult[*File]
		if err := srv.Select(ctx, parent, &fileRes, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mnt.Target)},
			},
		}); err != nil {
			return false, err
		}
		var stat *Stat
		if err := srv.Select(ctx, fileRes, &stat, dagql.Selector{Field: "stat"}); err != nil {
			return false, err
		}
		if stat == nil {
			return false, nil
		}
		if targetType == "" {
			return true, nil
		}
		return targetType == ExistsTypeRegular && stat.FileType == FileTypeRegular, nil

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
		var rootfs dagql.ObjectResult[*Directory]
		err := srv.Select(ctx, parent, &rootfs, dagql.Selector{Field: "rootfs"})
		if err != nil {
			return nil, err
		}
		err = srv.Select(ctx, rootfs, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.DirectorySource != nil: // directory mount
		var parentDir dagql.ObjectResult[*Directory]
		err := srv.Select(ctx, parent, &parentDir, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mnt.Target)},
			},
		})
		if err != nil {
			return nil, err
		}
		err = srv.Select(ctx, parentDir, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.FileSource != nil: // file mount
		if mntSubpath != "" && mntSubpath != "." {
			return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
		}
		var fileRes dagql.ObjectResult[*File]
		if err := srv.Select(ctx, parent, &fileRes, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mnt.Target)},
			},
		}); err != nil {
			return nil, err
		}
		err = srv.Select(ctx, fileRes, &stat, dagql.Selector{
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
	bk, err := query.Engine(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get engine client: %w", err)
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
		rootFS, ok := c.FS.Peek()
		if !ok || rootFS == nil {
			continue
		}
		l = append(l, c)
	}
	return l
}

func getVariantRefs(ctx context.Context, variants []*Container) (map[string]engineutil.ContainerExport, error) {
	inputByPlatform := map[string]engineutil.ContainerExport{}
	var eg errgroup.Group
	var mu sync.Mutex
	for _, variant := range variants {
		platformString := variant.Platform.Format()
		if _, ok := inputByPlatform[platformString]; ok {
			return nil, fmt.Errorf("duplicate platform %q", platformString)
		}

		variant := variant
		platformKey := platformString
		eg.Go(func() error {
			if err := variant.Evaluate(ctx); err != nil {
				return err
			}
			if variant.FS == nil {
				return nil
			}
			rootFS, ok := variant.FS.Peek()
			if !ok || rootFS == nil {
				return nil
			}
			fsRef, ok := rootFS.Snapshot.Peek()
			if !ok || fsRef == nil {
				return fmt.Errorf("get variant rootfs snapshot for platform %s: unset snapshot", platformKey)
			}

			mu.Lock()
			defer mu.Unlock()

			inputByPlatform[platformKey] = engineutil.ContainerExport{
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
	if opts.Tar {
		tarball, err := container.AsTarball(
			ctx,
			opts.PlatformVariants,
			opts.ForcedCompression,
			opts.MediaTypes,
			"container.tar",
		)
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = tarball.OnRelease(context.WithoutCancel(ctx))
		}()
		filePath, ok := tarball.File.Peek()
		if !ok {
			return nil, fmt.Errorf("container export tarball file path: unset")
		}
		snapshot, ok := tarball.Snapshot.Peek()
		if !ok {
			return nil, fmt.Errorf("container export tarball snapshot: unset")
		}
		if err := ExportFile(ctx, snapshot, filePath, opts.Dest, false); err != nil {
			return nil, err
		}
		return nil, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get engine client: %w", err)
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
	return fileSource.Self().Open(ctx, fileSource)
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

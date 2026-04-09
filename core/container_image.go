package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/transfer/archive"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/dagql"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type LoadedImportedImage struct {
	Image  bkcache.ImportedImage
	Config dockerspec.DockerOCIImage
}

type ContainerFromImageRefLazy struct {
	LazyState
	CanonicalRef string
	ResolveMode  serverresolver.ResolveMode
}

type dockerfileImageMetaResolver struct {
	resolver *serverresolver.Resolver
}

func (r dockerfileImageMetaResolver) ResolveImageConfig(
	ctx context.Context,
	ref string,
	opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	resolveMode := serverresolver.ResolveModeDefault
	if opt.ImageOpt != nil {
		switch opt.ImageOpt.ResolveMode {
		case "", pb.AttrImageResolveModeDefault:
			resolveMode = serverresolver.ResolveModeDefault
		case pb.AttrImageResolveModeForcePull:
			resolveMode = serverresolver.ResolveModeForcePull
		default:
			return "", "", nil, fmt.Errorf("unsupported image resolve mode %q", opt.ImageOpt.ResolveMode)
		}
	}

	if r.resolver == nil {
		return "", "", nil, fmt.Errorf("registry resolver is nil")
	}
	return r.resolver.ResolveImageConfig(ctx, ref, serverresolver.ResolveImageConfigOpts{
		Platform:    opt.Platform,
		ResolveMode: resolveMode,
	})
}

func (lazy *ContainerFromImageRefLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Container.from", func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		rslvr, err := query.RegistryResolver(ctx)
		if err != nil {
			return err
		}
		pulled, err := rslvr.Pull(ctx, lazy.CanonicalRef, serverresolver.PullOpts{
			Platform:    container.Platform.Spec(),
			ResolveMode: lazy.ResolveMode,
		})
		if err != nil {
			return fmt.Errorf("pull image %q: %w", lazy.CanonicalRef, err)
		}
		defer pulled.Release(context.WithoutCancel(ctx))

		rootfs, err := query.SnapshotManager().ImportImage(ctx, &bkcache.ImportedImage{
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
			return fmt.Errorf("import image %q: %w", lazy.CanonicalRef, err)
		}
		rootfsDir := &Directory{
			Platform: container.Platform,
			Services: slices.Clone(container.Services),
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		}
		rootfsDir.Dir.setValue("/")
		rootfsDir.Snapshot.setValue(rootfs)
		if container.FS == nil {
			container.FS = new(LazyAccessor[*Directory, *Container])
		}
		container.FS.setValue(rootfsDir)
		container.Lazy = nil
		return nil
	})
}

func (*ContainerFromImageRefLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (lazy *ContainerFromImageRefLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return json.Marshal(persistedContainerFromLazy{CanonicalRef: lazy.CanonicalRef})
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

	var release func(context.Context) error
	defer func() {
		if release != nil {
			release(context.WithoutCancel(ctx))
		}
	}()

	ctx, release, err = leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
	if err != nil {
		return nil, err
	}

	stream := archive.NewImageImportStream(tarball, "")
	desc, err := stream.Import(ctx, query.OCIStore())
	if err != nil {
		return nil, fmt.Errorf("image archive import: %w", err)
	}

	manifestDesc, err := resolveIndex(ctx, query.OCIStore(), desc, container.Platform.Spec(), tag)
	if err != nil {
		return nil, fmt.Errorf("resolve imported image manifest: %w", err)
	}

	return container.FromOCIStore(ctx, *manifestDesc, tag)
}

func (container *Container) FromOCIStore(
	ctx context.Context,
	manifestDesc specs.Descriptor,
	imageRef string,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	loaded, err := loadImportedImageFromStore(ctx, query.OCIStore(), manifestDesc, imageRef)
	if err != nil {
		return nil, err
	}

	rootfs, err := query.SnapshotManager().ImportImage(ctx, &loaded.Image, bkcache.ImportImageOpts{
		ImageRef:   imageRef,
		RecordType: bkclient.UsageRecordTypeRegular,
	})
	if err != nil {
		return nil, fmt.Errorf("import image rootfs %q: %w", imageRef, err)
	}

	rootPlatform := container.Platform
	if loaded.Config.Platform.OS != "" {
		rootPlatform = Platform(platforms.Normalize(loaded.Config.Platform))
		container.Platform = rootPlatform
	}
	container.Config = loaded.Config.Config
	container.ImageRef = imageRef

	rootfsDir := &Directory{
		Platform: rootPlatform,
		Services: slices.Clone(container.Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	rootfsDir.Dir.setValue("/")
	rootfsDir.Snapshot.setValue(rootfs)
	if container.FS == nil {
		container.FS = new(LazyAccessor[*Directory, *Container])
	}
	container.FS.setValue(rootfsDir)
	return container, nil
}

func (container *Container) FromInternal(
	ctx context.Context,
	desc specs.Descriptor,
) (*Container, error) {
	return container.FromOCIStore(ctx, desc, "")
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

	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get engine client: %w", err)
	}
	if mediaTypes == "" {
		mediaTypes = OCIMediaTypes
	}

	variants := filterEmptyContainers(append([]*Container{container}, platformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return nil, err
	}

	bkref, err := query.SnapshotManager().New(ctx, nil,
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

	err = MountRef(ctx, bkref, func(out string, _ *mount.Mount) error {
		file, err := os.OpenFile(filepath.Join(out, filePath), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		return bk.WriteContainerImageTarball(ctx, file, inputByPlatform, useOCIMediaTypes(mediaTypes), string(forcedCompression))
	})
	if err != nil {
		return nil, fmt.Errorf("container image to tarball file conversion failed: %w", err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	f = &File{
		Platform: query.Platform(),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	f.File.setValue(filePath)
	f.Snapshot.setValue(snap)
	return f, nil
}

func loadImportedImageFromStore(
	ctx context.Context,
	store content.Store,
	manifestDesc specs.Descriptor,
	imageRef string,
) (*LoadedImportedImage, error) {
	manifestBlob, err := content.ReadBlob(ctx, store, manifestDesc)
	if err != nil {
		return nil, fmt.Errorf("read manifest blob %s: %w", manifestDesc.Digest, err)
	}

	var manifest specs.Manifest
	if err := json.Unmarshal(manifestBlob, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest %s: %w", manifestDesc.Digest, err)
	}

	configDesc, err := hydrateImportedDescriptor(ctx, store, manifest.Config)
	if err != nil {
		return nil, err
	}

	configBlob, err := content.ReadBlob(ctx, store, configDesc)
	if err != nil {
		return nil, fmt.Errorf("read image config blob %s: %w", configDesc.Digest, err)
	}

	var imgSpec dockerspec.DockerOCIImage
	if err := json.Unmarshal(configBlob, &imgSpec); err != nil {
		return nil, fmt.Errorf("unmarshal image config %s: %w", configDesc.Digest, err)
	}

	if len(manifest.Layers) != len(imgSpec.RootFS.DiffIDs) {
		return nil, fmt.Errorf(
			"mismatched image rootfs and manifest layers: %d diffIDs vs %d layers",
			len(imgSpec.RootFS.DiffIDs),
			len(manifest.Layers),
		)
	}

	layers := make([]specs.Descriptor, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		hydrated, err := hydrateImportedDescriptor(ctx, store, layer)
		if err != nil {
			return nil, err
		}
		if hydrated.Annotations == nil {
			hydrated.Annotations = map[string]string{}
		}
		hydrated.Annotations[labels.LabelUncompressed] = imgSpec.RootFS.DiffIDs[i].String()
		layers[i] = hydrated
	}

	return &LoadedImportedImage{
		Image: bkcache.ImportedImage{
			Ref:          imageRef,
			ManifestDesc: manifestDesc,
			ConfigDesc:   configDesc,
			Layers:       layers,
		},
		Config: imgSpec,
	}, nil
}

func hydrateImportedDescriptor(
	ctx context.Context,
	store content.Store,
	desc specs.Descriptor,
) (specs.Descriptor, error) {
	if desc.Digest == "" {
		return desc, nil
	}
	info, err := store.Info(ctx, desc.Digest)
	if err != nil {
		return specs.Descriptor{}, fmt.Errorf("stat descriptor %s: %w", desc.Digest, err)
	}
	hydrated := desc
	if hydrated.Size == 0 {
		hydrated.Size = info.Size
	}
	return hydrated, nil
}

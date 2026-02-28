package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/core/containersource"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func BuiltInContainer(ctx context.Context, platform Platform, blobDigest string) (*Container, error) {
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

	refStr := fmt.Sprintf("dagger/import@%s", blobDigest)
	attrs := map[string]string{
		pb.AttrOCILayoutStoreID: buildkit.BuiltinContentOCIStoreName,
	}
	id, err := hsm.Identifier(refStr, attrs, &pb.Platform{
		Architecture: platform.Architecture,
		OS:           platform.OS,
		Variant:      platform.Variant,
		OSVersion:    platform.OSVersion,
		OSFeatures:   platform.OSFeatures,
	})
	if err != nil {
		return nil, err
	}

	src, err := hsm.Resolve(ctx, id, query.BuildkitSession())
	if err != nil {
		return nil, err
	}

	bkSessionGroup := buildkit.NewSessionGroup(bk.ID())
	ref, err := src.Snapshot(ctx, bkSessionGroup)
	if err != nil {
		return nil, err
	}
	container := NewContainer(platform)
	rootfsDir := &Directory{
		Dir:       "/",
		Platform:  platform,
		LazyState: NewLazyState(),
		Snapshot:  ref,
	}
	updatedRootFS, err := UpdatedRootFS(ctx, container, rootfsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to update rootfs: %w", err)
	}
	container.FS = updatedRootFS
	return container, nil
}

func BuiltInContainerUpdateConfig(ctx context.Context, ctr *Container, blobDigest string) error {
	goSDKContentStore, err := local.NewStore(distconsts.EngineContainerBuiltinContentDir)
	if err != nil {
		return fmt.Errorf("failed to create go sdk content store: %w", err)
	}

	manifestBlob, err := content.ReadBlob(ctx, goSDKContentStore, specs.Descriptor{
		Digest: digest.Digest(blobDigest),
	})
	if err != nil {
		return fmt.Errorf("image archive read manifest blob: %w", err)
	}

	var man specs.Manifest
	err = json.Unmarshal(manifestBlob, &man)
	if err != nil {
		return fmt.Errorf("image archive unmarshal manifest: %w", err)
	}

	configBlob, err := content.ReadBlob(ctx, goSDKContentStore, man.Config)
	if err != nil {
		return fmt.Errorf("image archive read image config blob %s: %w", man.Config.Digest, err)
	}

	var imgSpec dockerspec.DockerOCIImage
	err = json.Unmarshal(configBlob, &imgSpec)
	if err != nil {
		return fmt.Errorf("load image config: %w", err)
	}
	ctr.Config = imgSpec.Config
	return nil
}

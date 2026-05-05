package engineutil

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	archiveexporter "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/platforms"
	imageexport "github.com/dagger/dagger/engine/engineutil/imageexport"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	cacheconfig "github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/util/containerutil"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type ContainerExport struct {
	Ref         bkcache.ImmutableRef
	Config      dockerspec.DockerOCIImageConfig
	Annotations []containerutil.ContainerAnnotation
}

func (c *Client) buildExportRequest(
	inputByPlatform map[string]ContainerExport,
) (*imageexport.ExportRequest, error) {
	inputs := make([]imageexport.PlatformExportInput, 0, len(inputByPlatform))
	for platformKey, input := range inputByPlatform {
		platform, err := platforms.Parse(platformKey)
		if err != nil {
			return nil, err
		}

		manifestAnnotations := map[string]string{}
		manifestDescriptorAnnotations := map[string]string{}
		for _, annotation := range input.Annotations {
			manifestAnnotations[annotation.Key] = annotation.Value
			manifestDescriptorAnnotations[annotation.Key] = annotation.Value
		}

		inputs = append(inputs, imageexport.PlatformExportInput{
			Key:      platformKey,
			Platform: platform,
			Ref:      input.Ref,
			Config: dockerspec.DockerOCIImage{
				Image: specs.Image{
					Platform: specs.Platform{
						Architecture: platform.Architecture,
						OS:           platform.OS,
						OSVersion:    platform.OSVersion,
						OSFeatures:   platform.OSFeatures,
						Variant:      platform.Variant,
					},
				},
				Config: input.Config,
			},
			ManifestAnnotations:           manifestAnnotations,
			ManifestDescriptorAnnotations: manifestDescriptorAnnotations,
		})
	}

	slices.SortFunc(inputs, func(a, b imageexport.PlatformExportInput) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return &imageexport.ExportRequest{Platforms: inputs}, nil
}

func (c *Client) exportCommitOpts(
	forceCompression string,
	useOCIMediaTypes bool,
) (imageexport.CommitOpts, error) {
	refCfg := cacheconfig.RefConfig{
		Compression: compression.New(compression.Default),
	}
	if forceCompression != "" {
		switch forceCompression {
		case "Gzip":
			forceCompression = "gzip"
		case "Zstd":
			forceCompression = "zstd"
		case "EStarGZ":
			forceCompression = "estargz"
		case "Uncompressed":
			forceCompression = "uncompressed"
		}
		ctype, err := compression.Parse(forceCompression)
		if err != nil {
			return imageexport.CommitOpts{}, err
		}
		refCfg.Compression = compression.New(ctype).SetForce(true)
	}
	return imageexport.CommitOpts{
		RefCfg:   refCfg,
		OCITypes: useOCIMediaTypes,
	}, nil
}

func (c *Client) assembleExportedImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	useOCIMediaTypes bool,
	forceCompression string,
) (*imageexport.ExportedImage, error) {
	req, err := c.buildExportRequest(inputByPlatform)
	if err != nil {
		return nil, err
	}
	commitOpts, err := c.exportCommitOpts(forceCompression, useOCIMediaTypes)
	if err != nil {
		return nil, err
	}
	return c.imageExportWriter.Assemble(ctx, req, commitOpts)
}

func (c *Client) WriteContainerImageTarball(
	ctx context.Context,
	w io.Writer,
	inputByPlatform map[string]ContainerExport,
	useOCIMediaTypes bool,
	forceCompression string,
) error {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("write container image tarball done"))

	exported, err := c.assembleExportedImage(ctx, inputByPlatform, useOCIMediaTypes, forceCompression)
	if err != nil {
		return err
	}
	return archiveexporter.Export(ctx, exported.Provider, w, archiveexporter.WithManifest(exported.RootDesc))
}

type resolverPusher struct {
	resolver *serverresolver.Resolver
}

func (p resolverPusher) PushImage(
	ctx context.Context,
	img *imageexport.ExportedImage,
	ref string,
	opts imageexport.PushOpts,
) error {
	return p.resolver.PushImage(ctx, &serverresolver.PushedImage{
		RootDesc:          img.RootDesc,
		Provider:          img.Provider,
		SourceAnnotations: img.SourceAnnotations,
	}, ref, serverresolver.PushOpts{
		Insecure: opts.Insecure,
		ByDigest: opts.PushByDigest,
	})
}

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	refName string,
	useOCIMediaTypes bool,
	forceCompression string,
) (*imageexport.ExportResponse, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("publish container image done"))

	req, err := c.buildExportRequest(inputByPlatform)
	if err != nil {
		return nil, err
	}
	commitOpts, err := c.exportCommitOpts(forceCompression, useOCIMediaTypes)
	if err != nil {
		return nil, err
	}
	resolver, err := c.GetRegistryResolver(ctx)
	if err != nil {
		return nil, err
	}
	return imageexport.Export(ctx, imageexport.Deps{
		Writer: c.imageExportWriter,
		Pusher: resolverPusher{resolver: resolver},
	}, req, imageexport.ExportOpts{
		Names:  []string{refName},
		Push:   true,
		Commit: commitOpts,
	})
}

func (c *Client) ExportContainerImage(
	ctx context.Context,
	destPath string,
	inputByPlatform map[string]ContainerExport,
	forceCompression string,
	tarExport bool,
	_ string,
	useOCIMediaTypes bool,
) (*imageexport.ExportResponse, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("export container image done"))

	imageWriter, err := c.WriteImage(ctx, destPath)
	if err != nil {
		return nil, err
	}

	req, err := c.buildExportRequest(inputByPlatform)
	if err != nil {
		return nil, err
	}
	commitOpts, err := c.exportCommitOpts(forceCompression, useOCIMediaTypes)
	if err != nil {
		return nil, err
	}

	if imageWriter.ContentStore != nil {
		return imageexport.Export(ctx, imageexport.Deps{
			Images:       imageWriter.ImagesStore,
			ContentStore: imageWriter.ContentStore,
			LeaseManager: imageWriter.LeaseManager,
			Writer:       c.imageExportWriter,
		}, req, imageexport.ExportOpts{
			Names:  []string{destPath},
			Store:  true,
			Commit: commitOpts,
		})
	}

	if imageWriter.Tarball != nil {
		defer imageWriter.Tarball.Close()

		exported, err := c.imageExportWriter.Assemble(ctx, req, commitOpts)
		if err != nil {
			return nil, err
		}
		if err := archiveexporter.Export(ctx, exported.Provider, imageWriter.Tarball, archiveexporter.WithManifest(exported.RootDesc)); err != nil {
			return nil, err
		}
		return &imageexport.ExportResponse{
			RootDesc:  exported.RootDesc,
			Platforms: exported.Platforms,
		}, nil
	}

	return nil, fmt.Errorf("client has no supported api for loading image")
}

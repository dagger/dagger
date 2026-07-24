package engineutil

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/containerd/containerd/v2/core/content"
	archiveexporter "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/platforms"
	imageexport "github.com/dagger/dagger/engine/engineutil/imageexport"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	cacheconfig "github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/util/containerutil"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type ContainerExport struct {
	Ref         bkcache.ImmutableRef
	Config      dockerspec.DockerOCIImageConfig
	Annotations []containerutil.ContainerAnnotation
}

type PreparedContainerImage struct {
	manifest ContainerImageBlob
	blobs    map[digest.Digest]ContainerImageBlob
}

type ContainerImageBlob struct {
	descriptor specs.Descriptor
	provider   content.Provider
	contents   []byte
}

func (blob ContainerImageBlob) Descriptor() specs.Descriptor {
	return blob.descriptor
}

func (blob ContainerImageBlob) WriteTo(ctx context.Context, dst io.Writer) error {
	if blob.contents != nil {
		_, err := io.Copy(dst, bytes.NewReader(blob.contents))
		return err
	}

	reader, err := content.BlobReadSeeker(ctx, blob.provider, blob.descriptor)
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = io.Copy(dst, reader)
	return err
}

func (image *PreparedContainerImage) Manifest() ContainerImageBlob {
	return image.manifest
}

func (image *PreparedContainerImage) Blob(id string) (ContainerImageBlob, error) {
	dgst := digest.Digest(id)
	if err := dgst.Validate(); err != nil {
		return ContainerImageBlob{}, fmt.Errorf("invalid image blob digest %q: %w", id, err)
	}

	blob, ok := image.blobs[dgst]
	if !ok {
		return ContainerImageBlob{}, fmt.Errorf("image blob %q not found in manifest", id)
	}
	return blob, nil
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
	network  serverresolver.NetworkConfig
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
		RegistryTransport: opts.RegistryTransport,
		ByDigest:          opts.PushByDigest,
		Network:           p.network,
	})
}

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	refName string,
	useOCIMediaTypes bool,
	forceCompression string,
	network serverresolver.NetworkConfig,
	registryTransport serverresolver.RegistryTransport,
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
		Pusher: resolverPusher{
			resolver: resolver,
			network:  network,
		},
	}, req, imageexport.ExportOpts{
		Names:             []string{refName},
		Push:              true,
		RegistryTransport: registryTransport,
		Commit:            commitOpts,
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

func (c *Client) PrepareContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	useOCIMediaTypes bool,
	forceCompression string,
) (*PreparedContainerImage, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("prepare container image done"))

	exported, err := c.assembleExportedImage(ctx, inputByPlatform, useOCIMediaTypes, forceCompression)
	if err != nil {
		return nil, err
	}

	manifestBlob, err := content.ReadBlob(ctx, exported.Provider, exported.RootDesc)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var manifest struct {
		Layers []specs.Descriptor `json:"layers"`
		Config specs.Descriptor   `json:"config"`
	}
	if err := json.Unmarshal(manifestBlob, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	configBlob, err := content.ReadBlob(ctx, exported.Provider, manifest.Config)
	if err != nil {
		return nil, fmt.Errorf("read image config: %w", err)
	}

	prepared := &PreparedContainerImage{
		manifest: ContainerImageBlob{
			descriptor: exported.RootDesc,
			contents:   manifestBlob,
		},
		blobs: make(map[digest.Digest]ContainerImageBlob, len(manifest.Layers)+1),
	}
	for _, desc := range manifest.Layers {
		prepared.blobs[desc.Digest] = ContainerImageBlob{
			descriptor: desc,
			provider:   exported.Provider,
		}
	}
	prepared.blobs[manifest.Config.Digest] = ContainerImageBlob{
		descriptor: manifest.Config,
		contents:   configBlob,
	}
	return prepared, nil
}

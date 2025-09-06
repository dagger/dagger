package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/containerd/platforms"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine"
	ociexporter "github.com/dagger/dagger/engine/buildkit/exporter/oci"
)

type ContainerExport struct {
	Definition *bksolverpb.Definition
	Config     specs.ImageConfig
}

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("publish container image done"))

	combinedResult, err := c.getContainerResult(ctx, inputByPlatform)
	if err != nil {
		return nil, err
	}

	// TODO: lift this to dagger
	exporter, err := c.Worker.Exporter(bkclient.ExporterImage, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expResult, err := exporter.Resolve(ctx, 0, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %w", err)
	}

	resp, descRef, err := expResult.Export(ctx, combinedResult, nil, c.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %w", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

func (c *Client) ExportContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	destPath string,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("export container image done"))

	destPath = path.Clean(destPath)

	combinedResult, err := c.getContainerResult(ctx, inputByPlatform)
	if err != nil {
		return nil, err
	}

	variant := ociexporter.VariantDocker
	if len(combinedResult.Refs) > 1 {
		variant = ociexporter.VariantOCI
	}

	exporter, err := ociexporter.New(ociexporter.Opt{
		SessionManager: c.SessionManager,
		ImageWriter:    c.Worker.ImageWriter,
		Variant:        variant,
		LeaseManager:   c.Worker.LeaseManager(),
	})
	if err != nil {
		return nil, err
	}

	expResult, err := exporter.Resolve(ctx, 0, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester session ID from client metadata: %w", err)
	}

	ctx = engine.LocalExportOpts{
		Path:         destPath,
		IsFileStream: true,
	}.AppendToOutgoingContext(ctx)

	resp, descRef, err := expResult.Export(ctx, combinedResult, nil, clientMetadata.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to export: %w", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

func (c *Client) ContainerImageToTarball(
	ctx context.Context,
	engineHostPlatform specs.Platform,
	destPath string,
	inputByPlatform map[string]ContainerExport,
	opts map[string]string,
) error {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("container image to tarball done"))

	combinedResult, err := c.getContainerResult(ctx, inputByPlatform)
	if err != nil {
		return err
	}

	variant := ociexporter.VariantDocker
	if len(combinedResult.Refs) > 1 {
		variant = ociexporter.VariantOCI
	}

	exporter, err := ociexporter.New(ociexporter.Opt{
		SessionManager: c.SessionManager,
		ImageWriter:    c.Worker.ImageWriter,
		Variant:        variant,
		LeaseManager:   c.Worker.LeaseManager(),
	})
	if err != nil {
		return err
	}

	expResult, err := exporter.Resolve(ctx, 0, opts)
	if err != nil {
		return fmt.Errorf("failed to resolve exporter: %w", err)
	}

	ctx = engine.LocalExportOpts{
		Path:         destPath,
		IsFileStream: true,
	}.AppendToOutgoingContext(ctx)

	_, descRef, err := expResult.Export(ctx, combinedResult, nil, c.ID())
	if err != nil {
		return fmt.Errorf("failed to export: %w", err)
	}
	if descRef != nil {
		defer descRef.Release()
	}
	return nil
}

func (c *Client) getContainerResult(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
) (*solverresult.Result[bkcache.ImmutableRef], error) {
	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(inputByPlatform)),
	}
	// TODO: probably faster to do this in parallel for each platform
	for platformString, input := range inputByPlatform {
		res, err := c.Solve(ctx, bkgw.SolveRequest{
			Definition: input.Definition,
			Evaluate:   true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to solve for container publish: %w", err)
		}
		cacheRes, err := ConvertToWorkerCacheResult(ctx, res)
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %w", err)
		}
		ref, err := cacheRes.SingleRef()
		if err != nil {
			return nil, err
		}

		platform, err := platforms.Parse(platformString)
		if err != nil {
			return nil, err
		}
		cfgBytes, err := json.Marshal(specs.Image{
			Platform: specs.Platform{
				Architecture: platform.Architecture,
				OS:           platform.OS,
				OSVersion:    platform.OSVersion,
				OSFeatures:   platform.OSFeatures,
			},
			Config: input.Config,
		})
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			expPlatforms.Platforms[len(combinedResult.Refs)] = exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			}
			combinedResult.AddRef(platformString, ref)
		}
	}

	if len(combinedResult.Refs) > 1 {
		platformBytes, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)
	}

	return combinedResult, nil
}

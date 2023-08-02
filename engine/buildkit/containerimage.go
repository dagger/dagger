package buildkit

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/containerd/containerd/platforms"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
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
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	combinedResult, err := c.getContainerResult(ctx, inputByPlatform)
	if err != nil {
		return nil, err
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterImage, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, c.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
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
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	destPath = path.Clean(destPath)
	if destPath == ".." || strings.HasPrefix(destPath, "../") {
		return nil, fmt.Errorf("path %q escapes workdir; use an absolute path instead", destPath)
	}

	combinedResult, err := c.getContainerResult(ctx, inputByPlatform)
	if err != nil {
		return nil, err
	}

	exporterName := bkclient.ExporterDocker
	if len(combinedResult.Refs) > 1 {
		exporterName = bkclient.ExporterOCI
	}

	exporter, err := c.Worker.Exporter(exporterName, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
	sess, err := c.newFileSendServerProxySession(ctx, destPath)
	if err != nil {
		return nil, err
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, sess.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
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
			return nil, fmt.Errorf("failed to solve for container publish: %s", err)
		}
		cacheRes, err := ConvertToWorkerCacheResult(ctx, res)
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %s", err)
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
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
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

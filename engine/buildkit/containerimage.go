package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/platforms"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/exporter/containerimage/exptypes"
	solverresult "github.com/dagger/dagger/internal/buildkit/solver/result"
	"github.com/dagger/dagger/util/containerutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine"
	ociexporter "github.com/dagger/dagger/engine/buildkit/exporter/oci"
)

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]ContainerExport,
	refName string, // the destination image name
	useOCIMediaTypes bool,
	forceCompression string,
) (map[string]string, error) {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("publish container image done"))

	combinedResult, err := c.combineContainerRefs(ctx, inputByPlatform)
	if err != nil {
		return nil, err
	}

	// TODO: lift this to dagger
	exporter, err := c.Worker.Exporter(bkclient.ExporterImage, c.SessionManager)
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		string(exptypes.OptKeyName):     refName,
		string(exptypes.OptKeyPush):     strconv.FormatBool(true),
		string(exptypes.OptKeyOCITypes): strconv.FormatBool(useOCIMediaTypes),
	}
	if forceCompression != "" {
		opts[string(exptypes.OptKeyLayerCompression)] = strings.ToLower(forceCompression)
		opts[string(exptypes.OptKeyForceCompression)] = strconv.FormatBool(true)
	}

	err = addAnnotations(inputByPlatform, opts)
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

type ContainerExport struct {
	Ref         bkcache.ImmutableRef
	Config      specs.ImageConfig
	Annotations []containerutil.ContainerAnnotation
}

func addAnnotations(inputByPlatform map[string]ContainerExport, opts map[string]string) error {
	singlePlatform := len(inputByPlatform) == 1
	for plat, variant := range inputByPlatform {
		if singlePlatform {
			for _, annotation := range variant.Annotations {
				opts[exptypes.AnnotationManifestKey(nil, annotation.Key)] = annotation.Value
				opts[exptypes.AnnotationManifestDescriptorKey(nil, annotation.Key)] = annotation.Value
			}
		} else {
			platformSpec, err := platforms.Parse(plat)
			if err != nil {
				return err
			}
			// multi platform case
			for _, annotation := range variant.Annotations {
				opts[exptypes.AnnotationManifestKey(&platformSpec, annotation.Key)] = annotation.Value
				opts[exptypes.AnnotationManifestDescriptorKey(&platformSpec, annotation.Key)] = annotation.Value
			}
		}
	}
	return nil
}

func (c *Client) ExportContainerImage(
	ctx context.Context,
	destPath string,
	inputByPlatform map[string]ContainerExport,
	forceCompression string,
	tarExport bool,
	leaseID string, // only required when tarExport is false
	useOCIMediaTypes bool,
) (map[string]string, error) {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel(errors.New("export container image done"))

	destPath = path.Clean(destPath)

	combinedResult, err := c.combineContainerRefs(ctx, inputByPlatform)
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

	opts := map[string]string{
		string(exptypes.OptKeyOCITypes): strconv.FormatBool(useOCIMediaTypes),
	}

	opts["tar"] = strconv.FormatBool(tarExport)
	if !tarExport {
		if leaseID == "" {
			return nil, fmt.Errorf("missing leaseID")
		}
		opts["store"] = "export"
		opts["lease"] = leaseID
	}

	if forceCompression != "" {
		opts[string(exptypes.OptKeyLayerCompression)] = strings.ToLower(forceCompression)
		opts[string(exptypes.OptKeyForceCompression)] = strconv.FormatBool(true)
	}

	err = addAnnotations(inputByPlatform, opts)
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
	useOCIMediaTypes bool,
	forceCompression string,
) error {
	ctx = buildkitTelemetryProvider(ctx)
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel(errors.New("container image to tarball done"))

	combinedResult, err := c.combineContainerRefs(ctx, inputByPlatform)
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

	opts := map[string]string{
		"tar":                           strconv.FormatBool(true),
		string(exptypes.OptKeyOCITypes): strconv.FormatBool(useOCIMediaTypes),
	}
	if forceCompression != "" {
		opts[string(exptypes.OptKeyLayerCompression)] = strings.ToLower(forceCompression)
		opts[string(exptypes.OptKeyForceCompression)] = strconv.FormatBool(true)
	}

	err = addAnnotations(inputByPlatform, opts)
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

func (c *Client) combineContainerRefs(
	_ context.Context,
	inputByPlatform map[string]ContainerExport,
) (*solverresult.Result[bkcache.ImmutableRef], error) {
	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(inputByPlatform)),
	}
	for platformString, input := range inputByPlatform {
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
			combinedResult.SetRef(input.Ref)
		} else {
			expPlatforms.Platforms[len(combinedResult.Refs)] = exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			}
			combinedResult.AddRef(platformString, input.Ref)
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

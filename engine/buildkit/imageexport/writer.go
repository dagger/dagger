package imageexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/platforms"
	cache "github.com/dagger/dagger/engine/snapshots"
	cacheconfig "github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/converter"
	"github.com/dagger/dagger/internal/buildkit/util/progress"
	"github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type PlatformExportInput struct {
	Key      string
	Platform ocispecs.Platform
	Ref      cache.ImmutableRef

	Config    dockerspec.DockerOCIImage
	BaseImage *dockerspec.DockerOCIImage

	ManifestAnnotations           map[string]string
	ManifestDescriptorAnnotations map[string]string
}

type ExportRequest struct {
	Platforms []PlatformExportInput

	IndexAnnotations           map[string]string
	IndexDescriptorAnnotations map[string]string
	InlineCache                map[string][]byte
}

type WriterOpt struct {
	Snapshotter  snapshot.Snapshotter
	ContentStore content.Store
	Applier      diff.Applier
	Differ       diff.Comparer
}

type Writer struct {
	opt WriterOpt
}

func NewWriter(opt WriterOpt) (*Writer, error) {
	return &Writer{opt: opt}, nil
}

type ExportedImage struct {
	RootDesc ocispecs.Descriptor

	Platforms []ExportedPlatform
	Provider  content.InfoReaderProvider

	SourceAnnotations map[digest.Digest]map[string]string
}

type ExportedPlatform struct {
	Key          string
	Platform     ocispecs.Platform
	ManifestDesc ocispecs.Descriptor
	ConfigDesc   ocispecs.Descriptor
}

type CommitOpts struct {
	RefCfg           cacheconfig.RefConfig
	OCITypes         bool
	Epoch            *time.Time
	RewriteTimestamp bool
}

func (w *Writer) Assemble(
	ctx context.Context,
	req *ExportRequest,
	opts CommitOpts,
) (*ExportedImage, error) {
	if len(req.Platforms) == 0 {
		return nil, fmt.Errorf("image export request has no platforms")
	}

	refs := make([]cache.ImmutableRef, 0, len(req.Platforms))
	for _, input := range req.Platforms {
		refs = append(refs, input.Ref)
	}
	chains, err := w.exportLayers(ctx, opts.RefCfg, refs...)
	if err != nil {
		return nil, err
	}

	provider := contentutil.NewMultiProvider(w.opt.ContentStore)
	sourceAnnotations := map[digest.Digest]map[string]string{}
	exportedPlatforms := make([]ExportedPlatform, 0, len(req.Platforms))

	if len(req.Platforms) == 1 {
		input := req.Platforms[0]
		chain := &chains[0]
		if chain.Provider == nil {
			chain.Provider = w.opt.ContentStore
		}
		if opts.RewriteTimestamp {
			chain, err = w.rewriteExportChainWithEpoch(ctx, opts, chain, input.BaseImage)
			if err != nil {
				return nil, err
			}
		}
		w.addExportChainProvider(provider, sourceAnnotations, chain)

		inlineCache := req.InlineCache[input.Key]
		manifestDesc, configDesc, err := w.commitPlatformManifest(ctx, input, chain, inlineCache, opts)
		if err != nil {
			return nil, err
		}
		exportedPlatforms = append(exportedPlatforms, ExportedPlatform{
			Key:          input.Key,
			Platform:     input.Platform,
			ManifestDesc: manifestDesc,
			ConfigDesc:   configDesc,
		})
		return &ExportedImage{
			RootDesc:          manifestDesc,
			Platforms:         exportedPlatforms,
			Provider:          provider,
			SourceAnnotations: sourceAnnotations,
		}, nil
	}

	index := ocispecs.Index{
		MediaType:   ocispecs.MediaTypeImageIndex,
		Annotations: maps.Clone(req.IndexAnnotations),
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
	}
	if !opts.OCITypes {
		index.MediaType = images.MediaTypeDockerSchema2ManifestList
	}

	gcLabels := map[string]string{}
	for i, input := range req.Platforms {
		chain := &chains[i]
		if chain.Provider == nil {
			chain.Provider = w.opt.ContentStore
		}
		if opts.RewriteTimestamp {
			chain, err = w.rewriteExportChainWithEpoch(ctx, opts, chain, input.BaseImage)
			if err != nil {
				return nil, err
			}
		}
		w.addExportChainProvider(provider, sourceAnnotations, chain)

		inlineCache := req.InlineCache[input.Key]
		manifestDesc, configDesc, err := w.commitPlatformManifest(ctx, input, chain, inlineCache, opts)
		if err != nil {
			return nil, err
		}
		manifestDesc.Platform = &input.Platform
		index.Manifests = append(index.Manifests, manifestDesc)
		gcLabels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i)] = manifestDesc.Digest.String()

		exportedPlatforms = append(exportedPlatforms, ExportedPlatform{
			Key:          input.Key,
			Platform:     input.Platform,
			ManifestDesc: manifestDesc,
			ConfigDesc:   configDesc,
		})
	}

	indexJSON, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal index")
	}
	indexDigest := digest.FromBytes(indexJSON)
	indexDesc := ocispecs.Descriptor{
		Digest:      indexDigest,
		Size:        int64(len(indexJSON)),
		MediaType:   index.MediaType,
		Annotations: maps.Clone(req.IndexDescriptorAnnotations),
	}
	done := progress.OneOff(ctx, "exporting manifest list "+indexDigest.String())
	if err := content.WriteBlob(ctx, w.opt.ContentStore, indexDigest.String(), bytes.NewReader(indexJSON), indexDesc, content.WithLabels(gcLabels)); err != nil {
		return nil, done(errors.Wrapf(err, "error writing manifest list blob %s", indexDigest))
	}
	done(nil)

	return &ExportedImage{
		RootDesc:          indexDesc,
		Platforms:         exportedPlatforms,
		Provider:          provider,
		SourceAnnotations: sourceAnnotations,
	}, nil
}

func (w *Writer) commitPlatformManifest(
	ctx context.Context,
	input PlatformExportInput,
	chain *cache.ExportChain,
	inlineCache []byte,
	opts CommitOpts,
) (manifest ocispecs.Descriptor, config ocispecs.Descriptor, err error) {
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, err
	}
	if len(configJSON) == 0 {
		configJSON, err = defaultImageConfig()
		if err != nil {
			return ocispecs.Descriptor{}, ocispecs.Descriptor{}, err
		}
	}

	history, err := parseHistoryFromConfig(configJSON)
	if err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, err
	}

	chain, history = normalizeLayersAndHistory(ctx, chain, history, opts.OCITypes)
	layerDescs := make([]ocispecs.Descriptor, 0, len(chain.Layers))
	for _, layer := range chain.Layers {
		layerDescs = append(layerDescs, layer.Descriptor)
	}
	configJSON, err = patchImageConfig(configJSON, layerDescs, history, inlineCache, opts.Epoch, input.BaseImage)
	if err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, err
	}

	configDigest := digest.FromBytes(configJSON)
	manifestType := ocispecs.MediaTypeImageManifest
	configType := ocispecs.MediaTypeImageConfig
	if !opts.OCITypes {
		manifestType = images.MediaTypeDockerSchema2Manifest
		configType = images.MediaTypeDockerSchema2Config
	}

	manifestDoc := ocispecs.Manifest{
		MediaType:   manifestType,
		Annotations: maps.Clone(input.ManifestAnnotations),
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: ocispecs.Descriptor{
			Digest:    configDigest,
			Size:      int64(len(configJSON)),
			MediaType: configType,
		},
	}

	gcLabels := map[string]string{
		"containerd.io/gc.ref.content.0": configDigest.String(),
	}
	for i, layer := range chain.Layers {
		desc := layer.Descriptor
		desc.Annotations = removeInternalLayerAnnotations(desc.Annotations, opts.OCITypes)
		manifestDoc.Layers = append(manifestDoc.Layers, desc)
		gcLabels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i+1)] = desc.Digest.String()
	}

	manifestJSON, err := json.MarshalIndent(manifestDoc, "", "  ")
	if err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, errors.Wrap(err, "failed to marshal manifest")
	}
	manifestDigest := digest.FromBytes(manifestJSON)
	manifestDesc := ocispecs.Descriptor{
		Digest:      manifestDigest,
		Size:        int64(len(manifestJSON)),
		MediaType:   manifestType,
		Annotations: maps.Clone(input.ManifestDescriptorAnnotations),
	}
	done := progress.OneOff(ctx, "exporting manifest "+manifestDigest.String())
	if err := content.WriteBlob(ctx, w.opt.ContentStore, manifestDigest.String(), bytes.NewReader(manifestJSON), manifestDesc, content.WithLabels(gcLabels)); err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, done(errors.Wrapf(err, "error writing manifest blob %s", manifestDigest))
	}
	done(nil)

	configDesc := ocispecs.Descriptor{
		Digest:    configDigest,
		Size:      int64(len(configJSON)),
		MediaType: configType,
	}
	done = progress.OneOff(ctx, "exporting config "+configDigest.String())
	if err := content.WriteBlob(ctx, w.opt.ContentStore, configDigest.String(), bytes.NewReader(configJSON), configDesc); err != nil {
		return ocispecs.Descriptor{}, ocispecs.Descriptor{}, done(errors.Wrap(err, "error writing config blob"))
	}
	done(nil)

	return manifestDesc, configDesc, nil
}

func (w *Writer) exportLayers(ctx context.Context, refCfg cacheconfig.RefConfig, refs ...cache.ImmutableRef) ([]cache.ExportChain, error) {
	attr := []attribute.KeyValue{
		attribute.String("exportLayers.compressionType", refCfg.Compression.Type.String()),
		attribute.Bool("exportLayers.forceCompression", refCfg.Compression.Force),
	}
	if refCfg.Compression.Level != nil {
		attr = append(attr, attribute.Int("exportLayers.compressionLevel", *refCfg.Compression.Level))
	}
	span, ctx := tracing.StartSpan(ctx, "export layers", trace.WithAttributes(attr...))

	eg, ctx := errgroup.WithContext(ctx)
	done := progress.OneOff(ctx, "exporting layers")
	out := make([]cache.ExportChain, len(refs))

	for i, ref := range refs {
		i, ref := i, ref
		if ref == nil {
			continue
		}
		eg.Go(func() error {
			chain, err := ref.ExportChain(ctx, refCfg)
			if err != nil {
				return err
			}
			out[i] = *chain
			return nil
		})
	}

	err := done(eg.Wait())
	tracing.FinishWithError(span, err)
	return out, err
}

func rewriteImageLayerWithEpoch(ctx context.Context, cs content.Store, desc ocispecs.Descriptor, comp compression.Config, epoch *time.Time, immutableDiffID digest.Digest) (*ocispecs.Descriptor, error) {
	var immutableDiffIDs map[digest.Digest]struct{}
	if immutableDiffID != "" {
		immutableDiffIDs = map[digest.Digest]struct{}{immutableDiffID: {}}
	}
	converterFn, err := converter.NewWithRewriteTimestamp(ctx, cs, desc, comp, epoch, immutableDiffIDs)
	if err != nil {
		return nil, err
	}
	if converterFn == nil {
		return nil, nil
	}
	return converterFn(ctx, cs, desc)
}

func (w *Writer) rewriteExportChainWithEpoch(ctx context.Context, opts CommitOpts, chain *cache.ExportChain, baseImg *dockerspec.DockerOCIImage) (*cache.ExportChain, error) {
	if opts.Epoch == nil {
		bklog.G(ctx).Warn("rewrite-timestamp is specified, but no source-date-epoch was found")
		return chain, nil
	}
	layers := append([]cache.ExportLayer(nil), chain.Layers...)
	descs := make([]ocispecs.Descriptor, len(layers))
	for i, layer := range layers {
		descs[i] = layer.Descriptor
	}
	cs := contentutil.NewStoreWithProvider(w.opt.ContentStore, chain.Provider)
	eg, ctx := errgroup.WithContext(ctx)
	done := progress.OneOff(ctx, fmt.Sprintf("rewriting layers with source-date-epoch %d (%s)", opts.Epoch.Unix(), opts.Epoch.String()))
	var divergedFromBase bool

	for i, desc := range descs {
		i, desc := i, desc
		diffID := digest.Digest(desc.Annotations[labels.LabelUncompressed])
		if diffID == "" {
			info, err := cs.Info(ctx, desc.Digest)
			if err != nil {
				return nil, err
			}
			diffID = digest.Digest(info.Labels[labels.LabelUncompressed])
		}

		var immutableDiffID digest.Digest
		if !divergedFromBase && baseImg != nil && i < len(baseImg.RootFS.DiffIDs) {
			immutableDiffID = baseImg.RootFS.DiffIDs[i]
			if immutableDiffID == diffID {
				bklog.G(ctx).WithField("blob", desc).Debugf("Not rewriting to apply epoch (immutable diffID %q)", diffID)
				continue
			}
			divergedFromBase = true
		}

		eg.Go(func() error {
			rewrittenDesc, err := rewriteImageLayerWithEpoch(ctx, cs, desc, opts.RefCfg.Compression, opts.Epoch, immutableDiffID)
			if err != nil {
				bklog.G(ctx).WithError(err).Warnf("failed to rewrite layer %d/%d to match source-date-epoch %d (%s)", i+1, len(descs), opts.Epoch.Unix(), opts.Epoch.String())
				return nil
			}
			if rewrittenDesc != nil {
				layers[i].Descriptor = *rewrittenDesc
			}
			return nil
		})
	}

	if err := done(eg.Wait()); err != nil {
		return nil, err
	}
	return &cache.ExportChain{
		Layers:   layers,
		Provider: cs,
	}, nil
}

func defaultImageConfig() ([]byte, error) {
	platform := platforms.Normalize(platforms.DefaultSpec())
	img := ocispecs.Image{
		Platform: ocispecs.Platform{
			Architecture: platform.Architecture,
			OS:           platform.OS,
			OSVersion:    platform.OSVersion,
			OSFeatures:   platform.OSFeatures,
			Variant:      platform.Variant,
		},
		RootFS: ocispecs.RootFS{
			Type: "layers",
		},
	}
	img.Config.WorkingDir = "/"
	img.Config.Env = []string{"PATH=" + system.DefaultPathEnv(platform.OS)}
	dt, err := json.Marshal(img)
	return dt, errors.Wrap(err, "failed to create empty image config")
}

func parseHistoryFromConfig(dt []byte) ([]ocispecs.History, error) {
	var config struct {
		History []ocispecs.History
	}
	if err := json.Unmarshal(dt, &config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal history from config")
	}
	return config.History, nil
}

func patchImageConfig(dt []byte, descs []ocispecs.Descriptor, history []ocispecs.History, inlineCache []byte, epoch *time.Time, baseImg *dockerspec.DockerOCIImage) ([]byte, error) {
	var img ocispecs.Image
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, errors.Wrap(err, "invalid image config for export")
	}

	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(dt, &m); err != nil {
		return nil, errors.Wrap(err, "failed to parse image config for patch")
	}
	if m == nil {
		return nil, errors.Errorf("invalid null image config for export")
	}
	if img.OS == "" {
		return nil, errors.Errorf("invalid image config for export: missing os")
	}
	if img.Architecture == "" {
		return nil, errors.Errorf("invalid image config for export: missing architecture")
	}

	rootFS := ocispecs.RootFS{Type: "layers"}
	for _, desc := range descs {
		rootFS.DiffIDs = append(rootFS.DiffIDs, digest.Digest(desc.Annotations[labels.LabelUncompressed]))
	}
	rootJSON, err := json.Marshal(rootFS)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal rootfs")
	}
	m["rootfs"] = rootJSON

	if epoch != nil {
		var divergedFromBase bool
		for i, h := range history {
			if !divergedFromBase && baseImg != nil && i < len(baseImg.History) && reflect.DeepEqual(h, baseImg.History[i]) {
				continue
			}
			divergedFromBase = true
			if h.Created == nil || h.Created.After(*epoch) {
				history[i].Created = epoch
			}
		}
	}

	historyJSON, err := json.Marshal(history)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal history")
	}
	m["history"] = historyJSON

	if v, ok := m["created"]; ok && epoch != nil {
		var created time.Time
		if err := json.Unmarshal(v, &created); err != nil {
			return nil, errors.Wrapf(err, "failed to unmarshal creation time %q", m["created"])
		}
		if created.After(*epoch) {
			createdJSON, err := json.Marshal(&epoch)
			if err != nil {
				return nil, errors.Wrap(err, "failed to marshal creation time")
			}
			m["created"] = createdJSON
		}
	}
	if _, ok := m["created"]; !ok {
		var created *time.Time
		for _, h := range history {
			if h.Created != nil {
				created = h.Created
			}
		}
		createdJSON, err := json.Marshal(&created)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal creation time")
		}
		m["created"] = createdJSON
	}

	if len(inlineCache) > 0 {
		m["moby.buildkit.cache.v0"] = inlineCache
	}

	out, err := json.Marshal(m)
	return out, errors.Wrap(err, "failed to marshal config after patch")
}

func normalizeLayersAndHistory(ctx context.Context, chain *cache.ExportChain, history []ocispecs.History, oci bool) (*cache.ExportChain, []ocispecs.History) {
	refMeta := make([]refMetadata, len(chain.Layers))
	for i, layer := range chain.Layers {
		description := layer.Description
		if description == "" {
			description = "created by buildkit"
		}
		refMeta[i] = refMetadata{
			description: description,
			createdAt:   layer.CreatedAt,
		}
	}

	var historyLayers int
	for _, h := range history {
		if !h.EmptyLayer {
			historyLayers++
		}
	}
	if historyLayers > len(chain.Layers) {
		bklog.G(ctx).Warn("invalid image config with unaccounted layers")
		historyCopy := make([]ocispecs.History, 0, len(history))
		var layerCount int
		for _, h := range history {
			if layerCount >= len(chain.Layers) {
				h.EmptyLayer = true
			}
			if !h.EmptyLayer {
				layerCount++
			}
			historyCopy = append(historyCopy, h)
		}
		history = historyCopy
	}
	if len(chain.Layers) > historyLayers {
		for _, md := range refMeta[historyLayers:] {
			history = append(history, ocispecs.History{
				Created:   md.createdAt,
				CreatedBy: md.description,
				Comment:   "buildkit.exporter.image.v0",
			})
		}
	}

	var layerIndex int
	for i, h := range history {
		if !h.EmptyLayer {
			if h.Created == nil {
				h.Created = refMeta[layerIndex].createdAt
			}
			layerIndex++
		}
		history[i] = h
	}

	var created *time.Time
	var missingCreated bool
	for _, h := range history {
		if h.Created != nil {
			created = h.Created
			if missingCreated {
				break
			}
		} else {
			missingCreated = true
		}
	}
	missingCreated = false
	for i, h := range history {
		if h.Created != nil {
			if missingCreated {
				created = h.Created
			}
		} else {
			missingCreated = true
			h.Created = created
		}
		history[i] = h
	}

	descs := make([]ocispecs.Descriptor, len(chain.Layers))
	for i, layer := range chain.Layers {
		descs[i] = layer.Descriptor
	}
	descs = compression.ConvertAllLayerMediaTypes(ctx, oci, descs...)
	for i := range descs {
		chain.Layers[i].Descriptor = descs[i]
	}
	return chain, history
}

func removeInternalLayerAnnotations(in map[string]string, oci bool) map[string]string {
	if len(in) == 0 || !oci {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		switch key {
		case labels.LabelUncompressed, "buildkit/createdat":
			continue
		default:
			if strings.HasPrefix(key, "containerd.io/distribution.source.") {
				continue
			}
			out[key] = value
		}
	}
	return out
}

type refMetadata struct {
	description string
	createdAt   *time.Time
}

func (w *Writer) addExportChainProvider(provider *contentutil.MultiProvider, sourceAnnotations map[digest.Digest]map[string]string, chain *cache.ExportChain) {
	if chain == nil {
		return
	}
	for _, layer := range chain.Layers {
		desc := layer.Descriptor
		provider.Add(desc.Digest, chain.Provider)
		for key, value := range desc.Annotations {
			if !strings.HasPrefix(key, "containerd.io/distribution.source.") {
				continue
			}
			if sourceAnnotations[desc.Digest] == nil {
				sourceAnnotations[desc.Digest] = map[string]string{}
			}
			sourceAnnotations[desc.Digest][key] = value
		}
	}
}

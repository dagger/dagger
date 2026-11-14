package dagql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// The engine image's entries carry their parts' layers as files in the image:
// an image directory holds the bundle and, under blobs/<algorithm>/<encoded>,
// each layer's blob. An offer of the image names a blob by imageBlobScheme
// and its digest, never by a path, and the content source reads it from the
// image directory it was given, through a root that confines every read to
// that directory.

// imageBlobScheme is the scheme of the addresses of an image's offers.
const imageBlobScheme = "dagger-image"

// ImageBlobAddress is the address an image's offer gives the blob d.
func ImageBlobAddress(d digest.Digest) BlobAddress {
	return BlobAddress{URL: imageBlobPrefix + d.String()}
}

// ImageBlobPath is where an image directory holds the blob d.
func ImageBlobPath(d digest.Digest) string {
	return path.Join("blobs", d.Algorithm().String(), d.Encoded())
}

const imageBlobPrefix = imageBlobScheme + ":"

func isImageBlobAddress(address BlobAddress) bool {
	return strings.HasPrefix(address.URL, imageBlobPrefix)
}

// imageOfferLayers reports whether every layer of offer is addressed in the
// engine's image, at its own digest. Such an offer is the image's, and nothing
// else's is. It allocates nothing: Available calls it on every ranking.
func imageOfferLayers(offer *PersistedPartOffer) bool {
	if len(offer.Chain.Layers) == 0 {
		return false
	}
	for _, layer := range offer.Chain.Layers {
		d := layer.Descriptor.Digest
		address, ok := offer.Chain.Addresses[d]
		if !ok || !isImageBlobAddress(address) || address.URL[len(imageBlobPrefix):] != string(d) {
			return false
		}
	}
	return true
}

// checkSenderAddresses refuses a bundle whose addresses don't belong to its
// sender: the engine image's bundle addresses every blob in the image, and a
// bundle from the Cloud addresses none there.
func checkSenderAddresses(from CacheID, bundle ValueBundle) error {
	check := func(address BlobAddress) error {
		image := isImageBlobAddress(address)
		switch {
		case from == imageCacheID && !image:
			return fmt.Errorf("merge values: the image's bundle addresses a blob outside the image: %q", address.URL)
		case from != imageCacheID && image:
			return fmt.Errorf("merge values: a bundle from %q addresses a blob in the engine image", from)
		}
		return nil
	}
	checkChain := func(chain *OfferedChain) error {
		if chain == nil {
			return nil
		}
		if from == imageCacheID && chain.RenewalKey != "" {
			return errors.New("merge values: the image's bundle has a renewal key")
		}
		for _, address := range chain.Addresses {
			if err := check(address); err != nil {
				return err
			}
		}
		return nil
	}
	for _, address := range bundle.Addresses {
		if err := check(address); err != nil {
			return err
		}
	}
	for _, output := range bundle.Outputs {
		if err := checkChain(output.Chain); err != nil {
			return err
		}
	}
	for _, value := range bundle.Values {
		for i := range value.Record.Envelope.PendingOffers {
			if err := checkChain(&value.Record.Envelope.PendingOffers[i].Chain); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetImageContent gives the cache's content source the engine image's
// directory, which supplies the parts the image's offers name. It is set once,
// at start, before the image's bundle merges.
func (c *Cache) SetImageContent(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("engine image content: %w", err)
	}
	if !c.partContentSource.image.CompareAndSwap(nil, root) {
		root.Close()
		return errors.New("engine image content is already set")
	}
	return nil
}

func (s *PartContentSource) imageRoot() *os.Root {
	if s == nil {
		return nil
	}
	return s.image.Load()
}

// imageAvailable reports whether the image supplies offer: it is the image's,
// and the cache has the image's directory. It reads no blob.
func (s *PartContentSource) imageAvailable(offer *PersistedPartOffer) bool {
	return imageOfferLayers(offer) && s.imageRoot() != nil
}

// imagePartProvider reads an image offer's blobs from the image directory.
type imagePartProvider struct {
	root   *os.Root
	layers []imageLayer
}

// imageLayer is one layer an image offer names: its blob and size.
type imageLayer struct {
	Digest digest.Digest
	Size   int64
}

func newImagePartProvider(root *os.Root, offer PersistedPartOffer) *imagePartProvider {
	p := &imagePartProvider{root: root}
	for _, layer := range offer.Chain.Layers {
		p.layers = append(p.layers, imageLayer{Digest: layer.Descriptor.Digest, Size: layer.Descriptor.Size})
	}
	return p
}

func (p *imagePartProvider) layer(d digest.Digest) (imageLayer, bool) {
	for _, layer := range p.layers {
		if layer.Digest == d {
			return layer, true
		}
	}
	return imageLayer{}, false
}

func (p *imagePartProvider) Info(_ context.Context, d digest.Digest) (content.Info, error) {
	layer, ok := p.layer(d)
	if !ok {
		return content.Info{}, fmt.Errorf("image blob %s is outside the offered chain", d)
	}
	return content.Info{Digest: d, Size: layer.Size}, nil
}

func (p *imagePartProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	layer, ok := p.layer(desc.Digest)
	switch {
	case !ok:
		return nil, fmt.Errorf("image blob %s is outside the offered chain", desc.Digest)
	case layer.Size != desc.Size:
		return nil, fmt.Errorf("image blob %s: size %d, offered %d", desc.Digest, desc.Size, layer.Size)
	}
	if err := desc.Digest.Validate(); err != nil {
		return nil, err
	}
	file, err := p.root.Open(ImageBlobPath(desc.Digest))
	if err != nil {
		return nil, fmt.Errorf("image blob %s: %w", desc.Digest, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() != desc.Size {
		file.Close()
		return nil, fmt.Errorf("image blob %s: the image holds %d bytes, offered %d", desc.Digest, info.Size(), desc.Size)
	}
	return imageBlobReader{File: file, size: info.Size()}, nil
}

type imageBlobReader struct {
	*os.File
	size int64
}

func (r imageBlobReader) Size() int64 { return r.size }

// ErrFromImage refuses to export an entry the engine image ships: every engine
// built from the image has it, so the Cloud carries only what engines made.
var ErrFromImage = errors.New("the engine image ships this entry")

// FromImage reports whether the engine image ships result: the image's merge
// gave its entry an image holding.
func (c *Cache) FromImage(result AnyResult) bool {
	if result == nil {
		return false
	}
	res := result.cacheSharedResult()
	if res == nil {
		return false
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	_, image := res.imageHoldingLocked()
	return image != nil
}

// ImageEntryByContent returns the number of an entry the engine image ships
// whose value is of type typeName and whose content digest is contentDigest,
// or ErrUnknownEntry when the image ships none. Only the image's entries
// answer: a content digest isn't secret, and the image's entries are already
// every client's, while another client's result is not.
func (c *Cache) ImageEntryByContent(contentDigest digest.Digest, typeName string) (uint64, error) {
	if contentDigest == "" {
		return 0, errors.New("image entry by content: no digest")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	candidates := newSharedResultSet()
	c.appendDigestResultsLocked(candidates, contentDigest, time.Now().Unix(), nil)
	for res := range candidates.Items() {
		if _, image := res.imageHoldingLocked(); image == nil {
			continue
		}
		frame := res.loadResultCall()
		if frame == nil || frame.Type == nil || frame.Type.NamedType != typeName {
			continue
		}
		if frame.ContentDigest() != contentDigest {
			continue
		}
		return uint64(res.id), nil
	}
	return 0, fmt.Errorf("image entry of type %s with content %s: %w", typeName, contentDigest, ErrUnknownEntry)
}

// ImageBundleWriter receives an engine image's bundle as ExportImageBundle
// makes it: the bundle, then each blob its offers name, once each, by digest.
type ImageBundleWriter interface {
	WriteBundle(context.Context, ValueBundle) error
	WriteBlob(ctx context.Context, d digest.Digest, r content.ReaderAt) error
}

// ExportImageBundle exports results for an engine image to ship: each
// output's result, with its record and its closure's, and the output's part,
// whose blobs the image holds (ImageBlobAddress). An engine built from the image merges the
// bundle at start (MergeValues from imageCacheID). Every result must be
// captured. The engine it runs on may be busy with other work: a capture
// there, such as the remote cache's, can hold a row of the closure for a
// moment, and a capture never waits for a row (WithExportedValues). So when
// a result's closure reaches a row that's busy now, the export lets go of
// the capture, waits, and captures again, until every result is captured or
// ctx ends. A result refused for any other reason, or one whose row stays
// busy until a later evaluation (BusyBookkeeping), fails the export. It is
// built for the image's build, and nothing in the engine's runtime calls it.
func (c *Cache) ExportImageBundle(ctx context.Context, outputs []SelectedValueOutput, cfg config.RefConfig, w ImageBundleWriter) error {
	if len(outputs) == 0 {
		return errors.New("export image bundle: no results")
	}
	// Each result is one root, though several of its parts are outputs.
	var results []AnyResult
	seen := map[*sharedResult]bool{}
	for _, output := range outputs {
		res := output.Result.cacheSharedResult()
		if res == nil || seen[res] {
			continue
		}
		seen[res] = true
		results = append(results, output.Result)
	}
	selection := ValueSelection{Roots: results, Outputs: outputs, EveryRoot: true}
	// Each wait is logged, and so is the capture after them, with the time
	// from the first busy capture to the one that captured every result:
	// the log is where this export meeting another capture shows.
	var waits int
	var waitStart time.Time
	for pause := imageBundleBusyPause; ; pause = min(2*pause, maxImageBundleBusyPause) {
		passStart := time.Now()
		err := c.WithExportedValues(ctx, selection, cfg, func(ctx context.Context, values *ExportedValues) error {
			return writeImageBundle(ctx, values, w)
		})
		left := leftOutOnly(err)
		if left == nil {
			if err == nil && waits > 0 {
				slog.Info("export image bundle: every result captured after waiting for busy ones", "results", len(results), "waits", waits, "waited", passStart.Sub(waitStart))
			}
			return err
		}
		first := left.Roots[0]
		err = fmt.Errorf("export image bundle: %d of %d results left out, the first (row %d): %w", len(left.Roots), len(results), first.Row, first.Err)
		if !busyForNow(left.Roots) {
			return err
		}
		if waits == 0 {
			waitStart = time.Now()
		}
		waits++
		slog.Info("export image bundle: results busy, capturing again", "left_out", len(left.Roots), "results", len(results), "row", first.Row, "kind", first.Kind, "error", first.Err, "pause", pause)
		select {
		case <-ctx.Done():
			return errors.Join(context.Cause(ctx), err)
		case <-time.After(pause):
		}
	}
}

// The export's first pause after a busy capture, and its longest: a busy
// row is mostly another capture's, held for well under a second.
const (
	imageBundleBusyPause    = 10 * time.Millisecond
	maxImageBundleBusyPause = time.Second
)

// leftOutOnly is the RootsLeftOutError err is when it is nothing more, such
// as joined with the capture's release error: nil otherwise.
func leftOutOnly(err error) *RootsLeftOutError {
	for {
		switch e := err.(type) {
		case *RootsLeftOutError:
			return e
		case interface{ Unwrap() []error }:
			errs := e.Unwrap()
			if len(errs) != 1 {
				return nil
			}
			err = errs[0]
		default:
			return nil
		}
	}
}

// busyForNow reports whether every root left out is busy with something
// that ends on its own: no busy row it reaches waits for a later
// evaluation.
func busyForNow(roots []LeftOutRoot) bool {
	for _, root := range roots {
		if !errors.Is(root.Err, ErrPersistStateNotReady) || root.Kind == BusyBookkeeping {
			return false
		}
		for _, busy := range root.Busy {
			if busy.Kind == BusyBookkeeping {
				return false
			}
		}
	}
	return true
}

// writeImageBundle writes an image's bundle from values, every root
// captured, and then each blob its outputs' chains name, once each.
func writeImageBundle(ctx context.Context, values *ExportedValues, w ImageBundleWriter) error {
	bundle := values.Bundle
	address := func(addresses map[digest.Digest]BlobAddress, layers []snapshots.ExportLayer) map[digest.Digest]BlobAddress {
		if addresses == nil {
			addresses = map[digest.Digest]BlobAddress{}
		}
		for _, layer := range layers {
			addresses[layer.Descriptor.Digest] = ImageBlobAddress(layer.Descriptor.Digest)
		}
		return addresses
	}
	if len(bundle.Layers) > 0 {
		bundle.Addresses = address(bundle.Addresses, bundle.Layers)
	}
	for i := range bundle.Outputs {
		if chain := bundle.Outputs[i].Chain; chain != nil && len(chain.Layers) > 0 {
			chain.Addresses = address(chain.Addresses, chain.Layers)
		}
	}
	if err := w.WriteBundle(ctx, bundle); err != nil {
		return err
	}
	written := map[digest.Digest]bool{}
	for _, entry := range values.Chains.Entries {
		for _, layer := range entry.Layers {
			d := layer.Descriptor.Digest
			if written[d] {
				continue
			}
			written[d] = true
			if err := d.Validate(); err != nil {
				return err
			}
			r, err := entry.Provider.ReaderAt(ctx, layer.Descriptor)
			if err != nil {
				return fmt.Errorf("export image bundle: blob %s: %w", d, err)
			}
			err = w.WriteBlob(ctx, d, r)
			if closeErr := r.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("export image bundle: blob %s: %w", d, err)
			}
		}
	}
	return nil
}

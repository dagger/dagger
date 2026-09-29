package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

var _ remotecache.Adapter = (*RemoteCacheAdapter)(nil)

// exportRefConfig is how exported chains' layers are compressed: the engine's
// default, as for image exports.
var exportRefConfig = config.RefConfig{Compression: compression.New(compression.Default)}

// CacheIdentity returns the engine cache's identity and generation, which the
// remote cache integration says in hello.
func (a *RemoteCacheAdapter) CacheIdentity() (string, uint64) {
	identity := a.cache.Identity()
	return identity.ID, identity.Generation
}

// Export holds the requested entries by number and captures the held roots
// with the selected outputs, leaving out those outside the captured closure.
// A missing root is skipped as gone. When a busy entry fails the capture,
// each root is probed alone, those whose probe fails are skipped as busy, and
// the others are captured together; if that capture fails too, they are all
// busy. consume is called once, with the bundle while its chains stay open,
// or with no bundle when no root survived.
func (a *RemoteCacheAdapter) Export(ctx context.Context, req protocol.Export, consume func(context.Context, remotecache.Export) error) (rerr error) {
	if a.stopped.Load() {
		return ErrRemoteCacheAdapterClosed
	}
	start := time.Now()
	holds := entryHolds{cache: a.cache, held: map[uint64]dagql.AnyResult{}}
	defer func() { rerr = errors.Join(rerr, holds.release(ctx)) }()
	var roots []heldRoot
	var skipped []protocol.SkippedRoot
	for _, number := range req.Roots {
		res, err := holds.hold(ctx, number)
		switch {
		case errors.Is(err, dagql.ErrUnknownEntry):
			skipped = append(skipped, protocol.SkippedRoot{Number: number, Reason: protocol.SkipGone})
		case err != nil:
			return err
		default:
			roots = append(roots, heldRoot{number: number, result: res})
		}
	}
	// A selected output can survive its root. One that is gone is left out,
	// as one outside the captured closure is.
	var outputs []dagql.SelectedValueOutput
	for _, output := range req.Outputs {
		res, err := holds.hold(ctx, output.Number)
		switch {
		case errors.Is(err, dagql.ErrUnknownEntry):
		case err != nil:
			return err
		default:
			outputs = append(outputs, dagql.SelectedValueOutput{Result: res, Address: output.Address})
		}
	}

	captured := false
	export := func(roots []heldRoot) error {
		selection := dagql.ValueSelection{Outputs: outputs, LeaveOutOutputsOutsideClosure: true}
		for _, root := range roots {
			selection.Roots = append(selection.Roots, root.result)
		}
		return a.cache.WithExportedValues(ctx, selection, exportRefConfig, func(ctx context.Context, values *dagql.ExportedValues) error {
			captured = true
			logExport(req, skipped, values, time.Since(start))
			return consume(ctx, remotecache.Export{Bundle: &values.Bundle, Skipped: skipped, Blobs: newChainBlobs(values.Chains)})
		})
	}
	if len(roots) > 0 {
		err := export(roots)
		if captured || !errors.Is(err, dagql.ErrPersistStateNotReady) {
			return err
		}
		// A probe copies records and opens no chain.
		var ready []heldRoot
		for _, root := range roots {
			err := a.cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{root.result}}, exportRefConfig, func(context.Context, *dagql.ExportedValues) error {
				return nil
			})
			switch {
			case errors.Is(err, dagql.ErrPersistStateNotReady):
				skipped = append(skipped, protocol.SkippedRoot{Number: root.number, Reason: protocol.SkipBusy})
			case err != nil:
				return err
			default:
				ready = append(ready, root)
			}
		}
		if len(ready) > 0 {
			err := export(ready)
			if captured || !errors.Is(err, dagql.ErrPersistStateNotReady) {
				return err
			}
			// A task started between the probes and the export.
			for _, root := range ready {
				skipped = append(skipped, protocol.SkippedRoot{Number: root.number, Reason: protocol.SkipBusy})
			}
		}
	}
	logExport(req, skipped, nil, time.Since(start))
	return consume(ctx, remotecache.Export{Skipped: skipped})
}

type heldRoot struct {
	number uint64
	result dagql.AnyResult
}

// entryHolds holds entries by number, each once, for one request.
type entryHolds struct {
	cache    *dagql.Cache
	held     map[uint64]dagql.AnyResult
	releases []func(context.Context) error
}

func (h *entryHolds) hold(ctx context.Context, number uint64) (dagql.AnyResult, error) {
	if res, ok := h.held[number]; ok {
		return res, nil
	}
	res, release, err := h.cache.HoldEntry(ctx, number)
	if err != nil {
		return nil, err
	}
	h.held[number] = res
	h.releases = append(h.releases, release)
	return res, nil
}

func (h *entryHolds) release(ctx context.Context) error {
	var err error
	for _, release := range h.releases {
		err = errors.Join(err, release(ctx))
	}
	return err
}

// logExport records one export's capture: the roots asked for and skipped,
// the bundle's records and outputs, the time from the request to the bundle,
// and the part of it spent opening the chains, which compresses layers.
func logExport(req protocol.Export, skipped []protocol.SkippedRoot, values *dagql.ExportedValues, capture time.Duration) {
	var records, outputs int
	var chains time.Duration
	if values != nil {
		records, outputs, chains = len(values.Bundle.Values), len(values.Bundle.Outputs), values.ChainTime
	}
	slog.Info("remote cache export",
		"roots", len(req.Roots),
		"skipped", len(skipped),
		"records", records,
		"outputs", outputs,
		"capture", capture,
		"chains", chains)
}

// chainBlobs reads the layer blobs of an export's chains.
type chainBlobs map[digest.Digest]chainBlob

type chainBlob struct {
	provider content.InfoReaderProvider
	desc     ocispecs.Descriptor
}

func newChainBlobs(chains *dagql.SelectedChains) chainBlobs {
	blobs := chainBlobs{}
	if chains == nil {
		return blobs
	}
	for _, chain := range chains.Entries {
		for _, layer := range chain.Layers {
			blobs[layer.Descriptor.Digest] = chainBlob{provider: chain.Provider, desc: layer.Descriptor}
		}
	}
	return blobs
}

func (b chainBlobs) ReadBlob(ctx context.Context, dgst digest.Digest) (io.ReadCloser, int64, error) {
	blob, ok := b[dgst]
	if !ok {
		return nil, 0, fmt.Errorf("blob %s is not in the export's chains", dgst)
	}
	ra, err := blob.provider.ReaderAt(ctx, blob.desc)
	if err != nil {
		return nil, 0, err
	}
	return readerAtCloser{Reader: io.NewSectionReader(ra, 0, blob.desc.Size), Closer: ra}, blob.desc.Size, nil
}

type readerAtCloser struct {
	io.Reader
	io.Closer
}

// Merge merges a bundle of the Cloud's values into the cache and answers with
// what the commit left. A merge that committed is answered even when a
// release after the commit failed: the cache changed, and the service must
// learn how. The release's error is logged instead.
func (a *RemoteCacheAdapter) Merge(ctx context.Context, req protocol.Merge) (protocol.Merged, error) {
	if a.stopped.Load() {
		return protocol.Merged{}, ErrRemoteCacheAdapterClosed
	}
	start := time.Now()
	reply, err := a.mergeValues(ctx, dagql.CloudCacheID, req.Bundle)
	slog.Info("remote cache merge",
		"records", len(req.Bundle.Values),
		"roots", len(req.Bundle.Roots),
		"committed", reply.Committed,
		"commitHold", reply.CommitHold,
		"duration", time.Since(start),
		"error", err)
	if !reply.Committed {
		return protocol.Merged{}, err
	}
	if err != nil {
		slog.Warn("remote cache merge committed, then a release failed", "error", err)
	}
	return mergedReply(reply), nil
}

// mergedReply is the merged message for a committed merge's reply.
func mergedReply(reply dagql.MergeReply) protocol.Merged {
	merged := protocol.Merged{
		Generation: reply.Generation,
		EngineTime: reply.EngineTimeUnixNano,
		Values:     make([]protocol.MergedValue, 0, len(reply.Values)),
		Retained:   []protocol.RetainedRoot{},
	}
	for _, value := range reply.Values {
		merged.Values = append(merged.Values, protocol.MergedValue{
			Ordinal:       value.Ordinal,
			Number:        value.Number,
			Replacements:  value.Replacements,
			ExpiresAtUnix: value.ExpiresAtUnix,
			Deps:          nonNilNumbers(value.Deps),
			Complete:      nonNilAddresses(value.Parts),
			Offered:       nonNilAddresses(value.OfferedParts),
		})
	}
	for _, root := range reply.Roots {
		switch {
		case root.Expired:
			merged.Skipped = append(merged.Skipped, protocol.SkippedValue{Ordinal: root.Ordinal, Reason: protocol.SkipExpired})
		case root.Retained:
			merged.Retained = append(merged.Retained, protocol.RetainedRoot{Ordinal: root.Ordinal, ExpiresAtUnix: root.RetentionExpiresAtUnix})
		}
	}
	return merged
}

// The wire's lists are empty rather than null.
func nonNilNumbers(numbers []uint64) []uint64 {
	if numbers == nil {
		return []uint64{}
	}
	return numbers
}

func nonNilAddresses(addresses []dagql.PersistedPartAddress) []dagql.PersistedPartAddress {
	if addresses == nil {
		return []dagql.PersistedPartAddress{}
	}
	return addresses
}

// OfferParts places the Cloud's parts on the cache's entries by number. Each
// item's entry is held while its parts are offered; a missing one is gone.
// An item's replacement count and dependencies are those its outcomes were
// decided at.
func (a *RemoteCacheAdapter) OfferParts(ctx context.Context, req protocol.Offer) (protocol.Offered, error) {
	if a.stopped.Load() {
		return protocol.Offered{}, ErrRemoteCacheAdapterClosed
	}
	offered := protocol.Offered{Items: make([]protocol.OfferedItem, 0, len(req.Items))}
	for _, item := range req.Items {
		answer, err := a.offerItem(ctx, item)
		if err != nil {
			return protocol.Offered{}, err
		}
		offered.Items = append(offered.Items, answer)
	}
	return offered, nil
}

func (a *RemoteCacheAdapter) offerItem(ctx context.Context, item protocol.OfferItem) (_ protocol.OfferedItem, rerr error) {
	receiver, release, err := a.cache.HoldEntry(ctx, item.Number)
	if errors.Is(err, dagql.ErrUnknownEntry) {
		return protocol.OfferedItem{Number: item.Number, Gone: true}, nil
	}
	if err != nil {
		return protocol.OfferedItem{}, err
	}
	defer func() { rerr = errors.Join(rerr, release(ctx)) }()
	offers := make([]dagql.CloudPartOffer, len(item.Offers))
	for i, offer := range item.Offers {
		offers[i] = dagql.CloudPartOffer{Offer: offer.Offer, CloudNumber: offer.CloudNumber, CloudStored: offer.CloudStored, CloudExpiresAtUnix: offer.CloudExpiresAtUnix}
	}
	// The dispositions carry each part's own error; a context error ends
	// the request.
	dispositions, _ := a.cache.OfferParts(ctx, receiver, offers)
	if err := context.Cause(ctx); err != nil {
		return protocol.OfferedItem{}, err
	}
	answer := protocol.OfferedItem{Number: item.Number, Parts: make([]protocol.OfferedPart, 0, len(dispositions))}
	for _, disposition := range dispositions {
		outcome, err := protocol.OfferOutcomeOf(disposition.Outcome)
		if err != nil {
			return protocol.OfferedItem{}, err
		}
		part := protocol.OfferedPart{Address: disposition.Address, Outcome: outcome}
		if disposition.Err != nil {
			part.Message = disposition.Err.Error()
		}
		answer.Parts = append(answer.Parts, part)
	}
	// Every part is decided at the held entry's one value, whose
	// dependencies only grow, so the last disposition's state is the latest.
	if n := len(dispositions); n > 0 {
		answer.Replacements, answer.Deps = dispositions[n-1].Replacements, dispositions[n-1].Deps
	}
	return answer, nil
}

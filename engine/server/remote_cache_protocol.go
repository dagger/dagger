package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

var _ remotecache.Adapter = (*RemoteCacheAdapter)(nil)

// exportLayerWorkers is how many of an export's missing layer blobs build at
// once. Layers build independently: each is the diff of its snapshot against
// its parent's, compressed.
const exportLayerWorkers = 4

// exportRefConfig is how exported chains' layers are compressed: zstd, which
// compresses several times faster than gzip into smaller blobs, and
// decompresses faster for the engines that install them. A layer that already
// has a blob, such as a pulled image's, keeps it. It is also how many build at
// once: up to exportLayerWorkers per export, and across all exports no more
// than remotecache.ExportsInFlight serial exports built. Only building a blob
// takes the gate, so it bounds the engine's compression streams and layer
// reads.
var exportRefConfig = config.RefConfig{
	Compression: compression.New(compression.Zstd),
	Builds: config.LayerBuilds{
		Workers: exportLayerWorkers,
		Gate:    semaphore.NewWeighted(max(exportLayerWorkers, remotecache.ExportsInFlight)),
	},
}

// CacheIdentity returns the engine cache's identity and generation, which the
// remote cache integration says in hello.
func (a *RemoteCacheAdapter) CacheIdentity() (string, uint64) {
	identity := a.cache.Identity()
	return identity.ID, identity.Generation
}

// Export holds the requested entries by number and captures the held roots
// once, with the selected outputs, leaving out those outside the captured
// closure. A missing root is skipped as gone. A root whose closure reaches a
// row the capture can't capture now is skipped as busy, and one whose closure
// reaches a record the capture refuses is skipped as refused, each with that
// row's error as the message; the others go in the bundle. A capture that
// fails otherwise fails the request. consume is called once, with the bundle
// while its chains stay open, or with no bundle when no root survived.
func (a *RemoteCacheAdapter) Export(ctx context.Context, req protocol.Export, consume func(context.Context, remotecache.Export) error) (rerr error) {
	if a.stopped.Load() || !a.exports.tryBegin() {
		return ErrRemoteCacheAdapterClosed
	}
	defer a.exports.end()
	ctx, release, ok := a.paused.begin(ctx)
	if !ok {
		// The engine has given up its remote-cache work until a session
		// starts: every root is refused.
		skipped := make([]protocol.SkippedRoot, len(req.Roots))
		for i, number := range req.Roots {
			skipped[i] = protocol.SkippedRoot{Number: number, Reason: protocol.SkipRefused, Message: errRemoteCacheTimedOut.Error()}
		}
		return consume(ctx, remotecache.Export{Skipped: skipped})
	}
	defer release()
	start := time.Now()
	var local exportCapture
	holds := entryHolds{cache: a.cache, held: map[uint64]dagql.AnyResult{}}
	defer func() {
		releaseStart := time.Now()
		err := holds.release(ctx)
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.release.holds", wcprof.OpOpts{}, releaseStart, time.Now(), profExportOutcome(err))
		rerr = errors.Join(rerr, err)
	}()
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
	local.hold = time.Since(start)
	wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.hold", wcprof.OpOpts{}, start, start.Add(local.hold), wcprof.OutcomeOK)

	if len(roots) == 0 {
		logExport(req, skipped, nil, time.Since(start), local)
		return consume(ctx, remotecache.Export{Skipped: skipped})
	}
	selection := dagql.ValueSelection{Outputs: outputs, LeaveOutOutputsOutsideClosure: true}
	for _, root := range roots {
		selection.Roots = append(selection.Roots, root.result)
	}
	// leftOut skips the roots the capture left out, in the request's order.
	leftOut := func(left []dagql.LeftOutRoot) {
		for _, root := range left {
			reason := protocol.SkipRefused
			if errors.Is(root.Err, dagql.ErrPersistStateNotReady) {
				reason = protocol.SkipBusy
			} else {
				slog.Warn("remote cache export refused a root", "number", roots[root.Index].number, "row", root.Row, "error", root.Err)
			}
			skipped = append(skipped, protocol.SkippedRoot{Number: roots[root.Index].number, Reason: reason, Message: skippedMessage(root.Err)})
		}
	}
	// The capture's profile op ends when the bundle is handed on: the
	// consumer's upload is the export's, not the capture's. So are the
	// releases after it, each an op of the export's: the chains', once the
	// consumer is done with them, then the capture's records', as
	// WithExportedValues returns.
	captureCtx, captureOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.capture", wcprof.OpOpts{})
	captured := false
	var chainsReleased time.Time
	err := a.cache.WithExportedValues(captureCtx, selection, exportRefConfig, func(captureCtx context.Context, values *dagql.ExportedValues) error {
		captured = true
		leftOut(values.LeftOut)
		logExport(req, skipped, values, time.Since(start), local)
		captureOp.End(wcprof.OutcomeOK)
		err := consume(wcprof.ContextWithOpID(captureCtx, wcprof.CurrentOpID(ctx)), remotecache.Export{Bundle: &values.Bundle, Skipped: skipped, Blobs: newChainBlobs(values.Chains)})
		releaseStart := time.Now()
		// The chains release once: WithExportedValues returns this
		// release's error from its own.
		chainsErr := values.Chains.Release(captureCtx)
		chainsReleased = time.Now()
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.release.chains", wcprof.OpOpts{}, releaseStart, chainsReleased, profExportOutcome(chainsErr))
		return err
	})
	if captured {
		// Its outcome is the capture's, the consumer's error included.
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.release.capture", wcprof.OpOpts{}, chainsReleased, time.Now(), profExportOutcome(err))
		return err
	}
	captureOp.End(profExportOutcome(err))
	var left *dagql.RootsLeftOutError
	if !errors.As(err, &left) {
		return err
	}
	leftOut(left.Roots)
	logExport(req, skipped, nil, time.Since(start), local)
	return consume(ctx, remotecache.Export{Skipped: skipped})
}

// maxSkippedMessageBytes bounds the message a busy or refused root's skip
// carries, so that one long error can't bloat the reply.
const maxSkippedMessageBytes = 1024

// skippedMessage is err's message, cut to maxSkippedMessageBytes at a rune
// boundary.
func skippedMessage(err error) string {
	msg := err.Error()
	if len(msg) <= maxSkippedMessageBytes {
		return msg
	}
	cut := maxSkippedMessageBytes
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
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

// exportCapture is where an export's time went outside the capture it logs:
// holding the entries.
type exportCapture struct {
	hold time.Duration
}

// logExport records one export's capture: the roots asked for and skipped,
// the bundle's records and outputs, the time from the request to the bundle,
// and the part of it spent opening the chains, which compresses layers. Then
// where the capture's time went, and what the chains' layers took.
func logExport(req protocol.Export, skipped []protocol.SkippedRoot, values *dagql.ExportedValues, capture time.Duration, local exportCapture) {
	slog.Info("remote cache export", exportLogAttrs(req, skipped, values, capture, local)...)
}

// maxLoggedSkippedRoots is how many skipped roots an export's log line names:
// past it, they are only counted by reason.
const maxLoggedSkippedRoots = 8

// exportLogAttrs are an export's log attributes: its roots, its skipped roots
// counted by reason and, when there are at most maxLoggedSkippedRoots of
// them, named with their reasons, then what the bundle holds and how long
// the capture and the chains took, and then the capture's phases and the
// chains' layers. With no bundle, those of the capture and the chains are 0.
func exportLogAttrs(req protocol.Export, skipped []protocol.SkippedRoot, values *dagql.ExportedValues, capture time.Duration, local exportCapture) []any {
	var records, outputs int
	var chains time.Duration
	var phases dagql.ExportPhases
	var stats snapshots.ExportStats
	if values != nil {
		records, outputs, chains = len(values.Bundle.Values), len(values.Bundle.Outputs), values.ChainTime
		phases = values.Phases
		if values.Chains != nil {
			stats = values.Chains.Stats
		}
	}
	counts := map[protocol.SkipReason]int{}
	for _, root := range skipped {
		counts[root.Reason]++
	}
	attrs := []any{
		"roots", len(req.Roots),
		"skipped", len(skipped),
		"skipped_gone", counts[protocol.SkipGone],
		"skipped_busy", counts[protocol.SkipBusy],
		"skipped_refused", counts[protocol.SkipRefused],
	}
	if len(skipped) > 0 && len(skipped) <= maxLoggedSkippedRoots {
		named := make([]string, len(skipped))
		for i, root := range skipped {
			named[i] = fmt.Sprintf("%d:%s", root.Number, root.Reason)
		}
		attrs = append(attrs, "skipped_roots", named)
	}
	return append(attrs,
		"records", records,
		"outputs", outputs,
		"capture", capture,
		"chains", chains,
		"hold", local.hold,
		"walk_wait", phases.WalkWait,
		"walk", phases.Walk,
		"copy", phases.Copy,
		"bundle", phases.Bundle,
		"retried_stale", phases.RetriedStale,
		"layers", stats.Layers,
		"layers_reused", stats.LayersReused,
		"layers_diffed", stats.LayersDiffed,
		"raw_bytes", stats.RawBytes,
		"blob_bytes", stats.BlobBytes,
		"diff_time", stats.DiffTime,
		"lock_wait", stats.LockWait,
		"build_wait", stats.BuildWait,
		"layers_merged", stats.LayersMerged,
		"merged_raw_bytes", stats.MergedRawBytes,
		"merged_blob_bytes", stats.MergedBlobBytes,
		"merged_diff_time", stats.MergedDiffTime)
}

// offerLogAttrs are an offered part's log attributes: the entry offered to,
// the row and part the offer's address resolved to, which is the entry an
// install of the part logs, and what the engine did with it. Then the offered
// chain's layers and size, the time to its earliest address's expiry, and the
// time from the offer's arrival to its admission. Never an address.
func offerLogAttrs(number uint64, offer dagql.PersistedPartOffer, disposition dagql.OfferDisposition, outcome protocol.OfferOutcome, admit time.Duration, now time.Time) []any {
	address := disposition.TargetAddress
	if disposition.Target == 0 {
		address = disposition.Address
	}
	var size int64
	for _, layer := range offer.Chain.Layers {
		size += layer.Descriptor.Size
	}
	var earliest int64
	for _, blob := range offer.Chain.Addresses {
		if blob.ExpiresAtUnix != 0 && (earliest == 0 || blob.ExpiresAtUnix < earliest) {
			earliest = blob.ExpiresAtUnix
		}
	}
	var expiresIn time.Duration
	if earliest != 0 {
		expiresIn = time.Unix(earliest, 0).Sub(now)
	}
	attrs := []any{
		"entry", number,
		"target", disposition.Target,
		"output", address.OutputPath.String(),
		"part", string(address.Part),
		"outcome", string(outcome),
		"replaced", disposition.Replaced,
		"layers", len(offer.Chain.Layers),
		"bytes", size,
		"expires_in", expiresIn,
		"admit", admit,
	}
	if disposition.Err != nil {
		attrs = append(attrs, "error", disposition.Err.Error())
	}
	return attrs
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
// learn how. The release's error is logged instead. A merge refused for stale
// held rows committed nothing, and is answered with them.
func (a *RemoteCacheAdapter) Merge(ctx context.Context, id uint64, req protocol.Merge) (protocol.Merged, error) {
	if a.stopped.Load() {
		return protocol.Merged{}, ErrRemoteCacheAdapterClosed
	}
	start := time.Now()
	// A merge that answers a lookup goes first: the others wait while one
	// is in progress.
	if req.Lookup != 0 {
		a.lookupMerges.begin()
		defer a.lookupMerges.end()
	} else if err := a.lookupMerges.wait(ctx); err != nil {
		return protocol.Merged{}, err
	}
	priorityWait := time.Since(start)
	mergeCtx, mergeOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.merge", wcprof.OpOpts{})
	reply, err := a.mergeValues(mergeCtx, dagql.CloudCacheID, req.Bundle)
	if reply.Committed {
		mergeOp.End(wcprof.OutcomeOK)
	} else {
		mergeOp.EndErr(err)
	}
	held := 0
	for _, value := range req.Bundle.Values {
		if value.Held != "" {
			held++
		}
	}
	slog.Info("remote cache merge",
		"records", len(req.Bundle.Values),
		"held", held,
		"roots", len(req.Bundle.Roots),
		"committed", reply.Committed,
		"stale", len(reply.Stale),
		"commitHold", reply.CommitHold,
		"duration", time.Since(start),
		"error", err,
		"generation", reply.Generation,
		"engine_time", reply.EngineTimeUnixNano,
		"merge", id,
		"lookup", req.Lookup,
		"priority_wait", priorityWait)
	if err == nil && len(reply.Stale) > 0 {
		return protocol.Merged{Generation: reply.Generation, EngineTime: reply.EngineTimeUnixNano, Values: []protocol.MergedValue{}, Retained: []protocol.RetainedRoot{}, Stale: reply.Stale}, nil
	}
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
	start := time.Now()
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
	for i, disposition := range dispositions {
		outcome, err := protocol.OfferOutcomeOf(disposition.Outcome)
		if err != nil {
			return protocol.OfferedItem{}, err
		}
		// OfferParts answers each offer at its index.
		var offer dagql.PersistedPartOffer
		if len(dispositions) == len(item.Offers) {
			offer = item.Offers[i].Offer
		}
		level := slog.LevelInfo
		if outcome == protocol.OfferInvalid {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "remote cache part offered", offerLogAttrs(item.Number, offer, disposition, outcome, time.Since(start), time.Now())...)
		profOffer(ctx, item.Number, disposition, outcome, start)
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

// profOffer records an offered part's decision, from the offer's arrival to
// now, as a profile op: the part, the row its address resolved to (the
// entry, before it resolved), and an offer.<outcome> fact.
func profOffer(ctx context.Context, number uint64, disposition dagql.OfferDisposition, outcome protocol.OfferOutcome, start time.Time) {
	if !wcprof.Enabled(ctx) {
		return
	}
	address, row := disposition.TargetAddress, disposition.Target
	if row == 0 {
		address, row = disposition.Address, number
	}
	key, _ := json.Marshal(address)
	result := wcprof.OutcomeOK
	if outcome == protocol.OfferInvalid {
		result = wcprof.OutcomeError
	}
	id := wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.offer", wcprof.OpOpts{Ident: string(key), ResultID: row}, start, time.Now(), result)
	wcprof.Fact(ctx, id, "offer."+string(outcome), 0)
}

// profExportOutcome is a capture's profile outcome: a capture that every
// root's busy row stopped gives way, and isn't a failure.
func profExportOutcome(err error) wcprof.Outcome {
	switch {
	case err == nil:
		return wcprof.OutcomeOK
	case errors.Is(err, dagql.ErrPersistStateNotReady):
		return wcprof.OutcomeRefused
	case errors.Is(err, context.Canceled):
		return wcprof.OutcomeCanceled
	}
	return wcprof.OutcomeError
}

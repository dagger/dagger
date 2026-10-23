package dagql

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// Wall-clock profiling (wcprof) hooks for the dagql cache. These are no-ops
// unless profiling is enabled; call sites gate on wcprof.Enabled(ctx) before
// computing class strings so the disabled path stays an atomic load.

// profCallClass derives the operation class for a call frame, e.g.
// "Container.withExec" or "myModule:Foo.bar" for module-provided fields.
func profCallClass(frame *ResultCall) string {
	if frame == nil {
		return "?"
	}
	field := frame.Field
	if field == "" {
		field = frame.SyntheticOp
	}
	if field == "" {
		field = "?"
	}
	recv := "Query"
	if frame.Receiver != nil {
		recv = ""
		if frame.Receiver.Call != nil {
			recv = profNamedType(frame.Receiver.Call.Type)
		}
		if recv == "" && frame.Receiver.shared != nil {
			if rc := frame.Receiver.shared.loadResultCall(); rc != nil {
				recv = profNamedType(rc.Type)
			}
		}
		if recv == "" {
			recv = "?"
		}
	}
	if frame.Module != nil && frame.Module.Name != "" {
		return frame.Module.Name + ":" + recv + "." + field
	}
	return recv + "." + field
}

func profNamedType(typ *ResultCallType) string {
	for typ != nil {
		if typ.NamedType != "" {
			return typ.NamedType
		}
		typ = typ.Elem
	}
	return ""
}

func profClientID(ctx context.Context) string {
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		return md.ClientID
	}
	return ""
}

func profResultID(res AnyResult) uint64 {
	if res == nil {
		return 0
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return 0
	}
	return uint64(shared.id)
}

func profErrOutcome(err error) wcprof.Outcome {
	switch {
	case err == nil:
		return wcprof.OutcomeOK
	case errors.Is(err, context.Canceled):
		return wcprof.OutcomeCanceled
	default:
		return wcprof.OutcomeError
	}
}

// Remote-cache hooks. They read the measurement points the remote cache's log
// lines and span attributes already keep, and record them as intervals.

// partInstalledOutcome is the install outcome for a part that installed; the
// others are partInstallFailure's.
const partInstalledOutcome = "installed"

// profBeginInstall starts the profile op of one attempt to install an offered
// part on receiver, from start, where the install's line times it from: the
// part's key, and a part link with its size, the whole chain's, which is what
// a pull of it costs a cache that has none of it.
func profBeginInstall(ctx context.Context, receiver AnyResult, target PersistedPartAddress, layers []snapshots.ExportLayer, start time.Time) (context.Context, *wcprof.Op) {
	if !wcprof.Enabled(ctx) {
		return ctx, nil
	}
	key, _ := partAddressKey(target)
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.install", wcprof.OpOpts{
		Ident:    key,
		ClientID: profClientID(ctx),
		WorkType: wcprof.WorkTypeExternal,
		Start:    start,
	})
	var size int64
	for _, layer := range layers {
		size += layer.Descriptor.Size
	}
	wcprof.Part(ctx, op.ID(), key, profResultID(receiver), size)
	return ctx, op
}

// profEndInstall ends an install's profile op at now, where the install's
// line times it to, with its outcome (one of partInstallFailure's, or
// partInstalledOutcome) and the bytes it downloaded, after recording its
// commit, from committing, when it got that far. ctx is the op's.
func profEndInstall(ctx context.Context, op *wcprof.Op, receiver AnyResult, outcome string, downloaded int64, committing, now time.Time) {
	if op == nil {
		return
	}
	result := wcprof.OutcomeError
	switch outcome {
	case partInstalledOutcome:
		result = wcprof.OutcomeOK
	case "canceled":
		result = wcprof.OutcomeCanceled
	case "refused":
		result = wcprof.OutcomeRefused
	case "superseded":
		result = wcprof.OutcomeSuperseded
	case "content":
		wcprof.Fact(ctx, op.ID(), "install.content", 0)
	}
	if !committing.IsZero() {
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.install.commit", wcprof.OpOpts{}, committing, now, result)
	}
	op.SetBytes(downloaded)
	op.EndAt(result, profResultID(receiver), now)
}

// profRenewal records a renewal of a part's content addresses, from start to
// end: a round trip to the service, or a wait on another source's.
func profRenewal(ctx context.Context, blob digest.Digest, start, end time.Time, err error) {
	if !wcprof.Enabled(ctx) {
		return
	}
	wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.renew", wcprof.OpOpts{
		Ident:    blob.String(),
		WorkType: wcprof.WorkTypeExternal,
	}, start, end, profErrOutcome(err))
}

// profCopy records a capture's copy of its closure's records, from start to
// end.
func profCopy(ctx context.Context, start, end time.Time, err error) {
	wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.copy", wcprof.OpOpts{}, start, end, profErrOutcome(err))
}

// profBeginChain starts the profile op of opening one exported part's chain,
// which diffs the layers that have no blob yet.
func profBeginChain(ctx context.Context, address PersistedPartAddress) (context.Context, *wcprof.Op) {
	if !wcprof.Enabled(ctx) {
		return ctx, nil
	}
	key, _ := partAddressKey(address)
	return wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.chain", wcprof.OpOpts{Ident: key})
}

// profEndChain ends a chain's op with the result it is a part of and the
// bytes of the blobs it computed: 0 when every layer's blob already existed.
func profEndChain(op *wcprof.Op, chain *snapshots.ExportChain, resultID uint64, err error) {
	if op == nil {
		return
	}
	if chain != nil {
		op.SetBytes(chain.Stats.BlobBytes)
	}
	op.EndWithResult(profErrOutcome(err), resultID)
}

// profExportedParts links each part a bundle offers to its result, with its
// size: its chain's, which is what a pull of it costs a cache that has none
// of it. sources maps the bundle's ordinals to results.
func profExportedParts(ctx context.Context, outputs []TransferredOutput, sources []ImportedValue) {
	if !wcprof.Enabled(ctx) {
		return
	}
	results := make(map[TransferOrdinal]uint64, len(sources))
	for _, source := range sources {
		results[source.Ordinal] = source.ResultID
	}
	for _, output := range outputs {
		if output.Chain == nil {
			continue
		}
		key, err := partAddressKey(output.Address)
		if err != nil {
			continue
		}
		var size int64
		for _, layer := range output.Chain.Layers {
			size += layer.Descriptor.Size
		}
		wcprof.Part(ctx, 0, key, results[output.Ordinal], size)
	}
}

// egraphMuProfIdent names the cache's graph lock in lock waits: lookups,
// publications and an export's walk contend for it.
const egraphMuProfIdent = "dagql.Cache.egraphMu"

// cacheFact is a fact's name: its span attribute's, without the
// dagger.io/cache. prefix, so a profile and a trace say it the same way.
func cacheFact(attr string) string {
	return strings.TrimPrefix(attr, "dagger.io/cache.")
}

// profLookupFacts links the facts of a call's cache decision from its op, as
// its span's cache attributes say them: whether a hit was remote, why a miss
// found nothing it could use (its lost candidates, remote or not, and its new
// inputs), and whether its publication adopted a merged entry. A fact about a
// result names it.
func profLookupFacts(ctx context.Context, op *wcprof.Op, ev *CacheDecision, res AnyResult) {
	if op == nil || ev == nil {
		return
	}
	fact := func(attr string, resultID uint64) {
		wcprof.Fact(ctx, op.ID(), cacheFact(attr), resultID)
	}
	switch ev.Outcome {
	case CacheOutcomeHit:
		if ev.HitRemote {
			fact(telemetryattrs.CacheHitRemoteAttr, profResultID(res))
		}
	case CacheOutcomeExecuted:
		for _, f := range []struct {
			set  bool
			attr string
		}{
			{ev.MissIncompatibleCandidates, telemetryattrs.CacheMissIncompatibleCandidatesAttr},
			{ev.MissSawExpired, telemetryattrs.CacheMissSawExpiredAttr},
			{ev.MissUnknownInputIndex >= 0, telemetryattrs.CacheMissUnknownInputAttr},
			{ev.MissProducerReleased, telemetryattrs.CacheMissProducerReleasedAttr},
			{ev.MissRequirementsGrew, telemetryattrs.CacheMissRequirementsGrewAttr},
			{ev.MissSawExpired && ev.MissExpiredRemote, telemetryattrs.CacheMissExpiredRemoteAttr},
			{ev.MissIncompatibleCandidates && ev.MissIncompatibleRemote, telemetryattrs.CacheMissIncompatibleRemoteAttr},
			{(ev.MissProducerReleased || ev.MissRequirementsGrew) && ev.HitRemote, telemetryattrs.CacheMissChosenRemoteAttr},
			{len(ev.MissFreshInputs) > 0, telemetryattrs.CacheMissFreshInputsAttr},
		} {
			if f.set {
				fact(f.attr, 0)
			}
		}
	}
	if (ev.Outcome == CacheOutcomeExecuted || ev.Outcome == CacheOutcomeJoined) && ev.Adopted {
		fact(telemetryattrs.CacheAdoptedAttr, profResultID(res))
		if ev.AdoptedRemote {
			fact(telemetryattrs.CacheAdoptedRemoteAttr, profResultID(res))
		}
	}
}

// attachHolder is the op and span holding a result's dependency attachment
// open: its publication's, or a merge's that replaces its value.
type attachHolder struct {
	opID uint64
	span trace.SpanContext
}

// newAttachHolder returns the holder of an attachment opened under the op
// opID and the span span, or nil when neither is recorded.
func newAttachHolder(opID uint64, span trace.Span) *attachHolder {
	var sc trace.SpanContext
	if span != nil && span.IsRecording() {
		sc = span.SpanContext()
	}
	if opID == 0 && !sc.IsValid() {
		return nil
	}
	return &attachHolder{opID: opID, span: sc}
}

// attachWait is a publication's wait for an attachment another holds open.
type attachWait struct {
	native  *wcprof.Wait
	span    trace.Span
	target  trace.SpanContext
	startNS int64
}

// beginAttachWait starts the publication's attach wait on holder, read with
// waitCh under the attachment's mutex: on the op in ctx, and as a wait link on
// span when the holder has a span to target. A merge has none, so the trace
// leaves a wait on one as the publication's own time, as before. An
// attachment that has closed since is no wait, and its holder may be gone.
func beginAttachWait(ctx context.Context, span trace.Span, waitCh <-chan struct{}, holder *attachHolder) attachWait {
	var w attachWait
	if !wcprof.Enabled(ctx) && (span == nil || !span.IsRecording()) {
		return w
	}
	select {
	case <-waitCh:
		return w
	default:
	}
	var target uint64
	if holder != nil {
		target, w.target = holder.opID, holder.span
	}
	w.native = wcprof.BeginWait(ctx, target, wcprof.WaitReasonAttach)
	if span != nil && span.IsRecording() && w.target.IsValid() {
		w.span = span
		w.startNS = time.Now().UnixNano()
	}
	return w
}

// end records the wait up to now.
func (w attachWait) end(ctx context.Context) {
	w.native.End()
	if w.span != nil {
		EmitOTelWait(trace.ContextWithSpan(ctx, w.span), w.target, wcprof.WaitReasonAttach, w.startNS, time.Now().UnixNano())
	}
}

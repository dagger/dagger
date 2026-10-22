package dagql

import (
	"context"
	"errors"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots"
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

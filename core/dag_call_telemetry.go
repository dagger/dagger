package core

import (
	"context"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Call payloads over the log channel fill the gaps left by span attributes.
//
// This file is the producer half of the CallPayloadContentType contract
// (engine/telemetryattrs), modelled on the agent-state producer next door for
// the same structural reason: a span attribute can only describe a span that
// exists. A client rebuilding a call ID needs a payload for EVERY frame the
// chain references, and whole classes of frame never get a span of their own —
// introspection / isMeta / skipped selections, digests the per-session span
// dedupe already spent, the frames inside an ID-literal argument (LiteralID.pb
// flattens them to a bare digest), and array members that are only ever
// sub-selected.
//
// Calls that do get a recording span carry dagger.io/dag.call there. Both
// transports claim digests from the same delivery-domain store, so the closure
// walk below publishes logs only for frames a span did not already deliver.
//
// The claim store is scoped to the emitting client's DELIVERY DOMAIN — the
// client and its ancestors, exactly the per-client DBs telemetry fans out to
// (Query.CallPayloadSeenKeyStore) — NOT to the session. A session-wide claim
// let one client's emission permanently satisfy the claim for clients that
// never received it: a client attaching to the session later (e.g. a nested
// `dagger agent`) could then never obtain the payloads for frames claimed
// before it existed, leaving every agent whose chain crossed such a frame
// unaddressable there. Delivery-domain claims mean a later client's first
// closure walk re-publishes into its own domain; consumers dedupe by digest,
// so the bounded re-publication is harmless.

// recordCallPayloads publishes the missing frames in the transitive closure of
// a call's ID over the log channel — through receivers, modules, arguments (ID
// literals inside lists and objects included) and implicit inputs — minus
// digests already delivered by a span or log in the client's delivery domain.
//
// rootOnSpan means the caller already claimed the root for a recording span;
// the walk skips its log but still visits the closure. Otherwise this function
// claims the root itself and emits it as a log. A previously claimed root
// short-circuits the whole walk: reachability is transitive, so its first claim
// already accompanied a walk that delivered every then-missing frame.
//
// Everything here is best-effort. A payload that cannot be built or encoded is
// dropped rather than failing the call; the consequence is a client that
// cannot rebuild that one chain, which is exactly the status quo.
func recordCallPayloads(
	ctx context.Context,
	store dagql.TelemetrySeenKeyStore,
	callDigest string,
	frame *dagql.ResultCall,
	rootOnSpan bool,
) {
	if store == nil || frame == nil {
		return
	}
	if !rootOnSpan && !dagql.ShouldEmitCallPayload(store, callDigest) {
		// Someone already published this call's payload, and whoever did also
		// walked its closure — reachability is transitive, so that walk
		// covered everything this one would.
		return
	}

	id, err := frame.RecipeID(ctx)
	if err != nil {
		// Debug, not Warn: a recipe that cannot be rebuilt (a handle-form
		// reference to a shared result, a frame the cache no longer holds)
		// is a known shape, and this runs once per distinct call digest —
		// warning would mean thousands of identical lines for one bad chain.
		slog.DebugContext(ctx, "failed to rebuild recipe ID for call payloads", "digest", callDigest, "err", err)
		return
	}
	dagPB, err := id.ToProto()
	if err != nil {
		slog.DebugContext(ctx, "failed to build call payload DAG", "digest", callDigest, "err", err)
		return
	}
	// A handle-form ID is an engine-local result reference, not a recipe:
	// there are no frames to publish and no client could replay them anyway.
	recipe := dagPB.GetRecipe()
	if recipe == nil {
		return
	}

	logger := telemetry.Logger(ctx, InstrumentationLibrary)
	emit := func(dgst string, callPB *callpbv1.Call) {
		if dgst == callDigest {
			// The root was claimed before rebuilding. When its payload rode the
			// span, only its closure needs the log fallback.
			if rootOnSpan {
				return
			}
		} else {
			// Claim every other frame before encoding so repeated closure walks
			// skip payloads already delivered by either transport.
			if !dagql.ShouldEmitCallPayload(store, dgst) {
				return
			}
		}

		// The payload carries its own digest: it is the key the producer files
		// the frame under everywhere else (span attributes, other frames'
		// references), so consumers use it verbatim rather than re-deriving it
		// and coupling themselves to this engine version's digest scheme.
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(callPB)
		if err != nil {
			slog.WarnContext(ctx, "failed to marshal call payload", "digest", dgst, "err", err)
			return
		}
		rec := log.Record{}
		rec.SetTimestamp(time.Now())
		rec.SetBody(log.BytesValue(payload))
		rec.AddAttributes(log.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
		logger.Emit(ctx, rec)
	}

	// Emit the requested root first so consumers see the frame they asked for
	// before the rest of its closure.
	calls := recipe.GetCallsByDigest()
	rootDigest := recipe.GetRootDigest()
	if root := calls[rootDigest]; root != nil {
		emit(rootDigest, root)
	}
	for dgst, callPB := range calls {
		if dgst != rootDigest {
			emit(dgst, callPB)
		}
	}
}

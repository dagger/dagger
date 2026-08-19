package core

import (
	"context"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Call payloads over the log channel: the sole transport for newly emitted
// replayable call data.
//
// This file is the producer half of the dagger.io/dag.call.payload.* contract
// (engine/telemetryattrs), modelled on the agent-state producer next door for
// the same structural reason: a span attribute can only describe a span that
// exists. A client rebuilding a call ID needs a payload for EVERY frame the
// chain references, and whole classes of frame never get a span of their own —
// introspection / isMeta / skipped selections, digests the per-session span
// dedupe already spent, the frames inside an ID-literal argument (LiteralID.pb
// flattens them to a bare digest), and array members that are only ever
// sub-selected.
//
// The legacy span attribute dagger.io/dag.call remains readable by consumers,
// but producers no longer write it. Root and transitive frames share this one
// log transport and claim digests from the same delivery-domain store.
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

// CallPayloadInstrumentationScope names the logger emitting call payloads.
const CallPayloadInstrumentationScope = "dagger.io/dag.call"

// recordCallPayloads publishes the transitive closure of a call's ID over the
// log channel — every frame the chain references, through receivers, modules,
// arguments (ID literals inside lists and objects included) and implicit
// inputs — minus digests already claimed in the client's delivery domain.
//
// It is called after the call's span starts and claims the root digest before
// rebuilding the recipe. A second selection of that root short-circuits the
// whole walk; reachability is transitive, so the first walk already published
// every frame in its closure. This makes the cost at most one closure walk per
// distinct root digest in the emitting client's delivery domain.
//
// Everything here is best-effort. A payload that cannot be built or encoded is
// dropped rather than failing the call; the consequence is a client that
// cannot rebuild that one chain, which is exactly the status quo.
func recordCallPayloads(
	ctx context.Context,
	store dagql.TelemetrySeenKeyStore,
	callDigest string,
	frame *dagql.ResultCall,
) {
	if store == nil || frame == nil {
		return
	}
	if !dagql.ShouldEmitCallPayload(store, callDigest) {
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

	logger := telemetry.Logger(ctx, CallPayloadInstrumentationScope)
	for dgst, callPB := range recipe.GetCallsByDigest() {
		// The root was claimed before rebuilding. Claim every other frame before
		// encoding so repeated closure walks skip work already delivered.
		if dgst != callDigest && !dagql.ShouldEmitCallPayload(store, dgst) {
			continue
		}
		payload, err := callPB.Encode()
		if err != nil {
			slog.WarnContext(ctx, "failed to encode call payload", "digest", dgst, "err", err)
			continue
		}
		rec := log.Record{}
		rec.SetTimestamp(time.Now())
		// Explicit empty body: an unset body does not survive the OTLP
		// round-trip, and consumers skip empty-bodied records as text — this
		// record is call data, not output. (Same contract as EmitAgentState.)
		rec.SetBody(log.StringValue(""))
		rec.AddAttributes(
			log.String(telemetryattrs.DagCallPayloadDigestAttr, dgst),
			log.String(telemetryattrs.DagCallPayloadAttr, payload),
		)
		logger.Emit(ctx, rec)
	}
}

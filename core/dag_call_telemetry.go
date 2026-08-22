package core

import (
	"context"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call/callpbv1"
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
// The delivery state is scoped per target — the client and its ancestors,
// exactly the per-client DBs telemetry fans out to — NOT to the session. A
// session-wide decision could let one client's emission permanently satisfy a
// client that never received it. Producer checks avoid needless recipe work;
// the session log exporter atomically claims only the missing route targets.

// CallPayloadInstrumentationScope names the logger emitting call payloads.
const CallPayloadInstrumentationScope = "dagger.io/dag.call"

// recordCallPayloads publishes the transitive closure of a call's ID over the
// log channel — every frame the chain references, through receivers, modules,
// arguments (ID literals inside lists and objects included) and implicit
// inputs — minus digests already claimed in the client's delivery domain.
//
// It is called even when presentation-span deduplication suppresses the call,
// and first checks whether the root is missing anywhere on the route before
// rebuilding the recipe. The check is only an optimization: sessionLogExporter
// atomically claims the exact missing targets for each emitted record, so
// concurrent closure walks remain safe.
//
// Everything here is best-effort. A payload that cannot be built or encoded is
// dropped rather than failing the call; the consequence is a client that
// cannot rebuild that one chain, which is exactly the status quo.
func recordCallPayloads(
	ctx context.Context,
	store dagql.CallPayloadSeenKeyStore,
	callDigest string,
	frame *dagql.ResultCall,
) {
	if store == nil || frame == nil {
		return
	}
	if !store.CallPayloadNeedsEmission(callDigest) {
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
	emit := func(dgst string, callPB *callpbv1.Call) {
		// The root was checked before rebuilding. Check every other frame before
		// encoding so closure walks avoid work already delivered everywhere.
		if dgst != callDigest && !store.CallPayloadNeedsEmission(dgst) {
			return
		}
		payload, err := callPB.Encode()
		if err != nil {
			slog.WarnContext(ctx, "failed to encode call payload", "digest", dgst, "err", err)
			return
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

	// Emit the requested root first so it is never delayed behind a closure
	// larger than the fast payload transport's bounded exporter batch.
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

package core

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

func AroundFunc(ctx context.Context, self dagql.Object, id *call.ID) (context.Context, func(res dagql.Typed, cached bool, rerr error)) {
	if isIntrospection(id) {
		return ctx, dagql.NoopDone
	}

	var base string
	if id.Receiver() == nil {
		base = "Query"
	} else {
		base = id.Receiver().Type().ToAST().Name()
	}
	spanName := fmt.Sprintf("%s.%s", base, id.Field())

	callAttr, err := id.Call().Encode()
	if err != nil {
		slog.Warn("failed to encode call", "id", id.Display(), "err", err)
		return ctx, dagql.NoopDone
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.DagDigestAttr, id.Digest().String()),
		attribute.String(telemetry.DagCallAttr, callAttr),
	}
	if idInputs, err := id.Inputs(); err != nil {
		slog.Warn("failed to compute inputs(id)", "id", id.Display(), "err", err)
	} else {
		inputs := make([]string, len(idInputs))
		for i, input := range idInputs {
			inputs[i] = input.String()
		}
		attrs = append(attrs, attribute.StringSlice(telemetry.DagInputsAttr, inputs))
	}

	if isInternal(ctx, id) {
		attrs = append(attrs, attribute.Bool(telemetry.UIInternalAttr, true))
	}

	ctx, span := telemetry.Tracer(ctx, InstrumentationLibrary).
		Start(ctx, spanName, trace.WithAttributes(attrs...))

	return ctx, func(res dagql.Typed, cached bool, err error) {
		defer telemetry.End(span, func() error { return err })

		if cached {
			// TODO maybe this should be an event?
			span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))
		}

		if ctx.Err() != nil {
			// If the request was canceled, reflect it on the span.
			span.SetAttributes(attribute.Bool(telemetry.CanceledAttr, true))
		}

		if err != nil {
			// NB: we do +id.Display() instead of setting it as a field to avoid
			// double quoting
			slog.Warn("error resolving "+id.Display(), "error", err)
		}

		// Record an object result as an output of this call.
		//
		// This allows the UI to "simplify" the returned object's ID back to the
		// current call's ID, so we can show the user myMod().unit().stdout()
		// instead of container().from().[...].stdout().
		if obj, ok := res.(dagql.Object); ok {
			// Don't consider loadFooFromID to be a 'creator' as that would only
			// obfuscate the real ID.
			//
			// NB: so long as the simplifying process rejects larger IDs, this
			// shouldn't be necessary, but it seems like a good idea to just never even
			// consider it.
			isLoader := strings.HasPrefix(id.Field(), "load") && strings.HasSuffix(id.Field(), "FromID")
			if !isLoader {
				objDigest := obj.ID().Digest()
				span.SetAttributes(attribute.String(telemetry.DagOutputAttr, objDigest.String()))
			}
		}

		// record which LLB op is the singular result of this call
		//
		// previously we would emit all LLB digests and call it "done" based on the
		// status across all of them, but this backfires for withExec, since it
		// also includes a def for each mutated mount point, and those might never
		// be referenced.
		if hasPBs, ok := res.(HasPBOutput); ok {
			if def, err := hasPBs.PBOutput(ctx); err != nil {
				slog.Warn("failed to get LLB output", "err", err)
			} else if def != nil { // may be nil for scratch
				lastOpDigest := digest.FromBytes(def.Def[len(def.Def)-1])
				span.SetAttributes(
					attribute.String(telemetry.EffectOutputAttr, lastOpDigest.String()))
			}
		}
	}
}

// isInternal detects whether a call's span should be marked internal.
func isInternal(ctx context.Context, id *call.ID) bool {
	return dagql.IsInternal(ctx) || // dagql.Select call
		id.Field() == "sync" // sync is less interesting than the things it runs
}

// isIntrospection detects whether an ID is an introspection query.
//
// These queries tend to be very large and are not interesting for users to
// see.
func isIntrospection(id *call.ID) bool {
	if id.Receiver() == nil {
		switch id.Field() {
		case "__schema",
			"__schemaVersion",
			"currentTypeDefs",
			"currentFunctionCall",
			"currentModule":
			return true
		default:
			return false
		}
	} else {
		return isIntrospection(id.Receiver())
	}
}

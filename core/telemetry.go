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
	if id.Base() == nil {
		base = "Query"
	} else {
		base = id.Base().Type().ToAST().Name()
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
	if dagql.IsInternal(ctx) {
		attrs = append(attrs, attribute.Bool(telemetry.UIInternalAttr, true))
	}

	ctx, span := dagql.Tracer().Start(ctx, spanName, trace.WithAttributes(attrs...))

	return ctx, func(res dagql.Typed, cached bool, err error) {
		defer telemetry.End(span, func() error { return err })

		if cached {
			// TODO maybe this should be an event?
			span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))
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

		// Record any LLB op digests that the value depends on.
		//
		// This allows the UI to track the 'cause and effect' between lazy
		// operations and their eventual execution. The listed digests will be
		// correlated to spans coming from Buildkit which set the matching digest
		// as the 'vertex' span attribute.
		if hasPBs, ok := res.(HasPBDefinitions); ok {
			if defs, err := hasPBs.PBDefinitions(ctx); err != nil {
				slog.Warn("failed to get LLB definitions", "err", err)
			} else {
				seen := make(map[digest.Digest]bool)
				var ops []string
				for _, def := range defs {
					for _, op := range def.Def {
						dig := digest.FromBytes(op)
						if seen[dig] {
							continue
						}
						seen[dig] = true
						ops = append(ops, dig.String())
					}
				}
				span.SetAttributes(attribute.StringSlice(telemetry.EffectIDsAttr, ops))
			}
		}
	}
}

// isIntrospection detects whether an ID is an introspection query.
//
// These queries tend to be very large and are not interesting for users to
// see.
func isIntrospection(id *call.ID) bool {
	if id.Base() == nil {
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
		return isIntrospection(id.Base())
	}
}

func serviceEffect(id *call.ID) string {
	return id.Digest().String() + "-service"
}

// Tell telemetry about the associated effect for when the service starts.
func connectServiceEffect(ctx context.Context) {
	trace.SpanFromContext(ctx).SetAttributes(attribute.StringSlice(telemetry.EffectIDsAttr, []string{serviceEffect(dagql.CurrentID(ctx))}))
}

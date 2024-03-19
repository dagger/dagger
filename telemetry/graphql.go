package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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
		attribute.String(DagDigestAttr, id.Digest().String()),
		attribute.String(DagCallAttr, callAttr),
	}
	if idInputs, err := id.Inputs(); err != nil {
		slog.Warn("failed to compute inputs(id)", "id", id.Display(), "err", err)
	} else {
		inputs := make([]string, len(idInputs))
		for i, input := range idInputs {
			inputs[i] = input.String()
		}
		attrs = append(attrs, attribute.StringSlice(DagInputsAttr, inputs))
	}
	if dagql.IsInternal(ctx) {
		attrs = append(attrs, attribute.Bool(InternalAttr, true))
	}

	ctx, span := dagql.Tracer().Start(ctx, spanName, trace.WithAttributes(attrs...))
	ctx, _, _ = WithStdioToOtel(ctx, dagql.InstrumentationLibrary)

	return ctx, func(res dagql.Typed, cached bool, err error) {
		defer End(span, func() error { return err })

		if cached {
			// TODO maybe this should be an event?
			span.SetAttributes(attribute.Bool(CachedAttr, true))
		}

		if err != nil {
			// NB: we do +id.Display() instead of setting it as a field to avoid
			// dobule quoting
			slog.Warn("error resolving "+id.Display(), "error", err)
		}

		// don't consider loadFooFromID to be a 'creator' as that would only
		// obfuscate the real ID.
		//
		// NB: so long as the simplifying process rejects larger IDs, this
		// shouldn't be necessary, but it seems like a good idea to just never even
		// consider it.
		isLoader := strings.HasPrefix(id.Field(), "load") && strings.HasSuffix(id.Field(), "FromID")

		// record an object result as an output of this vertex
		//
		// this allows the UI to "simplify" this ID back to its creator ID when it
		// sees it in the future if it wants to, e.g. showing mymod.unit().stdout()
		// instead of the full container().from().[...].stdout() ID
		if obj, ok := res.(dagql.Object); ok && !isLoader {
			objDigest := obj.ID().Digest()
			span.SetAttributes(attribute.String(DagOutputAttr, objDigest.String()))
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

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func collectDefs(ctx context.Context, val dagql.Typed) []*pb.Definition {
	if hasPBs, ok := dagql.UnwrapAs[HasPBDefinitions](val); ok {
		if defs, err := hasPBs.PBDefinitions(ctx); err != nil {
			slog.Warn("failed to get LLB definitions", "err", err)
			return nil
		} else {
			return defs
		}
	}
	return nil
}

func AroundFunc(ctx context.Context, self dagql.Object, id *call.ID) (context.Context, func(res dagql.Typed, cached bool, rerr error)) {
	if isIntrospection(id) || isMeta(id) {
		// very uninteresting spans
		return ctx, dagql.NoopDone
	}
	if DagOpInContext[RawDagOp](ctx) || DagOpInContext[FSDagOp](ctx) || DagOpInContext[ContainerDagOp](ctx) {
		// dagops are all self calls, no need to emit additional spans here
		// FIXME: we lose telemetry.SpanStdio info from here
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

	if dagql.IsInternal(ctx) {
		attrs = append(attrs, attribute.Bool(telemetry.UIInternalAttr, true))
	}

	ctx, span := Tracer(ctx).Start(ctx, spanName, trace.WithAttributes(attrs...))

	return ctx, func(res dagql.Typed, cached bool, err error) {
		defer telemetry.End(span, func() error {
			if err != nil {
				return errors.New(unwrapError(err))
			}
			return nil
		})
		recordStatus(ctx, res, span, cached, err, id)
		logResult(ctx, res, self, id)
		collectEffects(ctx, res, span, self)
	}
}

// recordStatus records the status of a call on a span.
func recordStatus(ctx context.Context, res dagql.Typed, span trace.Span, cached bool, err error, id *call.ID) {
	if cached {
		span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))
	}

	if ctx.Err() != nil {
		// If the request was canceled, reflect it on the span.
		span.SetAttributes(attribute.Bool(telemetry.CanceledAttr, true))
	}

	// Record an object result as an output of this call.
	//
	// This allows the UI to "simplify" the returned object's ID back to the
	// current call's ID, so we can show the user myMod().unit().stdout()
	// instead of container().from().[...].stdout().
	if obj, ok := dagql.UnwrapAs[dagql.Object](res); ok {
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

	if err == nil {
		// It is important to set an Ok status here so functions can encapsulate
		// any internal errors.
		span.SetStatus(codes.Ok, "")
	} else {
		// append id.Display() instead of setting it as a field to avoid double
		// quoting
		slog.Warn("error resolving "+id.Display(), "error", err)
	}
}

// logResult prints the result of a call to the span's stdout.
func logResult(ctx context.Context, res dagql.Typed, self dagql.Object, id *call.ID) {
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	fieldSpec, ok := self.ObjectType().FieldSpec(id.Field(), dagql.View(id.View()))
	if !ok {
		return
	}
	if fieldSpec.Sensitive {
		// Take care not to print any sensitive values.
		return
	}
	if str, ok := dagql.UnwrapAs[dagql.String](res); ok {
		fmt.Fprint(stdio.Stdout, str)
	} else if lit, ok := dagql.UnwrapAs[call.Literate](res); ok {
		fmt.Fprint(stdio.Stdout, lit.ToLiteral().Display())
	}
}

// collectEffects records LLB op digests installed by this call so that we can
// know that it has pending work.
//
// Effects will become complete as spans appear from Buildkit with a
// corresponding effect ID.
func collectEffects(ctx context.Context, res dagql.Typed, span trace.Span, self dagql.Object) {
	// Keep track of which effects were already installed prior to the call so we
	// only see new ones.
	seenEffects := make(map[digest.Digest]bool)
	for _, def := range collectDefs(ctx, self) {
		for _, op := range def.Def {
			seenEffects[digest.FromBytes(op)] = true
		}
	}

	var effectIDs []string
	for _, def := range collectDefs(ctx, res) {
		for _, opBytes := range def.Def {
			dig := digest.FromBytes(opBytes)
			if seenEffects[dig] {
				continue
			}
			seenEffects[dig] = true

			var pbOp pb.Op
			err := pbOp.Unmarshal(opBytes)
			if err != nil {
				slog.Warn("failed to unmarshal LLB", "err", err)
				continue
			}
			if pbOp.Op == nil {
				// The last def should always be an empty op with the previous as
				// an input. We never actually see a span for this, so skip it,
				// otherwise the span will look like it still has pending
				// effects.
				continue
			}

			effectIDs = append(effectIDs, dig.String())
		}
	}
	if len(effectIDs) > 0 {
		span.SetAttributes(attribute.StringSlice(telemetry.EffectIDsAttr, effectIDs))
	}
}

// isIntrospection detects whether an ID is an introspection query.
//
// These queries tend to be very large and are not interesting for users to
// see.
func isIntrospection(id *call.ID) bool {
	if id.Receiver() == nil {
		switch id.Field() {
		case "__schema",
			"__schemaJSONFile",
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

// isMeta returns true if any type in the ID is "too meta" to show to the user,
// for example span and error APIs.
func isMeta(id *call.ID) bool {
	if anyReturns(id, "Error") {
		return true
	}
	switch id.Field() {
	case
		// Seeing loadFooFromID is only really interesting if it actually
		// resulted in evaluating the ID, so we don't need to give it its own
		// span.
		fmt.Sprintf("load%sFromID", id.Type().NamedType()),
		// We also don't care about seeing the id field selection itself,
		// since it's more noisy and confusing than helpful. We'll still show
		// all the spans leading up to it, just not the ID selection.
		"id",
		// We don't care about seeing the sync span itself - all relevant
		// info should show up somewhere more familiar.
		"sync":
		return true
	default:
		return false
	}
}

// anyReturns returns true if the call or any of its ancestors return any of
// the given types.
func anyReturns(id *call.ID, types ...string) bool {
	ret := id.Type().NamedType()
	if slices.Contains(types, ret) {
		return true
	}
	if id.Receiver() != nil {
		return anyReturns(id.Receiver(), types...)
	} else {
		return false
	}
}

// trim down unnecessary details from the error; we don't want to pollute
// telemetry errors with large chains like container.withExec.withExec since
// that info is better represented by the trace itself
func unwrapError(rerr error) string {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		return gqlErr.Message
	}
	return rerr.Error()
}

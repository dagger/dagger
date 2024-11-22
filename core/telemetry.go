package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func collectDefs(ctx context.Context, val dagql.Typed) []*pb.Definition {
	if val, ok := val.(dagql.Wrapper); ok {
		return collectDefs(ctx, val.Unwrap())
	}
	if hasPBs, ok := val.(HasPBDefinitions); ok {
		if defs, err := hasPBs.PBDefinitions(ctx); err != nil {
			slog.Warn("failed to get LLB definitions", "err", err)
			return nil
		} else {
			return defs
		}
	}
	return nil
}

func unwrapError(rerr error) string {
	var execErr *dagger.ExecError
	if errors.As(rerr, &execErr) {
		return execErr.Message()
	}
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		return gqlErr.Message
	}
	return rerr.Error()
}

func AroundFunc(ctx context.Context, self dagql.Object, id *call.ID) (context.Context, func(res dagql.Typed, cached bool, rerr error)) {
	if isIntrospection(id) {
		return ctx, dagql.NoopDone
	}

	// Keep track of which effects were already installed prior to the call so we
	// only see new ones.
	seenEffects := make(map[digest.Digest]bool)
	for _, def := range collectDefs(ctx, self) {
		for _, op := range def.Def {
			seenEffects[digest.FromBytes(op)] = true
		}
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

	ctx, span := telemetry.Tracer(ctx, InstrumentationLibrary).
		Start(ctx, spanName, trace.WithAttributes(attrs...))

	return ctx, func(res dagql.Typed, cached bool, err error) {
		defer telemetry.End(span, func() error {
			if err != nil {
				return errors.New(unwrapError(err) + "\n\n" + InspectError(err))
			}
			return nil
		})

		if cached {
			// NOTE: this is never actually called on cache hits, but might be in the
			// future.
			span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))
		}

		if ctx.Err() != nil {
			// If the request was canceled, reflect it on the span.
			span.SetAttributes(attribute.Bool(telemetry.CanceledAttr, true))
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

		// Record LLB op digests installed by this call so that we can know that it
		// has pending work.
		//
		// Effects will become complete as spans appear from Buildkit with a
		// corresponding effect ID.
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

// InspectError deeply analyzes an error, unwrapping all layers and printing
// detailed information about each error in the chain. It returns a string
// containing the full error analysis.
func InspectError(err error) string {
	if err == nil {
		return "nil error"
	}

	var builder strings.Builder
	builder.WriteString("Error Chain Analysis:\n")

	// Track depth for indentation
	depth := 0

	// Examine current error
	current := err
	for current != nil {
		// Create indentation based on depth
		indent := strings.Repeat("  ", depth)

		// Get error type
		errType := reflect.TypeOf(current)
		if errType.Kind() == reflect.Ptr {
			errType = errType.Elem()
		}

		// Write error information
		fmt.Fprintf(&builder, "%s→ Type: %v\n", indent, errType)
		fmt.Fprintf(&builder, "%s  Message: %v\n", indent, current.Error())

		// Check for additional error details
		if unwrapped := errors.Unwrap(current); unwrapped != nil {
			current = unwrapped
			depth++
		} else {
			// Check for multiple wrapped errors using errors.As
			switch v := current.(type) {
			case interface{ Unwrap() []error }:
				if errs := v.Unwrap(); len(errs) > 0 {
					fmt.Fprintf(&builder, "%s  Multiple wrapped errors found (%d):\n", indent, len(errs))
					for i, e := range errs {
						depth++
						fmt.Fprintf(&builder, "%s  Branch %d:\n", indent, i+1)
						fmt.Fprintf(&builder, "%s%s", indent, InspectError(e))
						depth--
					}
				}
			}
			break
		}
	}

	return builder.String()
}

package core

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry"
	"github.com/opencontainers/go-digest"
	"github.com/zeebo/xxh3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

func (q *Query) AroundFunc(ctx context.Context, self dagql.Object, id *call.ID) (context.Context, func(res dagql.Typed, cached bool, rerr error)) {
	if isIntrospection(id) {
		return ctx, dagql.NoopDone
	}

	var base string
	if id.Base() == nil {
		base = q.Type().Name()
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
		attribute.String(telemetry.DagDigestStableAttr, stableDigest(id).String()),
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

	if mod := id.Module(); mod != nil {
		// TODO: unfortunately, in many cases the following is not very useful.
		//
		// When running a local module, as is commonly the case in CI, the module
		// ref will be something like '.', and the module digest will be something
		// derived from that - not exactly universally identifying.
		//
		// That leaves us with just the module name, which might even just be
		// something dumb like 'ci', though hopefully it's closer to a project name.
		//
		// To resolve this, we could perhaps add a 'module GUID' to dagger.json, or
		// perhaps expand '.' into a fully qualified ref based on the git repo's
		// origin from the working directory.
		attrs = append(attrs,
			attribute.String(telemetry.DagModuleNameAttr, mod.Name()),
			attribute.String(telemetry.DagModuleRefAttr, mod.Ref()),
			attribute.String(telemetry.DagModuleDigestAttr, id.Module().ID().Digest().String()),
		)
	}

	// Record the call type.
	var callerType string
	if _, err := q.CurrentModule(ctx); err == nil {
		callerType = "module"
	} else if dagql.IsInternal(ctx) {
		callerType = "internal"
	} else {
		callerType = "direct"
	}
	attrs = append(attrs, attribute.String(telemetry.DagCallerType, callerType))

	ctx, span := dagql.Tracer().Start(ctx, spanName, trace.WithAttributes(attrs...))
	ctx, _, _ = telemetry.WithStdioToOtel(ctx, dagql.InstrumentationLibrary)

	// Save the query span so we can extend it later with module function
	// call metadata (e.g. caller type).
	// ctx = telemetry.WithQuerySpan(ctx, span)

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
				log.Println("!!! SETTING LLB DIGESTS", spanName, telemetry.LLBDigestsAttr, ops)
				span.SetAttributes(attribute.StringSlice(telemetry.LLBDigestsAttr, ops))
			}
		} else if spanName == "Bass.unit" {
			log.Println("!!! BASS UNIT DOES NOT HAVE PB DEFS??", fmt.Sprintf("%T", res))
		}

		if spanName == "Bass.unit" {
			log.Println("!!! BASS UNIT PB DEFS?", fmt.Sprintf("%T", res))
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

func stableDigest(id *call.ID) digest.Digest {
	cp := proto.Clone(id.Call()).(*callpbv1.Call)

	// Nullify any embedded calls from the args.
	nullifyArgs(cp)

	// Keep the module name, but drop the digest and ref.
	if cp.Module != nil {
		cp.Module.CallDigest = ""
		cp.Module.Ref = ""
	}

	// Stabilize the receiver digest.
	if cp.ReceiverDigest != "" {
		cp.ReceiverDigest = stableDigest(id.Base()).String()
	}

	// Unset the digest, since we're calculating the stable one.
	cp.Digest = ""

	// Hash the stabilized value.
	pbBytes, err := proto.MarshalOptions{
		Deterministic: true,
	}.Marshal(cp)
	if err != nil {
		panic(err)
	}
	h := xxh3.New()
	if _, err := h.Write(pbBytes); err != nil {
		panic(err)
	}

	return digest.Digest(fmt.Sprintf("xxh3:%x", h.Sum(nil)))
}

func nullifyArgs(call *callpbv1.Call) {
	for _, arg := range call.Args {
		nullifyCalls(arg.Value)
	}
}

func nullifyCalls(lit *callpbv1.Literal) {
	switch lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		lit.Value = &callpbv1.Literal_Null{
			Null: true,
		}
	case *callpbv1.Literal_List:
		for _, lit := range lit.GetList().Values {
			nullifyCalls(lit)
		}
	case *callpbv1.Literal_Object:
		for _, lit := range lit.GetObject().Values {
			nullifyCalls(lit.Value)
		}
	}
}

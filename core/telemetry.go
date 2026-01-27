package core

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

const (
	// name of arg indicating whether the op should execute the "unlazy" dagop impl
	IsDagOpArgName = "isDagOp"
)

func collectDefs(ctx context.Context, val dagql.AnyResult) []*pb.Definition {
	if hasPBs, ok := dagql.UnwrapAs[HasPBDefinitions](val); ok {
		ctx := dagql.ContextWithID(ctx, val.ID())
		if defs, err := hasPBs.PBDefinitions(ctx); err != nil {
			slog.WarnContext(ctx, "failed to get LLB definitions", "err", err)
			return nil
		} else {
			return defs
		}
	}
	return nil
}

var _ dagql.AroundFunc = AroundFunc

func AroundFunc(
	ctx context.Context,
	self dagql.AnyObjectResult,
	id *call.ID,
) (
	context.Context,
	func(res dagql.AnyResult, cached bool, rerr *error),
) {
	if dagql.IsSkipped(ctx) || isIntrospection(ctx, id) || isMeta(id) || isDagOp(id) {
		// introspection+meta are very uninteresting spans
		// dagops are all self calls, no need to emit additional spans here
		return ctx, dagql.NoopDone
	}

	var base string
	if id.Receiver() == nil {
		base = "Query"
	} else {
		base = id.Receiver().Type().ToAST().Name()
	}
	spanName := fmt.Sprintf("%s.%s", base, id.Field())

	slog.InfoContext(ctx, "start call",
		"field", spanName,
		"digest", id.Digest().String(),
	)

	callAttr, err := id.Call().Encode()
	if err != nil {
		slog.WarnContext(ctx, "failed to encode call", "id", id.DisplaySelf(), "err", err)
		return ctx, dagql.NoopDone
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.DagDigestAttr, id.Digest().String()),
		attribute.String(telemetry.DagCallAttr, callAttr),
	}

	// if inside a module call, add call trace metadata. this is useful
	// since within a single span, we can correlate the caller's and callee's
	// module and functions calls
	if q, err := CurrentQuery(ctx); id.Call().Module != nil && err == nil {
		callerRef, calleeRef := parseCallerCalleeRefs(ctx, q, id)

		if callerRef != nil && calleeRef != nil {
			var callerRefAttr string
			if len(callerRef.ref) > 0 {
				callerRefAttr = fmt.Sprintf("%s@%s", callerRef.ref, callerRef.version)
			}

			callerFn := callerRef.functionName
			calleeFn := calleeRef.functionName

			if callerRef.typeName != "" {
				callerFn = fmt.Sprintf("%s.%s", callerRef.typeName, callerRef.functionName)
			}

			if calleeRef.typeName != "" {
				calleeFn = fmt.Sprintf("%s.%s", calleeRef.typeName, calleeRef.functionName)
			}
			attrs = append(attrs,
				attribute.String(telemetry.ModuleRefAttr, fmt.Sprintf("%s@%s", calleeRef.ref, calleeRef.version)),
				attribute.String(telemetry.ModuleFunctionCallNameAttr, calleeFn),
				attribute.String(telemetry.ModuleCallerRefAttr, callerRefAttr),
				attribute.String(telemetry.ModuleCallerFunctionCallNameAttr, callerFn),
			)
		}
	}

	if idInputs, err := id.Inputs(); err != nil {
		slog.WarnContext(ctx, "failed to compute inputs(id)", "id", id.DisplaySelf(), "err", err)
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

	return ctx, func(res dagql.AnyResult, cached bool, err *error) {
		slog.InfoContext(ctx, "end call",
			"field", spanName,
			"digest", id.Digest().String(),
		)

		defer telemetry.EndWithCause(span, err)
		recordStatus(ctx, res, span, cached, id)
		logResult(ctx, res, self, id)
		collectEffects(ctx, res, span, self)
	}
}

type moduleCallRef struct {
	ref          string
	version      string
	functionName string
	typeName     string
}

func parseCallerCalleeRefs(ctx context.Context, q *Query, callID *call.ID) (*moduleCallRef, *moduleCallRef) {
	cm, _ := q.MainClientCallerMetadata(ctx)
	fc, _ := q.CurrentFunctionCall(ctx)
	m, _ := q.CurrentModule(ctx)
	sd, _ := q.CurrentServedDeps(ctx)

	call := callID.Call()
	calleeModule := call.Module

	callerRef, calleeRef := &moduleCallRef{}, &moduleCallRef{}
	// there's a caller

	var ms *ModuleSource
	if m != nil {
		ms = m.GetSource()
	} else if m, ok := sd.LookupDep(calleeModule.Name); ok {
		ms = m.GetSource()
	}

	if ms == nil {
		return nil, nil
	}

	if fc != nil {
		callerRef.functionName = fc.Name
		callerRef.typeName = fc.ParentName
		if ms.Git != nil {
			idx := strings.LastIndex(ms.AsString(), "@")
			callerRef.ref, callerRef.version = ms.AsString()[:idx], ms.AsString()[idx+1:]
		} else if gremote, ok := cm.Labels["dagger.io/git.remote"]; ok {
			callerRef.ref = path.Join(gremote, ms.SourceRootSubpath)
			if gref, ok := cm.Labels["dagger.io/git.ref"]; ok {
				callerRef.version = gref
			}
		} else {
			// we don't a way to identify the caller ref and version. this could happen
			// when calling local modules outside a git repo for example
			return nil, nil
		}
	}

	callerRef.ref = normalizeRef(callerRef.ref)

	calleeRef.functionName = call.Field
	calleeRef.version = call.Module.Pin
	calleeRef.ref = normalizeRef(call.Module.Ref)

	var voidType Void
	if callID.Receiver() != nil {
		calleeRef.typeName = callID.Receiver().Type().NamedType()
	} else if call.Type.NamedType == voidType.TypeName() {
		// it's a top level module function call so set the ParentName as the callee type
		calleeRef.typeName = callerRef.typeName
	}

	// if it doesn't have a pin, it's a local callee module
	if calleeRef.version == "" {
		if ms.Git != nil {
			subPath := ms.SourceRootSubpath
			calleeRef.ref = path.Join(calleeRef.ref, subPath)
		} else {
			if ms.Local != nil {
				calleeRef.ref = strings.ReplaceAll(calleeRef.ref, ms.Local.ContextDirectoryPath, "")
				if gremote, ok := cm.Labels["dagger.io/git.remote"]; ok {
					calleeRef.ref = path.Join(gremote, calleeRef.ref)
				} else {
					return nil, nil
				}
			} else if gremote, ok := cm.Labels["dagger.io/git.remote"]; ok {
				subPath := ms.SourceRootSubpath
				calleeRef.ref = path.Join(gremote, subPath)
			} else {
				return nil, nil
			}
		}

		// if not within a function and a local call, use the git ref as the version
		if gr, ok := cm.Labels["dagger.io/git.ref"]; fc == nil && ok {
			calleeRef.version = gr
		} else {
			calleeRef.version = callerRef.version
		}
	}
	return callerRef, calleeRef
}

func normalizeRef(ref string) string {
	if strings.HasPrefix(ref, "git@") {
		if strings.Count(ref, "@") > 1 {
			ref = ref[:strings.LastIndex(ref, "@")]
		}
		ref = strings.ReplaceAll(strings.TrimPrefix(ref, "git@"), ":", "/")
	} else {
		ref, _, _ = strings.Cut(ref, "@")
		ref = strings.TrimSuffix(ref, "/.")
	}
	return ref
}

// recordStatus records the status of a call on a span.
func recordStatus(ctx context.Context, res dagql.AnyResult, span trace.Span, cached bool, id *call.ID) {
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
	if obj, ok := dagql.UnwrapAs[dagql.AnyResult](res); ok {
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
}

// logResult prints the result of a call to the span's stdout.
func logResult(ctx context.Context, res dagql.AnyResult, self dagql.AnyObjectResult, id *call.ID) {
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()
	fieldSpec, ok := self.ObjectType().FieldSpec(id.Field(), id.View())
	if !ok {
		return
	}
	if fieldSpec.Sensitive {
		// Take care not to print any sensitive values.
		return
	}
	if str, ok := dagql.UnwrapAs[dagql.String](res); ok {
		fmt.Fprint(stdio.Stdout, str)
	} else if _, ok := dagql.UnwrapAs[dagql.IDType](res); ok {
		// Don't print IDs; they can get quite large
	} else if lit, ok := dagql.UnwrapAs[call.Literate](res); ok {
		fmt.Fprint(stdio.Stdout, lit.ToLiteral().Display())
	}
}

// collectEffects records LLB op digests installed by this call so that we can
// know that it has pending work.
//
// Effects will become complete as spans appear from Buildkit with a
// corresponding effect ID.
func collectEffects(ctx context.Context, res dagql.AnyResult, span trace.Span, self dagql.AnyObjectResult) {
	// Keep track of which effects were already installed prior to the call so we
	// only see new ones.
	seenEffects := make(map[digest.Digest]bool)
	for _, def := range collectDefs(ctx, self) {
		if def == nil {
			continue
		}
		for _, op := range def.Def {
			seenEffects[digest.FromBytes(op)] = true
		}
	}

	var effectIDs []string
	for _, def := range collectDefs(ctx, res) {
		if def == nil {
			continue
		}
		for _, opBytes := range def.Def {
			dig := digest.FromBytes(opBytes)
			if seenEffects[dig] {
				continue
			}
			seenEffects[dig] = true

			var pbOp pb.Op
			err := pbOp.Unmarshal(opBytes)
			if err != nil {
				slog.WarnContext(ctx, "failed to unmarshal LLB", "err", err)
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
func isIntrospection(ctx context.Context, id *call.ID) bool {
	if id.Receiver() == nil {
		switch id.Field() {
		case "__schema",
			"__schemaJSONFile",
			"__schemaVersion",
			"currentTypeDefs",
			"currentFunctionCall",
			"currentModule",
			"typeDef",
			"sourceMap":
			return true
		default:
			return false
		}
	}

	//nolint:gocritic
	// disable these unless debug is set in OTEL baggage
	if !slog.IsDebug(ctx) {
		switch id.Receiver().Type().NamedType() {
		case "Function":
			switch id.Field() {
			case "withCachePolicy",
				"withArg",
				"withSourceMap",
				"withDescription":
				return true
			}
		}
	}

	return isIntrospection(ctx, id.Receiver())
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

func isDagOp(id *call.ID) bool {
	for _, arg := range id.Args() {
		if arg.Name() == IsDagOpArgName {
			return true
		}
	}
	return false
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

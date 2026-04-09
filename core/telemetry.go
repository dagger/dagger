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

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var _ dagql.AroundFunc = AroundFunc

func AroundFunc(
	ctx context.Context,
	req *dagql.CallRequest,
) (
	context.Context,
	func(res dagql.AnyResult, cached bool, rerr *error),
) {
	if req == nil || req.ResultCall == nil {
		return ctx, dagql.NoopDone
	}
	if dagql.IsSkipped(ctx) {
		return ctx, dagql.NoopDone
	}
	introspection, receiver := introspectionInfo(ctx, req.ResultCall)
	if introspection {
		return dagql.WithSkip(ctx), dagql.NoopDone
	}
	if isMeta(ctx, req.ResultCall) {
		// introspection+meta are very uninteresting spans
		// dagops are all self calls, no need to emit additional spans here
		return ctx, dagql.NoopDone
	}

	base := "Unknown"
	if receiver != nil && receiver.Type != nil {
		base = receiver.Type.NamedType
	} else if req.Receiver == nil {
		base = "Query"
	}
	spanName := fmt.Sprintf("%s.%s", base, req.Field)

	callDigest, err := req.RecipeDigest(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to derive call digest", "field", spanName, "err", err)
		return ctx, dagql.NoopDone
	}
	var q *Query
	if currentQuery, currentQueryErr := CurrentQuery(ctx); currentQueryErr == nil {
		q = currentQuery
		if seenKeys, seenKeysErr := q.TelemetrySeenKeyStore(ctx); seenKeysErr == nil {
			if !dagql.ShouldEmitTelemetry(ctx, seenKeys, callDigest.String(), req.DoNotCache) {
				return ctx, dagql.NoopDone
			}
		}
	}

	slog.InfoContext(ctx, "start call",
		"field", spanName,
		"digest", callDigest.String(),
	)

	callPB, err := req.ResultCall.CallPB(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to build call payload", "field", spanName, "err", err)
		return ctx, dagql.NoopDone
	}
	callAttr, err := callPB.Encode()
	if err != nil {
		slog.WarnContext(ctx, "failed to encode call", "field", spanName, "err", err)
		return ctx, dagql.NoopDone
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.DagDigestAttr, callDigest.String()),
		attribute.String(telemetry.DagCallAttr, callAttr),
	}

	// if inside a module call, add call trace metadata. this is useful
	// since within a single span, we can correlate the caller's and callee's
	// module and functions calls
	if req.Module != nil && q != nil {
		callerRef, calleeRef := parseCallerCalleeRefs(ctx, q, req.ResultCall)

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

	if callInputs, err := req.ResultCall.Inputs(ctx); err != nil {
		slog.WarnContext(ctx, "failed to compute inputs(call)", "field", spanName, "err", err)
	} else {
		inputs := make([]string, len(callInputs))
		for i, input := range callInputs {
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
			"digest", callDigest.String(),
		)

		defer telemetry.EndWithCause(span, err)
		recordStatus(ctx, res, span, cached, req.ResultCall)
		recordPending(res, span)
		logResult(ctx, res, req.ResultCall)
	}
}

type moduleCallRef struct {
	ref          string
	version      string
	functionName string
	typeName     string
}

func parseCallerCalleeRefs(ctx context.Context, q *Query, frame *dagql.ResultCall) (*moduleCallRef, *moduleCallRef) {
	if frame == nil || frame.Module == nil {
		return nil, nil
	}
	cm, _ := q.MainClientCallerMetadata(ctx)
	fc, _ := q.CurrentFunctionCall(ctx)
	m, _ := q.CurrentModule(ctx)
	sd, _ := q.CurrentServedDeps(ctx)

	callerRef, calleeRef := &moduleCallRef{}, &moduleCallRef{}
	// there's a caller

	var ms *ModuleSource
	if m.Self() != nil {
		ms = m.Self().GetSource()
	} else if m, ok := sd.Lookup(frame.Module.Name); ok {
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

	calleeRef.functionName = frame.Field
	calleeRef.version = frame.Module.Pin
	calleeRef.ref = normalizeRef(frame.Module.Ref)

	var voidType Void
	if receiver, err := frame.ReceiverCall(ctx); err == nil && receiver != nil && receiver.Type != nil {
		calleeRef.typeName = receiver.Type.NamedType
	} else if frame.Type != nil && frame.Type.NamedType == voidType.TypeName() {
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
func recordStatus(ctx context.Context, res dagql.AnyResult, span trace.Span, cached bool, frame *dagql.ResultCall) {
	if cached && !dagql.HasPendingLazyEvaluation(res) {
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
		field := ""
		if frame != nil {
			field = frame.Field
		}
		isLoader := strings.HasPrefix(field, "load") && strings.HasSuffix(field, "FromID")
		if !isLoader {
			if objDig, err := obj.RecipeDigest(ctx); err == nil && objDig != "" {
				span.SetAttributes(attribute.String(telemetry.DagOutputAttr, objDig.String()))
			}
		}
	}
}

func recordPending(res dagql.AnyResult, span trace.Span) {
	if dagql.HasPendingLazyEvaluation(res) {
		span.SetAttributes(attribute.Bool(dagql.PendingAttr, true))
	}
}

// logResult prints the result of a call to the span's stdout.
func logResult(ctx context.Context, res dagql.AnyResult, frame *dagql.ResultCall) {
	var output string
	if str, ok := dagql.UnwrapAs[dagql.String](res); ok {
		output = string(str)
	} else if _, ok := dagql.UnwrapAs[dagql.IDType](res); ok {
		// Don't print IDs; they can get quite large.
		return
	} else if lit, ok := dagql.UnwrapAs[call.Literate](res); ok {
		output = lit.ToLiteral().Display()
	} else {
		return
	}

	fieldSpec, ok := fieldSpecForCall(ctx, frame)
	if !ok {
		return
	}
	if fieldSpec.Sensitive {
		// Take care not to print any sensitive values.
		return
	}

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, output)
}

func fieldSpecForCall(ctx context.Context, frame *dagql.ResultCall) (dagql.FieldSpec, bool) {
	srv := dagql.CurrentDagqlServer(ctx)
	if srv == nil || frame == nil || frame.Field == "" {
		return dagql.FieldSpec{}, false
	}

	parentTypeName := srv.Root().Type().Name()
	if receiver, err := frame.ReceiverCall(ctx); err == nil && receiver != nil && receiver.Type != nil {
		parentTypeName = receiver.Type.NamedType
	}

	parentType, ok := srv.ObjectType(parentTypeName)
	if !ok {
		return dagql.FieldSpec{}, false
	}
	return parentType.FieldSpec(frame.Field, frame.View)
}

// isIntrospection detects whether an ID is an introspection query.
//
// These queries tend to be very large and are not interesting for users to
// see.
func isIntrospection(ctx context.Context, frame *dagql.ResultCall) bool {
	introspection, _ := introspectionInfo(ctx, frame)
	return introspection
}

func introspectionInfo(ctx context.Context, frame *dagql.ResultCall) (bool, *dagql.ResultCall) {
	if frame == nil {
		return false, nil
	}

	cur := frame
	var immediateReceiver *dagql.ResultCall
	for cur != nil {
		if cur.Receiver == nil {
			switch cur.Field {
			case "__schema",
				"__schemaJSONFile",
				"__schemaVersion",
				"currentTypeDefs",
				"currentModule",
				"currentFunctionCall",
				"function",
				"typeDef",
				"sourceMap",
				"__loadInputTypeDef",
				"__function",
				"__functionArg",
				"__functionArgExact",
				"__fieldTypeDef",
				"__fieldTypeDefExact",
				"__enumMemberTypeDef",
				"__enumValueTypeDef",
				"__listTypeDef",
				"__objectTypeDef",
				"__interfaceTypeDef",
				"__inputTypeDef",
				"__scalarTypeDef",
				"__enumTypeDef",
				"__function":
				return true, immediateReceiver
			default:
				return false, immediateReceiver
			}
		}

		receiver, err := cur.ReceiverCall(ctx)
		if err != nil || receiver == nil {
			return false, immediateReceiver
		}
		if cur == frame {
			immediateReceiver = receiver
		}

		//nolint:gocritic
		// disable these unless debug is set in OTEL baggage
		if !slog.IsDebug(ctx) && receiver.Type != nil {
			switch receiver.Type.NamedType {
			case "Function":
				switch cur.Field {
				case "withCachePolicy",
					"withArg",
					"withSourceMap",
					"withDescription":
					return true, immediateReceiver
				}
				if strings.HasPrefix(cur.Field, "__") {
					return true, immediateReceiver
				}
			case "TypeDef":
				switch cur.Field {
				case "withOptional",
					"withKind",
					"withScalar",
					"withListOf",
					"withObject",
					"withInterface",
					"withObjectField",
					"withFunction",
					"withObjectConstructor",
					"withEnum",
					"withEnumValue",
					"withEnumMember":
					return true, immediateReceiver
				}
				if strings.HasPrefix(cur.Field, "__") {
					return true, immediateReceiver
				}
			case "FunctionArg",
				"ObjectTypeDef",
				"InterfaceTypeDef",
				"InputTypeDef",
				"FieldTypeDef",
				"ListTypeDef",
				"EnumTypeDef",
				"EnumMemberTypeDef":
				if strings.HasPrefix(cur.Field, "__") {
					return true, immediateReceiver
				}
			}
		}

		cur = receiver
	}

	return false, immediateReceiver
}

// isMeta returns true if any type in the ID is "too meta" to show to the user,
// for example span and error APIs.
func isMeta(ctx context.Context, frame *dagql.ResultCall) bool {
	if frame == nil {
		return false
	}
	if anyReturns(ctx, frame, "Error") {
		return true
	}
	typeName := ""
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	switch frame.Field {
	case
		// Seeing loadFooFromID is only really interesting if it actually
		// resulted in evaluating the ID, so we don't need to give it its own
		// span.
		fmt.Sprintf("load%sFromID", typeName),
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
func anyReturns(ctx context.Context, frame *dagql.ResultCall, types ...string) bool {
	if frame == nil || frame.Type == nil {
		return false
	}
	ret := frame.Type.NamedType
	if slices.Contains(types, ret) {
		return true
	}
	receiver, err := frame.ReceiverCall(ctx)
	if err != nil || receiver == nil {
		return false
	}
	return anyReturns(ctx, receiver, types...)
}

package engineutil

import (
	"context"
	"encoding/json"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
)

// OTel emission for the container exec, splitting engine setup time from the
// user's process time — the single highest-value addition for the
// user-facing goal: a slow `go build` should headline as the *user's* slow
// command, not as engine overhead. It mirrors, on the executor's ordinary OTel
// spans, the native wcprof recorder's exec.run op plus its containerStart /
// processRun phase split (executor.go, executor_spec.go) so the offline analyzer
// compiles a Cloud trace into the same wcprof IR.
//
// The OTel executor path is otherwise pure traceparent propagation with no
// Tracer.Start phase spans, so this is genuinely new emission. Like the other
// OTel-profiling hooks (dagql/otelprof_hooks.go) it is gated only on telemetry
// being active (dagql.OTelProfActive), NOT on wcprof.Enabled: the OTel source
// must be reconstructable from a Cloud trace alone, with no native recorder
// running. The cost is bounded — three passthrough spans per container run, all
// on a path that is already starting a container.

// beginOTelExecRun starts the exec.run span for one container run,
// mirroring native's OpKindExec op (executor.go Client.Run). It is created on the
// executor's ctx, which descends from the withExec resolver's call_exec span (the
// resolver runs the executor synchronously), so exec.run nests under
// call_exec; for a service daemon it instead nests under the service's exec span.
// Either way it is a genuine synchronous nesting (the caller is blocked through
// the whole run), so this parent-child edge is itself causal and the implicit join
// — not an explicit wait edge — serializes it. Marked ui.passthrough so dagui keeps showing the
// visible withExec/service span, not this internal one. The caller ends the
// returned span when the run finishes.
//
// ident mirrors native's exec.run ident (executor.go): the call digest when
// known, else the execution id — so the cross-source oracle can match per-exec.
func beginOTelExecRun(ctx context.Context, ident string) (context.Context, trace.Span) {
	return Tracer(ctx).Start(ctx, "exec.run",
		telemetry.Passthrough(),
		trace.WithAttributes(
			attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindExec.String()),
			attribute.String(telemetry.DagDigestAttr, ident),
		),
	)
}

// endOTelExecRun ends the exec.run span, charging any run error as its status
// (the OTel analog of execOp.EndErr) — via dagql.EndProfSpan, never
// telemetry.EndWithCause: stamping this unrendered passthrough span as the
// error's origin would also mutate the error the executor returns to core.
// Kept here so executor.go owns no otel-go span lifecycle of its own.
func endOTelExecRun(span trace.Span, errPtr *error) {
	dagql.EndProfSpan(span, errPtr)
}

// emitOTelExecSplit emits the containerStart (engine) and processRun (user) child
// spans of exec.run, split at the started-callback boundary, after
// the run returns — mirroring native's two RecordOp calls (executor_spec.go).
// They are emitted with explicit timestamps (the boundary is known only in
// retrospect, exactly as native records it from stored nanos), so the spans carry
// the true [run start, process started] and [process started, run end] intervals
// as genuine children of the exec.run span carried by ctx.
//
//   - started.IsZero() means the process never started (a setup failure); native
//     then records only containerStart over the whole interval, charged the run
//     error — mirrored here.
//   - work_type=user is set on processRun ONLY: marking the whole run "user" would
//     mislabel engine container-setup overhead (not sub-ms — the original wcprof
//     headline was a serial container-setup tax) as the user's slow command.
func emitOTelExecSplit(ctx context.Context, id string, start, started, end time.Time, runErr error, argv []string) {
	if !dagql.OTelProfActive(ctx) {
		return
	}
	if started.IsZero() {
		// process never started: the engine-setup phase ran the whole interval and
		// carries the failure (native: containerStart over [start,end] with the run
		// error). No processRun, so no argv (it stays the blob, consistent with native).
		emitOTelExecPhase(ctx, "exec.containerStart", id, start, end, false, runErr, nil)
		return
	}
	// the container started: engine overhead is [start,started] (it succeeded in
	// starting, so no error), the user's process is [started,end] and carries any
	// run error (native: containerStart OutcomeOK + processRun WorkTypeUser). Argv
	// rides ONLY on the user processRun phase, where the scalable self-time lives.
	emitOTelExecPhase(ctx, "exec.containerStart", id, start, started, false, nil, nil)
	emitOTelExecPhase(ctx, "exec.processRun", id, started, end, true, runErr, argv)
}

// emitOTelExecPhase emits one exec phase span [start,end] as a passthrough child
// of exec.run, classified exec_phase (matching native's OpKindExecPhase). user
// sets work_type=user (the process-run phase); err sets an error status so the
// loader records the phase's outcome. On the user phase, argv (when present) is
// stamped as the scalar JSON-array string the loader compiles into Op.Argv.
func emitOTelExecPhase(ctx context.Context, name, id string, start, end time.Time, user bool, err error, argv []string) {
	attrs := []attribute.KeyValue{
		attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindExecPhase.String()),
		attribute.String(telemetry.DagDigestAttr, id),
	}
	if user {
		attrs = append(attrs, attribute.String(telemetryattrs.WcprofWorkTypeAttr, wcprof.WorkTypeUser.String()))
		// The same json.Marshal of the same scrubbed+bounded slice the native
		// recorder interns (record.go internArgv), so the two wire forms are
		// byte-identical. On a marshal error the attr is simply omitted — never a
		// partial or panicking emit.
		if len(argv) > 0 {
			if b, merr := json.Marshal(argv); merr == nil {
				attrs = append(attrs, attribute.String(telemetryattrs.WcprofExecArgvAttr, string(b)))
			}
		}
	}
	_, span := Tracer(ctx).Start(ctx, name,
		telemetry.Passthrough(),
		trace.WithTimestamp(start),
		trace.WithAttributes(attrs...),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End(trace.WithTimestamp(end))
}

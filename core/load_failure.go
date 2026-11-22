package core

import (
	"context"
	"errors"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

type moduleLoadSpanKey struct{}

// moduleLoadSpan is the span of a module load a client asked for, and
// whether the module it loads claimed it (see ClaimModuleLoadSpan).
type moduleLoadSpan struct {
	span    trace.SpanContext
	claimed bool
}

// ContextWithModuleLoadSpan marks ctx's current span as the load of a module
// a client asked for, the row a user sees for it, for the module the load is
// for to claim (see ClaimModuleLoadSpan).
func ContextWithModuleLoadSpan(ctx context.Context) context.Context {
	return context.WithValue(ctx, moduleLoadSpanKey{}, moduleLoadSpan{span: trace.SpanContextFromContext(ctx)})
}

// ClaimModuleLoadSpan is for the load of a module as a Module
// (ModuleSource.asModule). The first beneath ContextWithModuleLoadSpan is the
// module the load is for, which claims the load's span; any module that one
// loads in turn, such as a dependency, has none, so that its failure isn't
// told as its dependent's.
func ClaimModuleLoadSpan(ctx context.Context) context.Context {
	mark, ok := ctx.Value(moduleLoadSpanKey{}).(moduleLoadSpan)
	if !ok || !mark.span.IsValid() {
		return ctx
	}
	if mark.claimed {
		return context.WithValue(ctx, moduleLoadSpanKey{}, moduleLoadSpan{})
	}
	mark.claimed = true
	return context.WithValue(ctx, moduleLoadSpanKey{}, mark)
}

// ModuleLoadSpanContext returns the span of the load of the module ctx loads,
// if a client asked for it (see ClaimModuleLoadSpan). An SDK that reads a
// module's types from its source in the engine, as Dang does, reports the
// source's errors there, as the origin of the load's failure: the calls
// between the load and the SDK are the engine's plumbing. The value doesn't
// cross a nested client, so a function a module calls while loading has
// none.
func ModuleLoadSpanContext(ctx context.Context) (trace.SpanContext, bool) {
	mark, ok := ctx.Value(moduleLoadSpanKey{}).(moduleLoadSpan)
	return mark.span, ok && mark.claimed && mark.span.IsValid()
}

type ModuleLoadFailure struct {
	// Name is the module's workspace name (what the skipped-module span is
	// called).
	Name string `json:"name"`
	// Dir is the module's workspace-root-relative directory, or "" when it
	// has none (a git source).
	Dir string `json:"dir,omitempty"`
	// Message is the described load error (see the engine's
	// describeLoadFailure).
	Message string `json:"message"`
	// Entrypoint reports whether the module is the workspace main, whose
	// artifact paths may omit the module name; the loader sets it. The legacy
	// field and JSON name are retained for compatibility with older consumers.
	// Siblings lists the address names of the workspace's other modules,
	// which such a shorthand path cannot start with.
	Entrypoint bool     `json:"entrypoint,omitempty"`
	Siblings   []string `json:"siblings,omitempty"`
}

// ModuleLoadMode is how an operation wants a workspace module it cannot load
// to be handled.
type ModuleLoadMode int

const (
	// ModuleLoadStrict fails the operation on the first module that cannot
	// load: `dagger call`, `dagger start`.
	ModuleLoadStrict ModuleLoadMode = iota
	// ModuleLoadBestEffort skips a module that cannot load and records its
	// failure for the caller to report: `dagger check`, which stands each
	// failure up as a check that fails.
	ModuleLoadBestEffort
	// ModuleLoadRepairing is best-effort for the operation that repairs
	// unloadable modules, `dagger generate`, whose failure messages must not
	// advise running the command already running.
	ModuleLoadRepairing
)

// BestEffort reports whether a module that cannot load is skipped rather than
// failing the whole operation.
func (mode ModuleLoadMode) BestEffort() bool {
	return mode != ModuleLoadStrict
}

// DescribeLoadFailure renders a module load failure the way a best-effort load
// should report it. A strict load surfaces the raw error as-is; a best-effort
// one skips the module instead, so its report needs two adjustments:
//
//   - A module missing its generated files can't load until they're generated.
//     The SDK's advice is to run `dagger generate`, which is useless to echo
//     back at `dagger generate` itself — under ModuleLoadRepairing the message
//     keeps only the missing-file reason. Every other mode
//     keeps the advice, which is exactly the fix its user needs.
//   - An exec failure (typically the SDK runtime's build, e.g. `go build`
//     rejecting the module source, or its constructor crashing) carries the
//     compiler/runtime output only in the failing span's logs. That span sits
//     under the internal module-load spans the frontends hide, and the API's
//     loadFailures strings can't reach it at all, so without this the user is
//     left with a bare "exit code: 1". Inline the captured output.
//
// The [traceparent:...] error-origin markers are stripped: they are
// span-attribution plumbing, not part of the message (see LoadFailureCause).
func DescribeLoadFailure(err error, mode ModuleLoadMode) string {
	msg := StripErrorOrigins(err.Error())

	var missing *MissingGeneratedFileError
	if mode == ModuleLoadRepairing && errors.As(err, &missing) {
		msg = strings.Replace(msg, missing.Error(), missing.Reason(), 1)
	}

	var execErr *ExecError
	if errors.As(err, &execErr) {
		if out := execErrorOutput(execErr); out != "" {
			msg += "\n" + out
		}
	}
	return msg
}

// LoadFailureCause is the error to report on a span for a load failure: the
// described message (DescribeLoadFailure), optionally prefixed, re-stamped
// with the original error's origins so EndWithCause still links the span to
// the failing exec.
func LoadFailureCause(prefix string, err error, mode ModuleLoadMode) error {
	cause := errors.New(prefix + DescribeLoadFailure(err, mode))
	for _, origin := range telemetry.ParseErrorOrigins(err.Error()) {
		cause = telemetry.TrackOrigin(cause, origin)
	}
	return cause
}

// execErrorOutput is the output worth showing for a failed exec: stderr (where
// compilers and runtimes report errors), falling back to stdout.
func execErrorOutput(execErr *ExecError) string {
	if out := strings.TrimSpace(execErr.Stderr); out != "" {
		return out
	}
	return strings.TrimSpace(execErr.Stdout)
}

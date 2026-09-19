package core

import (
	"errors"
	"strings"

	telemetry "github.com/dagger/otel-go"
)

// ModuleLoadMode is how an operation wants a workspace module it cannot load
// to be handled.
type ModuleLoadMode int

const (
	// ModuleLoadStrict fails the operation on the first module that cannot
	// load: `dagger call`, `dagger up`.
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
//     says the module is skipped until generation instead. Every other mode
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
		msg = strings.Replace(msg, missing.Error(), missing.Reason()+" (skipped until it is generated)", 1)
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

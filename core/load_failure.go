package core

import (
	"errors"
	"strings"

	telemetry "github.com/dagger/otel-go"
)

// DescribeLoadFailure renders a module load failure the way best-effort
// `dagger generate` should report it. The raw error is what a strict load
// (dagger call, check, up) surfaces and stays recorded as-is; generate skips
// the module instead, so its report needs two adjustments:
//
//   - A module missing its generated files can't load until they're generated.
//     The SDK's advice is to run `dagger generate` — which is what's running —
//     so say the module is skipped until generation instead of parroting the
//     hint back.
//   - An exec failure (typically the SDK runtime's build, e.g. `go build`
//     rejecting the module source, or its constructor crashing) carries the
//     compiler/runtime output only in the failing span's logs. That span sits
//     under the internal module-load spans the frontends hide, and the API's
//     loadFailures strings can't reach it at all, so without this the user is
//     left with a bare "exit code: 1". Inline the captured output.
//
// The [traceparent:...] error-origin markers are stripped: they are
// span-attribution plumbing, not part of the message (see LoadFailureCause).
func DescribeLoadFailure(err error) string {
	msg := StripErrorOrigins(err.Error())

	var missing *MissingGeneratedFileError
	if errors.As(err, &missing) {
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
func LoadFailureCause(prefix string, err error) error {
	var cause error = errors.New(prefix + DescribeLoadFailure(err))
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

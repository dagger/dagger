package core

import (
	"context"
	"errors"
	"strings"

	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// startGitSourceTree records which path a source-only tree (discardGitDir)
// took, and why the cheaper ones were skipped, so benchmarks can tell them
// apart without reading span attributes:
//
//   - an internal span named "git source tree: <path>" (incremental, full,
//     remote or error), with dagger.git.tree.path, dagger.git.tree.detail and
//     dagger.git.tree.skipped attributes;
//   - a zero-length wcprof marker op of kind io under the GitRef.tree lazy
//     op, class "git.tree.<path>[<detail or skip codes>]", ident the same.
//
// The returned context carries the span; finish ends it.
func startGitSourceTree(ctx context.Context) (context.Context, func(path, detail string, skipped []string, err error)) {
	profCtx := ctx
	ctx, span := Tracer(ctx).Start(ctx, "git source tree", telemetry.Internal())
	return ctx, func(path, detail string, skipped []string, err error) {
		if err != nil {
			path = "error"
		}
		span.SetName("git source tree: " + path)
		span.SetAttributes(
			attribute.String("dagger.git.tree.path", path),
			attribute.String("dagger.git.tree.detail", detail),
			attribute.StringSlice("dagger.git.tree.skipped", skipped),
		)
		telemetry.EndWithCause(span, &err)
		if !wcprof.Enabled(profCtx) {
			return
		}
		why := detail
		if len(skipped) > 0 {
			why = strings.TrimPrefix(detail+",", ",") + strings.Join(skipped, ",")
		}
		class := "git.tree." + path
		if why != "" {
			class += "[" + why + "]"
		}
		outcome := wcprof.OutcomeOK
		if err != nil {
			outcome = wcprof.OutcomeError
		}
		now := wcprof.NowNS()
		wcprof.RecordOp(profCtx, wcprof.OpKindIO, class, wcprof.OpOpts{Ident: why}, now, now, outcome)
	}
}

// gitTreeFallbackCode reduces a fast path's fallback error to a short code for
// span names and profile classes; the full error is on the fast path's span.
func gitTreeFallbackCode(err error) string {
	var reason nativeCommitUnsupportedReason
	if errors.As(err, &reason) {
		return string(reason)
	}
	return "error"
}

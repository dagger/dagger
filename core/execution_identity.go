package core

import (
	"context"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
)

// SetExecutionIdentity lets the executor record the content-preferred digest
// of the call that owns an execution. The executor reads it when the
// execution ends: its inputs are evaluated then, so the digest matches the one
// that later cache hits of the call report. A call span can complete before a
// lazy input such as a git tree learns its content, and then carries an older
// digest.
//
// It applies only when the current call is the call whose recipe digest the
// execution already carries, so an execution never gets another call's
// identity.
func SetExecutionIdentity(ctx context.Context, execMD *engineutil.ExecutionMetadata) {
	if execMD == nil || execMD.ContentPreferredDigest != nil || execMD.CallDigest == "" {
		return
	}
	frame := dagql.CurrentCall(ctx)
	if frame == nil {
		return
	}
	if recipe, err := frame.RecipeDigest(ctx); err != nil || recipe != execMD.CallDigest {
		return
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return
	}
	// Keep only the engine-wide cache, not the request context: exec metadata
	// can stay in the cache after the session ends, and the request context
	// holds session state.
	cacheCtx := dagql.ContextWithCache(context.Background(), cache)
	execMD.ContentPreferredDigest = func() digest.Digest {
		dig, err := frame.ContentPreferredDigestForTelemetry(cacheCtx)
		if err != nil {
			slog.Debug("failed to derive execution content-preferred digest", "err", err)
			return ""
		}
		return dig
	}
}

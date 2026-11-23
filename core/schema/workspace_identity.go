package schema

import (
	"context"
	"log/slog"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// identifyWorkspaceResults makes every field of fields that returns a
// Workspace give its result the workspace's content identity as its content
// digest (identifiedWorkspaceResult). The other fields are left as they are.
func identifyWorkspaceResults[T dagql.Typed](fields dagql.Fields[T]) dagql.Fields[T] {
	for i, field := range fields {
		if field.Spec == nil || field.Spec.Type == nil {
			continue
		}
		if t := field.Spec.Type.Type(); t == nil || t.NamedType != (&core.Workspace{}).Type().NamedType {
			continue
		}
		resolve := field.Func
		field.Func = func(ctx context.Context, self dagql.ObjectResult[T], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			res, err := resolve(ctx, self, args, view)
			if err != nil || res == nil {
				return res, err
			}
			return identifiedWorkspaceResult(ctx, res), nil
		}
		fields[i] = field
	}
	return fields
}

// identifiedWorkspaceResult gives a workspace that has a content identity
// that identity as its result's content digest, unless the result carries a
// content digest already, as the session's workspace does
// (currentWorkspace). Two workspaces with the same identity are
// interchangeable, so a workspace made from another, by withWorkdir or an
// edit, then shares a class with every workspace that has its identity, and
// the reads keyed by its content (workspaceReadCacheKey) hit across calls,
// sessions and engines. A host or rootless workspace has no identity and is
// returned as it is, and so is any other result. A workspace whose identity
// can't be computed loses only the sharing.
func identifiedWorkspaceResult(ctx context.Context, res dagql.AnyResult) dagql.AnyResult {
	ws, ok := res.Unwrap().(*core.Workspace)
	if !ok || !ws.HasContentIdentity() {
		return res
	}
	if frame, err := res.ResultCall(); err == nil && frame != nil && frame.ContentDigest() != "" {
		return res
	}
	identity, ok, err := ws.ContentIdentity(ctx)
	if err != nil {
		slog.Warn("workspace content identity failed; its result carries no content digest", "error", err)
		return res
	}
	if !ok {
		return res
	}
	identified, err := res.WithContentDigestAny(ctx, identity)
	if err != nil {
		slog.Warn("setting a workspace's content identity as its content digest failed", "error", err)
		return res
	}
	return identified
}

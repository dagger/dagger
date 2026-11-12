package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

// keptFromContent keeps a planning field's results past their session when
// its receiver comes from a workspace identified by its content: only then
// can a later session's call have the same key. Planning through any other
// workspace is keyed per call (see currentWorkspace), so keeping it would
// only grow the cache. A kept result keeps its receiver too, so a receiver
// that no later session reuses keeps nothing.
func keptFromContent[T dagql.Typed](field dagql.Field[T], fromContent func(dagql.ObjectResult[T]) bool) dagql.Field[T] {
	spec := *field.Spec
	spec.IsPersistable = true
	field.Spec = &spec
	resolve := field.Func
	field.Func = func(ctx context.Context, self dagql.ObjectResult[T], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
		if !fromContent(self) {
			dagql.DeclinePersistence(ctx)
		}
		return resolve(ctx, self, args, view)
	}
	return field
}

func workspaceFromContent(ws dagql.ObjectResult[*core.Workspace]) bool {
	return ws.Self() != nil && ws.Self().HasContentIdentity()
}

func artifactsFromContent(a dagql.ObjectResult[*core.Artifacts]) bool {
	for _, entry := range a.Self().Entries {
		if !workspaceFromContent(entry.Workspace) {
			return false
		}
	}
	return len(a.Self().Entries) > 0
}

// keptExpansion runs a resolver of a keptFromContent field that evaluates
// the selection's collections to list their items. Its result stands for
// those evaluations, so it is kept only as long as each of them would give
// the same result: only for its own session when one is cached per call,
// session or client, and not past the earliest expiry of their results.
// Another session, even one open at the same time, lists the items its own
// evaluations return.
func keptExpansion[R any](ctx context.Context, resolve func(context.Context) (R, error)) (R, error) {
	scoped, scopes := dagql.WithCallScopes(ctx)
	result, err := resolve(scoped)
	if err != nil {
		return result, err
	}
	if bound := scopes.SessionBound(); bound != "" {
		slog.Debug("listing bound to its session", "boundBy", bound)
		dagql.BindToSession(ctx)
	}
	dagql.ExpireBy(ctx, scopes.ExpiresAtUnix())
	return result, nil
}

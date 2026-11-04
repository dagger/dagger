package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

func artifactFromContent(a dagql.ObjectResult[*core.Artifact]) bool {
	return workspaceFromContent(a.Self().Workspace) && !a.Self().SessionBound
}

func artifactsFromContent(a dagql.ObjectResult[*core.Artifacts]) bool {
	for _, entry := range a.Self().Entries {
		if !workspaceFromContent(entry.Workspace) || entry.SessionBound {
			return false
		}
	}
	return len(a.Self().Entries) > 0
}

// keptExpansion runs a resolver of a keptFromContent field that evaluates
// the selection's collections to list their items. Its result stands for
// those evaluations, so it is kept only when each would give a later session
// the same result: none is cached per call, session or client, or only for a
// time. It reports whether it wasn't.
func keptExpansion[R any](ctx context.Context, resolve func(context.Context) (R, error)) (R, bool, error) {
	scoped, scopes := dagql.WithCallScopes(ctx)
	result, err := resolve(scoped)
	bound := err == nil && scopes.SessionBound() != ""
	if bound {
		dagql.DeclinePersistence(ctx)
	}
	return result, bound, err
}

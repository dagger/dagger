package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type watcherSchema struct{}

var _ SchemaResolvers = &watcherSchema{}

func (s *watcherSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Watcher]{
		dagql.Func("asDirectory", s.asDirectory),

		// XXX: CachePerCall might be nice here? but causes *bizarre* problems,
		// still invoking the call, but returning the old value...
		dagql.NodeFuncWithCacheKey("next", s.next, dagql.CachePerClient),
	}.Install(srv)
}

func (s *watcherSchema) asDirectory(ctx context.Context, parent *core.Watcher, args struct{}) (dagql.ObjectResult[*core.Directory], error) {
	return parent.Current, nil
}

func (s *watcherSchema) next(ctx context.Context, parent dagql.ObjectResult[*core.Watcher], args struct{}) (inst dagql.ObjectResult[*core.Watcher], _ error) {
	// XXX: should persist the watcher between next calls, so that we don't
	// drop events
	client, err := parent.Self().Watcher(ctx)
	if err != nil {
		return inst, err
	}

	watcher, err := parent.Self().Wait(ctx, client)
	if err != nil {
		return inst, err
	}
	srv := dagql.CurrentDagqlServer(ctx)
	return dagql.NewObjectResultForCurrentID(ctx, srv, watcher)
}

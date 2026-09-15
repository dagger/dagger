package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

var engineDefaultPlatformInput = dagql.ImplicitInput{
	Name: "engineDefaultPlatform",
	Resolver: func(ctx context.Context, _ map[string]dagql.Input) (dagql.Input, error) {
		return currentEngineDefaultPlatform(ctx)
	},
}

func currentEngineDefaultPlatform(ctx context.Context) (core.Platform, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return core.Platform{}, err
	}
	return query.Platform(), nil
}

type platformSchema struct{}

var _ SchemaResolvers = &platformSchema{}

func (s *platformSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("defaultPlatform", s.defaultPlatform).
			WithInput(engineDefaultPlatformInput).
			Doc(`The default platform of the engine.`),
	}.Install(srv)

	srv.InstallScalar(core.Platform{})
}

func (s *platformSchema) defaultPlatform(ctx context.Context, parent *core.Query, _ struct{}) (core.Platform, error) {
	return parent.Platform(), nil
}

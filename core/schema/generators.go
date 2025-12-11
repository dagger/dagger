package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type generatorsSchema struct{}

var _ SchemaResolvers = &generatorsSchema{}

func (s generatorsSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.GeneratorGroup]{
		dagql.Func("list", s.list).
			Doc("Return a list of individual generatos and their details"),

		dagql.Func("run", s.run).
			Doc("Execute all selected generators"),
	}.Install(srv)

	dagql.Fields[*core.Generator]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the generator"),
		dagql.Func("run", s.runSingleGenerator).
			Doc("Execute the generator"),
	}.Install(srv)
}

func (s generatorsSchema) list(ctx context.Context, parent *core.GeneratorGroup, args struct{}) ([]*core.Generator, error) {
	return parent.List(ctx)
}

func (s generatorsSchema) run(ctx context.Context, parent *core.GeneratorGroup, args struct{}) (*core.GeneratorGroup, error) {
	return parent.Run(ctx)
}

func (s generatorsSchema) name(_ context.Context, parent *core.Generator, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s generatorsSchema) runSingleGenerator(ctx context.Context, parent *core.Generator, args struct{}) (*core.Generator, error) {
	return parent.Run(ctx)
}

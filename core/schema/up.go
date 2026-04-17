package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type upSchema struct{}

var _ SchemaResolvers = &upSchema{}

func (s upSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.UpGroup]{
		dagql.Func("list", s.list).
			Doc("Return a list of individual services and their details"),
		dagql.Func("run", s.run).
			Doc("Execute all selected service functions"),
	}.Install(srv)

	dagql.Fields[*core.Up]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the service"),
		dagql.Func("description", s.description).
			Doc("The description of the service"),
		dagql.Func("path", s.path).
			Doc("The path of the service within its module"),
		dagql.Func("originalModule", s.originalModule).
			Doc("The original module in which the service has been defined"),
		dagql.Func("run", s.runSingleUp).
			Doc("Execute the service function"),
	}.Install(srv)
}

func (s upSchema) name(_ context.Context, parent *core.Up, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s upSchema) description(_ context.Context, parent *core.Up, args struct{}) (string, error) {
	return parent.Description(), nil
}

func (s upSchema) path(_ context.Context, parent *core.Up, args struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s upSchema) originalModule(_ context.Context, parent *core.Up, args struct{}) (*core.Module, error) {
	return parent.OriginalModule(), nil
}

func (s upSchema) list(_ context.Context, parent *core.UpGroup, args struct{}) ([]*core.Up, error) {
	return parent.List(), nil
}

func (s upSchema) run(ctx context.Context, parent *core.UpGroup, args struct{}) (*core.UpGroup, error) {
	return parent.Run(ctx)
}

func (s upSchema) runSingleUp(ctx context.Context, parent *core.Up, args struct{}) (*core.Up, error) {
	return parent.Run(ctx)
}

package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type checksSchema struct{}

var _ SchemaResolvers = &checksSchema{}

func (s checksSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.CheckGroup]{
		dagql.Func("list", s.list).
			Doc("Return a list of individual checks and their details"),

		dagql.Func("run", s.run).
			Doc("Execute all selected checks"),

		dagql.Func("report", s.report).
			Doc("Generate a markdown report"),
	}.Install(srv)

	// Check methods
	dagql.Fields[*core.Check]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the check"),
		dagql.Func("description", s.description).
			Doc("The description of the check"),
		dagql.Func("path", s.path).
			Doc("The path of the check within its module"),

		dagql.Func("resultEmoji", s.resultEmoji).
			Doc("An emoji representing the result of the check"),
		dagql.Func("run", s.runSingleCheck).
			Doc("Execute the check"),
	}.Install(srv)
}

func (s checksSchema) name(_ context.Context, parent *core.Check, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s checksSchema) description(_ context.Context, parent *core.Check, args struct{}) (string, error) {
	return parent.Description(), nil
}

func (s checksSchema) path(_ context.Context, parent *core.Check, args struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s checksSchema) resultEmoji(_ context.Context, parent *core.Check, args struct{}) (string, error) {
	return parent.ResultEmoji(), nil
}

func (s checksSchema) list(ctx context.Context, parent *core.CheckGroup, args struct{}) ([]*core.Check, error) {
	return parent.List(), nil
}

func (s checksSchema) run(ctx context.Context, parent *core.CheckGroup, args struct{}) (*core.CheckGroup, error) {
	return parent.Run(ctx)
}

func (s checksSchema) report(ctx context.Context, parent *core.CheckGroup, args struct{}) (*core.File, error) {
	return parent.Report(ctx)
}

func (s checksSchema) runSingleCheck(ctx context.Context, parent *core.Check, args struct{}) (*core.Check, error) {
	return parent.Run(ctx)
}

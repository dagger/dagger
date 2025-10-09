package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type checksSchema struct{}

var _ SchemaResolvers = &checksSchema{}

func (s checksSchema) Install(srv *dagql.Server) {
	// Top-level constructor: checks (returns array of Check)
	dagql.Fields[*core.Query]{}.Install(srv)

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
		dagql.Func("resultEmoji", s.resultEmoji).
			Doc("An emoji representing the result of the check"),
	}.Install(srv)
}

func (s checksSchema) checks(ctx context.Context, q *core.Query, args struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.CheckGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return core.CurrentChecks(ctx, include)
}

func (s checksSchema) name(_ context.Context, parent *core.Check, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s checksSchema) resultEmoji(_ context.Context, parent *core.Check, args struct{}) (string, error) {
	return parent.ResultEmoji(), nil
}

func (s checksSchema) list(ctx context.Context, parent *core.CheckGroup, args struct{}) ([]*core.Check, error) {
	return parent.List(ctx)
}

func (s checksSchema) run(ctx context.Context, parent *core.CheckGroup, args struct{}) (*core.CheckGroup, error) {
	return parent.Run(ctx)
}

func (s checksSchema) report(ctx context.Context, parent *core.CheckGroup, args struct{}) (*core.File, error) {
	return parent.Report(ctx)
}

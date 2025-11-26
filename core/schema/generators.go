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
			Doc("Return a list of individual generators and their details"),

		dagql.Func("run", s.run).
			Doc("Execute all selected generators"),

		dagql.NodeFunc("isEmpty", s.groupIsEmpty).
			Doc("Wether changeset from the generator execution is empty or not").
			Args(
				dagql.Arg("continueOnConflicts").Doc(`Continue to merge changes even if conflict exists, like the same file modified by multiple generators`),
			),

		dagql.Func("changes", s.groupChanges).
			Doc(`The combined changes from the generators execution`,
				`If any conflict occurs, for instance if the same file is modified by multiple generators,
				or if a file is both modified and deleted, an error is raised and the merge of the changesets will failed.`,
				`Set 'continueOnConflicts' flag to force to merge the changes in a 'last write wins' strategy.`).
			Args(
				dagql.Arg("continueOnConflicts").Doc(`Continue to merge changes even if conflict exists, like the same file modified by multiple generators`),
			),
	}.Install(srv)

	dagql.Fields[*core.Generator]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the generator"),

		dagql.Func("run", s.runSingleGenerator).
			Doc("Execute the generator"),

		dagql.NodeFunc("isEmpty", s.isEmpty).
			Doc("Wether changeset from the generator execution is empty or not"),
	}.Install(srv)
}

func (s generatorsSchema) list(ctx context.Context, parent *core.GeneratorGroup, args struct{}) ([]*core.Generator, error) {
	return parent.List(ctx)
}

func (s generatorsSchema) run(ctx context.Context, parent *core.GeneratorGroup, args struct{}) (*core.GeneratorGroup, error) {
	return parent.Run(ctx)
}

func (s generatorsSchema) groupIsEmpty(ctx context.Context, parent dagql.ObjectResult[*core.GeneratorGroup], args struct {
	ContinueOnConflicts bool `default:"false"`
}) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	var empty dagql.Boolean
	if err := srv.Select(ctx, parent, &empty,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{Name: "continueOnConflicts", Value: dagql.NewBoolean(args.ContinueOnConflicts)},
			},
		},
		dagql.Selector{
			Field: "isEmpty",
		},
	); err != nil {
		return false, err
	}

	return empty, nil
}

func (s generatorsSchema) groupChanges(ctx context.Context, parent *core.GeneratorGroup, args struct {
	ContinueOnConflicts bool `default:"false"`
}) (*core.Changeset, error) {
	return parent.Changes(ctx, args.ContinueOnConflicts)
}

func (s generatorsSchema) name(_ context.Context, parent *core.Generator, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s generatorsSchema) runSingleGenerator(ctx context.Context, parent *core.Generator, args struct{}) (*core.Generator, error) {
	return parent.Run(ctx)
}

func (s generatorsSchema) isEmpty(ctx context.Context, parent dagql.ObjectResult[*core.Generator], args struct{}) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	var empty dagql.Boolean
	if err := srv.Select(ctx, parent, &empty,
		dagql.Selector{
			Field: "changes",
		},
		dagql.Selector{
			Field: "isEmpty",
		},
	); err != nil {
		return false, err
	}

	return empty, nil
}

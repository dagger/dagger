package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type agentsSchema struct{}

var _ SchemaResolvers = &agentsSchema{}

func (s agentsSchema) Install(srv *dagql.Server) {
	// Agents are v1+ API surface; installing the classes with a view gate also
	// gates their generated ID/load fields.
	srv.InstallObject(dagql.NewClass[*core.AgentGroup](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.Agent](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.AgentGroup]{
		dagql.Func("list", s.list).
			Doc("Return a list of individual agents and their details"),

		dagql.Func("compose", s.compose).
			Doc("Compose all selected agent middlewares onto a base LLM, in alphabetical module:fn order, and return the composed LLM.").
			Args(
				dagql.Arg("base").Doc("The base LLM to compose onto. Defaults to a fresh workspace-bound LLM."),
			),
	}.Install(srv)

	dagql.Fields[*core.Agent]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the agent"),
		dagql.Func("description", s.description).
			Doc("The description of the agent"),
		dagql.Func("path", s.path).
			Doc("The path of the agent within its module"),
		dagql.Func("originalModule", s.originalModule).
			Doc("The original module in which the agent has been defined"),
	}.Install(srv)
}

func (s agentsSchema) list(_ context.Context, parent *core.AgentGroup, args struct{}) ([]*core.Agent, error) {
	return parent.List(), nil
}

func (s agentsSchema) compose(ctx context.Context, parent *core.AgentGroup, args struct {
	Base dagql.Optional[core.LLMID]
}) (dagql.ObjectResult[*core.LLM], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.LLM]{}, err
	}

	var base dagql.ObjectResult[*core.LLM]
	if args.Base.Valid {
		base, err = args.Base.Value.Load(ctx, srv)
		if err != nil {
			return dagql.ObjectResult[*core.LLM]{}, err
		}
	} else {
		// Seed a fresh workspace-bound LLM — the sole base-LLM seed point
		// (hack/designs/workspace-agents.md §3). Every folded leaf then gets base passed explicitly.
		if err := srv.Select(ctx, srv.Root(), &base, dagql.Selector{Field: "llm"}); err != nil {
			return dagql.ObjectResult[*core.LLM]{}, err
		}
	}

	return parent.Compose(ctx, base)
}

func (s agentsSchema) name(_ context.Context, parent *core.Agent, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s agentsSchema) description(_ context.Context, parent *core.Agent, args struct{}) (string, error) {
	return parent.Description(), nil
}

func (s agentsSchema) path(_ context.Context, parent *core.Agent, args struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s agentsSchema) originalModule(_ context.Context, parent *core.Agent, args struct{}) (*core.Module, error) {
	return parent.OriginalModule(), nil
}

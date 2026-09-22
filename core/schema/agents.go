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
	srv.InstallObject(dagql.NewClass[*core.AgentMiddlewareGroup](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.AgentMiddleware](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.AgentMiddlewareGroup]{
		dagql.Func("list", s.list).
			Experimental("Agent APIs are likely to change.").
			Doc("Return a list of individual agents and their details"),

		dagql.Func("recompose", s.recompose).
			View(AfterVersion("v1.0.0-0")).
			Experimental("Agent APIs are likely to change.").
			Doc(
				"Recompose the selected agent middlewares onto an existing LLM, replacing their owned system prompts and tool bindings while preserving tool object state.",
				"Caller-added prompts and unrelated middleware contributions are retained. Prompts from older conversations without ownership metadata are never removed automatically. Other middleware effects retain compose semantics; this is not a general rollback of arbitrary middleware changes.",
				"Existing field values win over new defaults; fields added by the new revision take its defaults. Changing a binding's withTools version resets that object's state to the new defaults instead. With an unchanged version, visibly incompatible state (a public field that changed type, or a value whose shape differs from the new default) is an error. Discarded bindings or a changed module origin are errors regardless of version. The base workspace is preserved.",
			).
			Args(
				dagql.Arg("base").Doc("The existing conversation whose tool state should be preserved."),
			),
		dagql.Func("compose", s.compose).
			Experimental("Agent APIs are likely to change.").
			Doc("Compose all selected agent middlewares onto a base LLM, in alphabetical module:fn order, and return the composed LLM.").
			Args(
				dagql.Arg("base").Doc("The base LLM to compose onto. Defaults to a fresh workspace-bound LLM."),
			),
	}.Install(srv)

	dagql.Fields[*core.AgentMiddleware]{
		dagql.Func("name", s.name).
			Experimental("Agent APIs are likely to change.").
			Doc("Return the command name of the agent. Entrypoint targets omit the module prefix."),
		dagql.Func("description", s.description).
			Experimental("Agent APIs are likely to change.").
			Doc("The description of the agent"),
		dagql.Func("path", s.path).
			Experimental("Agent APIs are likely to change.").
			Doc("The path of the agent within its module"),
		dagql.Func("originalModule", s.originalModule).
			Experimental("Agent APIs are likely to change.").
			Doc("The original module in which the agent has been defined"),
	}.Install(srv)
}

func (s agentsSchema) list(_ context.Context, parent *core.AgentMiddlewareGroup, args struct{}) ([]*core.AgentMiddleware, error) {
	return parent.List(), nil
}

func (s agentsSchema) compose(ctx context.Context, parent *core.AgentMiddlewareGroup, args struct {
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
		// (hack/designs/workspace-agents.md §3). Every folded leaf then gets base
		// passed explicitly. llm() starts unbound (NewLLM no longer binds the
		// ambient workspace), so bind the workspace this group was rolled up from
		// explicitly — an @agent leaf reads it via LLM.workspace and acts on it
		// through its tools.
		sels := []dagql.Selector{{Field: "llm"}}
		if parent.BoundWorkspace.Self() != nil {
			wsID, err := parent.BoundWorkspace.ID()
			if err != nil {
				return dagql.ObjectResult[*core.LLM]{}, err
			}
			sels = append(sels, dagql.Selector{
				Field: "withWorkspace",
				Args: []dagql.NamedInput{{
					Name:  "workspace",
					Value: dagql.NewID[*core.Workspace](wsID),
				}},
			})
		}
		if err := srv.Select(ctx, srv.Root(), &base, sels...); err != nil {
			return dagql.ObjectResult[*core.LLM]{}, err
		}
	}

	return parent.Compose(ctx, base)
}

func (s agentsSchema) recompose(ctx context.Context, parent *core.AgentMiddlewareGroup, args struct {
	Base core.LLMID
}) (dagql.ObjectResult[*core.LLM], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.LLM]{}, err
	}
	base, err := args.Base.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.LLM]{}, err
	}
	return parent.Recompose(ctx, base)
}

func (s agentsSchema) name(_ context.Context, parent *core.AgentMiddleware, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s agentsSchema) description(_ context.Context, parent *core.AgentMiddleware, args struct{}) (string, error) {
	return parent.Description(), nil
}

func (s agentsSchema) path(_ context.Context, parent *core.AgentMiddleware, args struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s agentsSchema) originalModule(_ context.Context, parent *core.AgentMiddleware, args struct{}) (*core.Module, error) {
	return parent.OriginalModule(), nil
}

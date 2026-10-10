package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type agentsSchema struct{}

func (*agentsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.Expertise](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.Expertise]{
		dagql.Func("artifact", func(_ context.Context, a *core.Expertise, _ struct{}) (*core.Artifact, error) {
			return a.Artifact.Clone(), nil
		}).Doc("The artifact this expertise runs."),
		dagql.Func("arguments", func(_ context.Context, a *core.Expertise, _ struct{}) (core.JSON, error) { return a.Arguments, nil }).Doc("Bound arguments besides the conversation, as a JSON object."),
		dagql.Func("name", func(_ context.Context, a *core.Expertise, _ struct{}) (string, error) { return a.Name(), nil }).Doc("The agent function's name."),
		dagql.Func("description", func(_ context.Context, a *core.Expertise, _ struct{}) (string, error) {
			return a.Description(), nil
		}).Doc("The agent function's description."),
		dagql.Func("path", func(_ context.Context, a *core.Expertise, _ struct{}) ([]string, error) { return a.Path(), nil }).Doc("The agent function's path within its module."),
		dagql.Func("originalModule", func(_ context.Context, a *core.Expertise, _ struct{}) (*core.Module, error) {
			return a.OriginalModule(), nil
		}).Doc("The module that defines the agent function."),
	}.Install(srv)
}

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
		}).Doc("The artifact this expertise runs. Look it up again by URI to pick up module changes."),
		dagql.Func("name", func(_ context.Context, a *core.Expertise, _ struct{}) (string, error) { return a.Name(), nil }).Doc("The function's name."),
		dagql.Func("description", func(_ context.Context, a *core.Expertise, _ struct{}) (string, error) {
			return a.Description(), nil
		}).Doc("The function's description."),
		dagql.Func("path", func(_ context.Context, a *core.Expertise, _ struct{}) ([]string, error) { return a.Path(), nil }).Doc("The function's path within its module."),
		dagql.Func("originalModule", func(_ context.Context, a *core.Expertise, _ struct{}) (*core.Module, error) {
			return a.OriginalModule(), nil
		}).Doc("The module that defines the function."),
	}.Install(srv)
}

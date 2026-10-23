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
		// Return the module's own result, not its value: publishing the value
		// again as a second result would attach it a second time, and a module
		// that includes itself in its deps would then name that second result,
		// owned only by this call's session, as its self dependency.
		dagql.Func("originalModule", func(_ context.Context, a *core.Expertise, _ struct{}) (dagql.ObjectResult[*core.Module], error) {
			return a.Artifact.Node.OriginalModule, nil
		}).Doc("The module that defines the function."),
	}.Install(srv)
}

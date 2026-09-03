package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type terminalsSchema struct{}

var _ SchemaResolvers = &terminalsSchema{}

func (s terminalsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.TerminalGroup](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.TerminalTarget](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.TerminalGroup]{
		dagql.Func("list", s.list).
			Doc("Return the selected terminal targets and their details"),
		dagql.NodeFunc("run", s.run).
			DoNotCache("Opens an interactive terminal and then returns the original group.").
			Doc("Open the selected terminal target"),
	}.Install(srv)

	dagql.Fields[*core.TerminalTarget]{
		dagql.Func("name", s.name).
			Doc("Return the fully qualified name of the terminal target"),
		dagql.Func("description", s.description).
			Doc("The description of the terminal target"),
		dagql.Func("path", s.path).
			Doc("The path of the terminal target within its module"),
		dagql.Func("originalModule", s.originalModule).
			Doc("The module in which the terminal target is defined"),
	}.Install(srv)
}

func (s terminalsSchema) list(_ context.Context, parent *core.TerminalGroup, _ struct{}) ([]*core.TerminalTarget, error) {
	return parent.List(), nil
}

func (s terminalsSchema) run(ctx context.Context, parent dagql.ObjectResult[*core.TerminalGroup], _ struct{}) (dagql.ObjectResult[*core.TerminalGroup], error) {
	return parent, parent.Self().Run(ctx)
}

func (s terminalsSchema) name(_ context.Context, parent *core.TerminalTarget, _ struct{}) (string, error) {
	return parent.Name(), nil
}

func (s terminalsSchema) description(_ context.Context, parent *core.TerminalTarget, _ struct{}) (string, error) {
	return parent.Description(), nil
}

func (s terminalsSchema) path(_ context.Context, parent *core.TerminalTarget, _ struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s terminalsSchema) originalModule(_ context.Context, parent *core.TerminalTarget, _ struct{}) (*core.Module, error) {
	return parent.OriginalModule(), nil
}

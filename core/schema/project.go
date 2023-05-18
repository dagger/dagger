package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type projectSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &projectSchema{}

func (s *projectSchema) Name() string {
	return "project"
}

func (s *projectSchema) Schema() string {
	return Project
}

var projectIDResolver = stringResolver(core.ProjectID(""))

var projectCommandIDResolver = stringResolver(core.ProjectCommandID(""))

func (s *projectSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"ProjectID":        projectIDResolver,
		"ProjectCommandID": projectCommandIDResolver,
		"Query": router.ObjectResolver{
			"project":        router.ToResolver(s.project),
			"projectCommand": router.ToResolver(s.projectCommand),
		},
		"Project": router.ObjectResolver{
			"id":       router.ToResolver(s.projectID),
			"name":     router.ToResolver(s.projectName),
			"load":     router.ToResolver(s.load),
			"commands": router.ToResolver(s.commands),
		},
		"ProjectCommand": router.ObjectResolver{
			"id": router.ToResolver(s.projectCommandID),
		},
	}
}

func (s *projectSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type projectArgs struct {
	ID core.ProjectID
}

func (s *projectSchema) project(ctx *router.Context, parent *core.Query, args projectArgs) (*core.Project, error) {
	return core.NewProject(args.ID, s.platform)
}

func (s *projectSchema) projectID(ctx *router.Context, parent *core.Project, args any) (core.ProjectID, error) {
	return parent.ID()
}

func (s *projectSchema) projectName(ctx *router.Context, parent *core.Project, args any) (string, error) {
	return parent.Config.Name, nil
}

type loadArgs struct {
	Source     core.DirectoryID
	ConfigPath string
}

func (s *projectSchema) load(ctx *router.Context, parent *core.Project, args loadArgs) (*core.Project, error) {
	source, err := args.Source.ToDirectory()
	if err != nil {
		return nil, err
	}
	progSock := &core.Socket{HostPath: s.progSock}
	return parent.Load(ctx, s.gw, s.router, progSock, source, args.ConfigPath)
}

func (s *projectSchema) commands(ctx *router.Context, parent *core.Project, args any) ([]core.ProjectCommand, error) {
	return parent.Commands(ctx)
}

type projectCommandArgs struct {
	ID core.ProjectCommandID
}

func (s *projectSchema) projectCommand(ctx *router.Context, parent *core.Query, args projectCommandArgs) (*core.ProjectCommand, error) {
	return core.NewProjectCommand(args.ID)
}

func (s *projectSchema) projectCommandID(ctx *router.Context, parent *core.ProjectCommand, args any) (core.ProjectCommandID, error) {
	return parent.ID()
}

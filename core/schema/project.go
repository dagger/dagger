package schema

import (
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

type projectSchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &projectSchema{}

func (s *projectSchema) Name() string {
	return "project"
}

func (s *projectSchema) Schema() string {
	return Project
}

var projectIDResolver = stringResolver(core.ProjectID(""))

var projectCommandIDResolver = stringResolver(core.ProjectCommandID(""))

func (s *projectSchema) Resolvers() Resolvers {
	return Resolvers{
		"ProjectID":        projectIDResolver,
		"ProjectCommandID": projectCommandIDResolver,
		"Query": ObjectResolver{
			"project":        ToResolver(s.project),
			"projectCommand": ToResolver(s.projectCommand),
		},
		"Project": ObjectResolver{
			"id":       ToResolver(s.projectID),
			"name":     ToResolver(s.projectName),
			"load":     ToResolver(s.load),
			"commands": ToResolver(s.commands),
		},
		"ProjectCommand": ObjectResolver{
			"id": ToResolver(s.projectCommandID),
		},
	}
}

func (s *projectSchema) Dependencies() []ExecutableSchema {
	return nil
}

type projectArgs struct {
	ID core.ProjectID
}

func (s *projectSchema) project(ctx *core.Context, parent *core.Query, args projectArgs) (*core.Project, error) {
	return core.NewProject(args.ID, s.platform)
}

func (s *projectSchema) projectID(ctx *core.Context, parent *core.Project, args any) (core.ProjectID, error) {
	return parent.ID()
}

func (s *projectSchema) projectName(ctx *core.Context, parent *core.Project, args any) (string, error) {
	return parent.Config.Name, nil
}

type loadArgs struct {
	Source     core.DirectoryID
	ConfigPath string
}

func (s *projectSchema) load(ctx *core.Context, parent *core.Project, args loadArgs) (*core.Project, error) {
	source, err := args.Source.ToDirectory()
	if err != nil {
		return nil, err
	}
	proj, resolver, err := parent.Load(ctx, s.bk, s.progSockPath, source.Pipeline, source, args.ConfigPath)
	if err != nil {
		return nil, err
	}

	resolvers := make(Resolvers)
	doc, err := parser.ParseSchema(&ast.Source{Input: proj.Schema})
	if err != nil {
		return nil, err
	}
	for _, def := range append(doc.Definitions, doc.Extensions...) {
		def := def
		if def.Kind != ast.Object {
			continue
		}
		objResolver := ObjectResolver{}
		resolvers[def.Name] = objResolver
		for _, field := range def.Fields {
			field := field
			objResolver[field.Name] = ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
				res, err := resolver(ctx, parent, args)
				if err != nil {
					return nil, err
				}
				return convertOutput(res, field.Type, s.MergedSchemas)
			})
		}
	}
	if err := s.addSchemas(StaticSchema(StaticSchemaParams{
		Name:      proj.Config.Name,
		Schema:    proj.Schema,
		Resolvers: resolvers,
	})); err != nil {
		// TODO: return nil, fmt.Errorf("failed to install project schema: %w", err)
		return nil, fmt.Errorf("failed to install project schema: %w: %s", err, proj.Schema)
	}

	return proj, nil
}

func (s *projectSchema) commands(ctx *core.Context, parent *core.Project, args any) ([]core.ProjectCommand, error) {
	return parent.Commands(ctx)
}

type projectCommandArgs struct {
	ID core.ProjectCommandID
}

func (s *projectSchema) projectCommand(ctx *core.Context, parent *core.Query, args projectCommandArgs) (*core.ProjectCommand, error) {
	return core.NewProjectCommand(args.ID)
}

func (s *projectSchema) projectCommandID(ctx *core.Context, parent *core.ProjectCommand, args any) (core.ProjectCommandID, error) {
	return parent.ID()
}

func convertOutput(output any, outputType *ast.Type, s *MergedSchemas) (any, error) {
	if outputType.Elem != nil {
		outputType = outputType.Elem
	}

	for objectName, resolver := range s.resolvers() {
		if objectName != outputType.Name() {
			continue
		}
		resolver, ok := resolver.(IDableObjectResolver)
		if !ok {
			continue
		}

		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and project container.
		outputStr, ok := output.(string)
		if !ok {
			return nil, fmt.Errorf("expected id string output for %s", objectName)
		}
		return resolver.FromID(outputStr)
	}
	return output, nil
}

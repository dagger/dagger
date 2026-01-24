package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Workspace]{
		dagql.NodeFunc("directory", s.workspaceDirectory).
			Doc(`Load a directory from the workspace.`).
			Args(
				dagql.Arg("path").Doc(`The path to the directory within the workspace.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFunc("file", s.workspaceFile).
			Doc(`Load a file from the workspace.`).
			Args(
				dagql.Arg("path").Doc(`The path to the file within the workspace.`),
			),
	}.Install(dag)

	// Internal query for creating Workspace from ModuleSource (used for Workspace arg injection)
	dag.Root().Extend(
		dagql.NodeFunc("_workspace", s.createWorkspace).
			Doc(`Create a Workspace from a ModuleSource. Internal use only.`).
			Args(
				dagql.Arg("source").Doc(`The module source ID to create workspace from.`),
			),
	)
}

func (s *workspaceSchema) workspaceDirectory(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	args struct {
		Path      string
		Exclude   dagql.Optional[dagql.ArrayInput[dagql.String]]
		Include   dagql.Optional[dagql.ArrayInput[dagql.String]]
		Gitignore dagql.Optional[dagql.Boolean]
	},
) (inst dagql.ObjectResult[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	filter := core.CopyFilter{}
	if args.Exclude.Valid {
		for _, pattern := range args.Exclude.Value {
			filter.Exclude = append(filter.Exclude, string(pattern))
		}
	}
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			filter.Include = append(filter.Include, string(pattern))
		}
	}
	if args.Gitignore.Valid {
		filter.Gitignore = args.Gitignore.Value.Bool()
	}

	src := ws.Self().ModuleSource()
	return src.LoadContextDir(ctx, dag, args.Path, filter)
}

func (s *workspaceSchema) workspaceFile(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	args struct {
		Path string
	},
) (inst dagql.ObjectResult[*core.File], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	src := ws.Self().ModuleSource()
	return src.LoadContextFile(ctx, dag, args.Path)
}

// createWorkspace creates a Workspace from a ModuleSource.
// This is an internal function used to inject Workspace arguments in function calls.
func (s *workspaceSchema) createWorkspace(
	ctx context.Context,
	_ *core.Query,
	args struct {
		Source dagql.ID[*core.ModuleSource]
	},
) (*core.Workspace, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	sourceResult, err := args.Source.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source: %w", err)
	}

	return &core.Workspace{
		Source: sourceResult,
	}, nil
}

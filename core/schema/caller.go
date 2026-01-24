package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type callerSchema struct{}

var _ SchemaResolvers = &callerSchema{}

func (s *callerSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Caller]{
		dagql.NodeFunc("directory", s.callerDirectory).
			Doc(`Load a directory from the caller's context.`).
			Args(
				dagql.Arg("path").Doc(`The path to the directory within the caller's context.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFunc("file", s.callerFile).
			Doc(`Load a file from the caller's context.`).
			Args(
				dagql.Arg("path").Doc(`The path to the file within the caller's context.`),
			),
	}.Install(dag)

	// Internal query for creating Caller from ModuleSource (used for Caller arg injection)
	dag.Root().Extend(
		dagql.NodeFunc("_caller", s.createCaller).
			Doc(`Create a Caller from a ModuleSource. Internal use only.`).
			Args(
				dagql.Arg("source").Doc(`The module source ID to create caller from.`),
			),
	)
}

func (s *callerSchema) callerDirectory(
	ctx context.Context,
	caller dagql.ObjectResult[*core.Caller],
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

	src := caller.Self().ModuleSource()
	return src.LoadContextDir(ctx, dag, args.Path, filter)
}

func (s *callerSchema) callerFile(
	ctx context.Context,
	caller dagql.ObjectResult[*core.Caller],
	args struct {
		Path string
	},
) (inst dagql.ObjectResult[*core.File], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	src := caller.Self().ModuleSource()
	return src.LoadContextFile(ctx, dag, args.Path)
}

// createCaller creates a Caller from a ModuleSource.
// This is an internal function used to inject Caller arguments in function calls.
func (s *callerSchema) createCaller(
	ctx context.Context,
	_ *core.Query,
	args struct {
		Source dagql.ID[*core.ModuleSource]
	},
) (*core.Caller, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	sourceResult, err := args.Source.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source: %w", err)
	}

	return &core.Caller{
		Source: sourceResult,
	}, nil
}

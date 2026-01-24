package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type contextSchema struct{}

var _ SchemaResolvers = &contextSchema{}

func (s *contextSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Context]{
		dagql.NodeFunc("directory", s.contextDirectory).
			Doc(`Load a directory from this context.`).
			Args(
				dagql.Arg("path").Doc(`The path to the directory within the context.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFunc("file", s.contextFile).
			Doc(`Load a file from this context.`).
			Args(
				dagql.Arg("path").Doc(`The path to the file within the context.`),
			),
	}.Install(dag)
}

func (s *contextSchema) contextDirectory(
	ctx context.Context,
	ctxObj dagql.ObjectResult[*core.Context],
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

	src := ctxObj.Self().ModuleSource()
	return src.LoadContextDir(ctx, dag, args.Path, filter)
}

func (s *contextSchema) contextFile(
	ctx context.Context,
	ctxObj dagql.ObjectResult[*core.Context],
	args struct {
		Path string
	},
) (inst dagql.ObjectResult[*core.File], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	src := ctxObj.Self().ModuleSource()
	return src.LoadContextFile(ctx, dag, args.Path)
}

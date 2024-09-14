package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type errorSchema struct {
	dag *dagql.Server
}

var _ SchemaResolvers = &errorSchema{}

func (s *errorSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("error", s.error).
			Doc(`Create a new error.`).
			ArgDoc("message", `A brief description of the error.`),
	}.Install(s.dag)

	dagql.Fields[*core.Error]{}.Install(s.dag)
}

func (s *errorSchema) error(ctx context.Context, _ *core.Query, args struct {
	Message string `doc:"A description of the error."`
}) (*core.Error, error) {
	return &core.Error{
		Message: args.Message,
	}, nil
}

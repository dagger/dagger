package schema

import (
	"context"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type errorSchema struct{}

var _ SchemaResolvers = &errorSchema{}

func (s *errorSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("error", s.error).
			Doc(`Create a new error.`).
			Args(
				dagql.Arg("message").Doc(`A brief description of the error.`),
			),
	}.Install(dag)

	dagql.Fields[*core.Error]{
		dagql.Func("withValue", s.withValue).
			Doc(`Add a value to the error.`),
	}.Install(dag)

	dagql.Fields[*core.ErrorValue]{}.Install(dag)
}

func (s *errorSchema) error(ctx context.Context, _ *core.Query, args struct {
	Message string `doc:"A description of the error."`
}) (*core.Error, error) {
	// We don't want to see these in the UI
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool(telemetry.UIInternalAttr, true))
	return &core.Error{
		Message: args.Message,
	}, nil
}

func (s *errorSchema) withValue(ctx context.Context, self *core.Error, args struct {
	Name  string    `doc:"The name of the value."`
	Value core.JSON `doc:"The value to store on the error."`
}) (*core.Error, error) {
	return self.WithValue(args.Name, args.Value), nil
}

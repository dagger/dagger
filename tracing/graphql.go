package tracing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var _ graphql.Extension = &GraphQLTracer{}

type GraphQLTracer struct {
}

func (t *GraphQLTracer) Init(ctx context.Context, p *graphql.Params) context.Context {
	return ctx
}

func (t *GraphQLTracer) Name() string {
	return "opentelemetry"
}

func (t *GraphQLTracer) HasResult() bool {
	return false
}

func (t *GraphQLTracer) GetResult(ctx context.Context) interface{} {
	return nil
}

func (t *GraphQLTracer) ParseDidStart(ctx context.Context) (context.Context, graphql.ParseFinishFunc) {
	return ctx, func(error) {}

	// FIXME: need a parent span
	// _, span := Tracer.Start(ctx, "parse")
	// return ctx, func(err error) {
	// 	if err != nil {
	// 		span.RecordError(err)
	// 		span.SetStatus(codes.Error, err.Error())
	// 	}
	// 	span.End()
	// }
}

func (t *GraphQLTracer) ValidationDidStart(ctx context.Context) (context.Context, graphql.ValidationFinishFunc) {
	return ctx, func([]gqlerrors.FormattedError) {}

	// FIXME: need a parent span
	// _, span := Tracer.Start(ctx, "validation")
	// return ctx, func(errs []gqlerrors.FormattedError) {
	// 	for _, err := range errs {
	// 		span.RecordError(err)
	// 		span.SetStatus(codes.Error, err.Error())
	// 	}
	// 	span.End()
	// }
}

func (t *GraphQLTracer) ExecutionDidStart(ctx context.Context) (context.Context, graphql.ExecutionFinishFunc) {
	return ctx, func(*graphql.Result) {}

	// FIXME: need to filter out __schema queries
	// _, span := Tracer.Start(ctx, "query")
	// return ctx, func(result *graphql.Result) {
	// 	for _, err := range result.Errors {
	// 		span.RecordError(err)
	// 		span.SetStatus(codes.Error, err.Message)
	// 	}
	// 	if result.Data != nil {
	// 		serialized, err := json.MarshalIndent(result.Data, "", "  ")
	// 		if err == nil {
	// 			span.SetAttributes(attribute.String("data", string(serialized)))
	// 		}
	// 	}

	// 	span.End()
	// }
}

func (t *GraphQLTracer) ResolveFieldDidStart(ctx context.Context, i *graphql.ResolveInfo) (context.Context, graphql.ResolveFieldFinishFunc) {
	if i.Path.AsArray()[0].(string) == "__schema" {
		return ctx, func(v interface{}, err error) {
		}
	}
	ctx, span := Tracer.Start(ctx, fmt.Sprintf("%s", i.Path.Key))
	return ctx, func(v interface{}, err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		if v != nil {
			serialized, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				// span.AddEvent("event", trace.WithAttributes(attribute.String("value", string(serialized))))
				span.SetAttributes(attribute.String("value", string(serialized)))
			}
		}
		span.End()
	}
}

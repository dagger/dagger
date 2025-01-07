package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"go.opentelemetry.io/otel/codes"
)

type querySchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &querySchema{}

func (s *querySchema) Install() {
	introspection.Install[*core.Query](s.srv)

	s.srv.InstallScalar(core.JSON{})
	s.srv.InstallScalar(core.Void{})

	core.NetworkProtocols.Install(s.srv)
	core.ImageLayerCompressions.Install(s.srv)
	core.ImageMediaTypesEnum.Install(s.srv)
	core.CacheSharingModes.Install(s.srv)
	core.TypeDefKinds.Install(s.srv)
	core.ModuleSourceKindEnum.Install(s.srv)
	core.ReturnTypesEnum.Install(s.srv)

	dagql.MustInputSpec(PipelineLabel{}).Install(s.srv)
	dagql.MustInputSpec(core.PortForward{}).Install(s.srv)
	dagql.MustInputSpec(core.BuildArg{}).Install(s.srv)

	dagql.Fields[EnvVariable]{}.Install(s.srv)

	dagql.Fields[core.Port]{}.Install(s.srv)

	dagql.Fields[Label]{}.Install(s.srv)

	dagql.Fields[core.SpanContext]{}.Install(s.srv)

	dagql.Fields[*core.Query]{
		dagql.Func("pipeline", s.pipeline).
			View(BeforeVersion("v0.13.0")).
			Deprecated("Explicit pipeline creation is now a no-op").
			Doc("Creates a named sub-pipeline.").
			ArgDoc("name", "Name of the sub-pipeline.").
			ArgDoc("description", "Description of the sub-pipeline.").
			ArgDoc("labels", "Labels to apply to the sub-pipeline."),

		dagql.Func("version", s.version).
			Doc(`Get the current Dagger Engine version.`),

		dagql.Func("span", s.start).
			Doc(`Create a new OpenTelemetry span.`),
	}.Install(s.srv)

	dagql.Fields[*core.Span]{
		dagql.Func("end", s.spanEnd).
			Doc(`End the OpenTelemetry span, with an optional error.`),
	}.Install(s.srv)
}

type pipelineArgs struct {
	Name        string
	Description string `default:""`
	Labels      dagql.Optional[dagql.ArrayInput[dagql.InputObject[PipelineLabel]]]
}

func (s *querySchema) pipeline(ctx context.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
	return parent.WithPipeline(args.Name, args.Description), nil
}

func (s *querySchema) version(_ context.Context, _ *core.Query, args struct{}) (string, error) {
	return engine.Version, nil
}

func (s *querySchema) start(ctx context.Context, parent *core.Query, args struct {
	Name string
}) (*core.Span, error) {
	// First, grab the tracer based on the incoming (real) span.
	tracer := core.Tracer(ctx)
	// Overwrite the span in the context so we inherit from the query's span.
	ctx = parent.SpanContext.ToContext(ctx)
	// Start a span beneath the query span.
	ctx, span := tracer.Start(ctx, args.Name)
	// Update the query with the new span context.
	child := parent.Clone()
	child.SpanContext = core.SpanContextFromContext(ctx)
	return &core.Span{
		Span:  span,
		Query: child,
	}, nil
}

func (s *querySchema) spanEnd(ctx context.Context, parent *core.Span, args struct {
	Error dagql.Optional[dagql.ID[*core.Error]]
}) (dagql.Nullable[core.Void], error) {
	if args.Error.Valid {
		dagErr, err := args.Error.Value.Load(ctx, s.srv)
		if err != nil {
			parent.Span.SetStatus(codes.Error, fmt.Sprintf("failed to load error: %v", err))
		} else {
			parent.Span.SetStatus(codes.Error, dagErr.Self.Message)
		}
	}
	parent.Span.End()
	return dagql.Null[core.Void](), nil
}

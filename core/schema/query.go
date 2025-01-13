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

		dagql.Func("span", s.span).
			Doc(`Create a new OpenTelemetry span.`).
			ArgDoc("name", "Name of the span."),

		//
		// dagql.Func("log", s.log).
		// 	Doc(`Log a message.`).
		// 	ArgDoc("message", "The message to log.").
		// 	Impure("Always evaluated for side effects (logging)."),
		dagql.Func("currentSpan", s.span),
	}.Install(s.srv)

	dagql.Fields[*core.Span]{
		// dagql.Func("logs", s.spanLogs),

		dagql.Func("withActor", s.spanWithActor),

		dagql.Func("withInternal", s.spanWithInternal),

		dagql.Func("internalId", s.spanInternalID).
			Doc(`Returns the internal ID of the span.`),

		dagql.NodeFunc("start", s.spanStart).
			Doc(`Start a new instance of the span.`).
			Impure("Creates a new span with each call."),

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

func (s *querySchema) span(ctx context.Context, parent *core.Query, args struct {
	Name string
	Key  string `default:""`
}) (*core.Span, error) {
	query := parent
	if args.Key != "" {
		span, found := query.LookupSpan(args.Key)
		if !found {
			return nil, fmt.Errorf("span not found: %s", args.Key)
		}
		return span, nil
	}
	return &core.Span{
		Name:  args.Name,
		Query: parent,
	}, nil
}

func (s *querySchema) spanStart(ctx context.Context, parent dagql.Instance[*core.Span], args struct{}) (dagql.ID[*core.Span], error) {
	started := parent.Self.Start(ctx)
	var inst dagql.Instance[*core.Span]
	err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "span",
		Pure:  true,
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(started.Name)},
			{Name: "key", Value: dagql.NewString(started.InternalID())},
		},
	})
	if err != nil {
		return dagql.ID[*core.Span]{}, err
	}
	return dagql.NewID[*core.Span](inst.ID()), nil
}

func (s *querySchema) spanEnd(ctx context.Context, parent *core.Span, args struct {
	Error dagql.Optional[dagql.ID[*core.Error]]
}) (dagql.Nullable[core.Void], error) {
	if parent.Span == nil {
		return dagql.Null[core.Void](), fmt.Errorf("span not started")
	}
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

//	func (s *querySchema) spanLogs(ctx context.Context, parent dagql.Instance[*core.Span], args struct{}) (dagql.ID[*core.Span], error) {
//		started := parent.Self.Start(ctx)
//		var inst dagql.Instance[*core.Span]
//		err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
//			Field: "span",
//			Pure:  true,
//			Args: []dagql.NamedInput{
//				{Name: "name", Value: dagql.NewString(started.Name)},
//				{Name: "key", Value: dagql.NewString(started.InternalID())},
//			},
//		})
//		if err != nil {
//			return dagql.ID[*core.Span]{}, err
//		}
//		return dagql.NewID[*core.Span](inst.ID()), nil
//	}

func (s *querySchema) spanInternalID(ctx context.Context, parent *core.Span, args struct{}) (string, error) {
	return parent.Span.SpanContext().SpanID().String(), nil
}

func (s *querySchema) spanWithActor(ctx context.Context, parent *core.Span, args struct {
	Actor string
}) (*core.Span, error) {
	return parent.WithActor(args.Actor), nil
}

func (s *querySchema) spanWithInternal(ctx context.Context, parent *core.Span, args struct{}) (*core.Span, error) {
	return parent.WithInternal(), nil
}

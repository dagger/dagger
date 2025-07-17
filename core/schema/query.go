package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	"dagger.io/dagger/telemetry"
	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/blob"
	"go.opentelemetry.io/otel/codes"
)

type querySchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &querySchema{}

func (s *querySchema) Install() {
	introspection.Install[*core.Query](s.srv)
	dagql.Fields[*core.Query]{
		// Augment introspection with an API that returns the current schema serialized to
		// JSON and written to a core.File. This is currently used internally for calling
		// module SDKs and is thus hidden the same way the rest of introspection is hidden
		// (via the magic __ prefix).
		dagql.NodeFuncWithCacheKey("__schemaJSONFile", s.schemaJSONFile,
			dagql.CachePerSchema[*core.Query, schemaJSONArgs](s.srv)).
			Doc("Get the current schema as a JSON file.").
			Args(
				dagql.Arg("hiddenTypes").Doc("Types to hide from the schema JSON file."),
			),
	}.Install(s.srv)

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
			Args(
				dagql.Arg("name").Doc("Name of the sub-pipeline."),
				dagql.Arg("description").Doc("Description of the sub-pipeline."),
				dagql.Arg("labels").Doc("Labels to apply to the sub-pipeline."),
			),

		dagql.Func("version", s.version).
			Doc(`Get the current Dagger Engine version.`),

		dagql.Func("status", s.status).
			Doc(`Create a new status indicator.`).
			Experimental(
				"The statuses API is experimental and subject to change.",
				"The current capabilities are limited. Please open an issue to request new UI controls.",
			).
			Args(
				dagql.Arg("name").Doc("A display name for the status."),
			),
	}.Install(s.srv)

	dagql.Fields[*core.Status]{
		dagql.NodeFuncWithCacheKey("start", s.statusStart, dagql.CachePerCall).
			Doc(`Start a new instance of the status.`),

		dagql.NodeFuncWithCacheKey("display", s.statusDisplay, dagql.CachePerCall).
			Doc(`Start and immediately finish the status, so that it just gets displayed to the user.`),

		dagql.Func("end", s.statusEnd).
			Doc(`Mark the status as complete, with an optional error.`),

		dagql.Func("internalId", s.statusInternalID).
			Doc(
				`Returns the internal OpenTelemetry span ID of the status.`,
				`(You probably don't need to use this, unless you're implementing OpenTelemetry integration for a Dagger SDK.)`,
			),
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

type schemaJSONArgs struct {
	HiddenTypes []string `default:"[]"`
}

func (s *querySchema) schemaJSONFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args schemaJSONArgs,
) (inst dagql.Result[*core.File], rerr error) {
	data, err := s.srv.Query(ctx, codegenintrospection.Query, nil)
	if err != nil {
		return inst, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return inst, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	var introspection codegenintrospection.Response
	if err := json.Unmarshal(jsonBytes, &introspection); err != nil {
		return inst, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}

	for _, typed := range core.TypesHiddenFromModuleSDKs {
		introspection.Schema.ScrubType(typed.Type().Name())
		introspection.Schema.ScrubType(dagql.IDTypeNameFor(typed))
	}

	for _, rawType := range args.HiddenTypes {
		introspection.Schema.ScrubType(rawType)
		introspection.Schema.ScrubType(dagql.IDTypeNameForRawType(rawType))
	}

	moduleSchemaJSON, err := json.Marshal(introspection)
	if err != nil {
		return inst, fmt.Errorf("failed to marshal introspection JSON: %w", err)
	}

	const schemaJSONFilename = "schema.json"
	const perm fs.FileMode = 0644

	f, err := core.NewFileWithContents(ctx, schemaJSONFilename, moduleSchemaJSON, perm, nil, parent.Self().Platform())
	if err != nil {
		return inst, err
	}
	bk, err := parent.Self().Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	dgst, err := core.GetContentHashFromDef(ctx, bk, f.LLB, "/")
	if err != nil {
		return inst, fmt.Errorf("failed to get content hash: %w", err)
	}

	// LLB marshalling takes up too much memory when file ops have a ton of contents, so we still go through
	// the blob source for now simply to avoid that.
	f, err = core.NewFileSt(ctx, blob.LLB(dgst), f.File, f.Platform, f.Services)
	if err != nil {
		return inst, err
	}

	fileInst, err := dagql.NewResultForCurrentID(ctx, f)
	if err != nil {
		return inst, err
	}

	return fileInst.WithDigest(dgst), nil
}

func (s *querySchema) status(ctx context.Context, parent *core.Query, args struct {
	Name string
	Key  string `default:""`
}) (*core.Status, error) {
	query := parent
	if args.Key != "" {
		status, found, err := query.LookupStatus(ctx, args.Key)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("status not found: %s", args.Key)
		}
		return status, nil
	}
	return &core.Status{
		Name: args.Name,
	}, nil
}

func (s *querySchema) statusStart(ctx context.Context, parent dagql.ObjectResult[*core.Status], args struct{}) (dagql.ID[*core.Status], error) {
	st, err := parent.Self().Start(ctx)
	if err != nil {
		return dagql.ID[*core.Status]{}, err
	}
	return s.selectStatus(ctx, st)
}

func (s *querySchema) statusDisplay(ctx context.Context, parent dagql.ObjectResult[*core.Status], args struct{}) (dagql.ID[*core.Status], error) {
	st, err := parent.Self().Display(ctx)
	if err != nil {
		return dagql.ID[*core.Status]{}, err
	}
	return s.selectStatus(ctx, st)
}

func (s *querySchema) selectStatus(ctx context.Context, started *core.Status) (dagql.ID[*core.Status], error) {
	var inst dagql.ObjectResult[*core.Status]
	err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "status",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(started.Name)},
			{Name: "key", Value: dagql.NewString(started.InternalID())},
		},
	})
	if err != nil {
		return dagql.ID[*core.Status]{}, err
	}
	return dagql.NewID[*core.Status](inst.ID()), nil
}

func (s *querySchema) statusEnd(ctx context.Context, parent *core.Status, args struct {
	Error dagql.Optional[dagql.ID[*core.Error]]
}) (dagql.Nullable[core.Void], error) {
	if parent.Span == nil {
		return dagql.Null[core.Void](), fmt.Errorf("status not started")
	}
	if args.Error.Valid {
		dagErr, err := args.Error.Value.Load(ctx, s.srv)
		if err != nil {
			parent.Span.SetStatus(codes.Error, fmt.Sprintf("failed to load error: %v", err))
		} else {
			// use telemetry.End which also provides origin tracking
			telemetry.End(parent.Span, func() error { return dagErr.Self() })
		}
	} else {
		// use telemetry.End so the status gets set to OK
		telemetry.End(parent.Span, func() error { return nil })
	}
	return dagql.Null[core.Void](), nil
}

func (s *querySchema) statusInternalID(ctx context.Context, parent *core.Status, args struct{}) (string, error) {
	return parent.Span.SpanContext().SpanID().String(), nil
}

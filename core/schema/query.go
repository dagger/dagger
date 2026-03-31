package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
)

type querySchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &querySchema{}

func (s *querySchema) Install(srv *dagql.Server) {
	introspection.Install[*core.Query](srv)
	dagql.Fields[*core.Query]{
		// Augment introspection with an API that returns the current schema serialized to
		// JSON and written to a core.File. This is currently used internally for calling
		// module SDKs and is thus hidden the same way the rest of introspection is hidden
		// (via the magic __ prefix).
		dagql.NodeFuncWithCacheKey("__schemaJSONFile", s.schemaJSONFile,
			dagql.CachePerSchema[*core.Query, schemaJSONArgs](srv)).
			Doc("Get the current schema as a JSON file.").
			Args(
				dagql.Arg("hiddenTypes").Doc("Types to hide from the schema JSON file."),
			),
	}.Install(srv)

	srv.InstallScalar(core.JSON{})
	srv.InstallScalar(core.Void{})
	core.NetworkProtocols.Install(srv)
	core.ImageLayerCompressions.Install(srv)
	core.ImageMediaTypesEnum.Install(srv)
	core.CacheSharingModes.Install(srv)
	core.TypeDefKinds.Install(srv)
	core.ModuleSourceKindEnum.Install(srv)
	core.ReturnTypesEnum.Install(srv)
	core.ModuleSourceExperimentalFeatures.Install(srv)
	core.FunctionCachePolicyEnum.Install(srv)

	dagql.MustInputSpec(PipelineLabel{}).Install(srv)
	dagql.MustInputSpec(core.PortForward{}).Install(srv)
	dagql.MustInputSpec(core.BuildArg{}).Install(srv)

	dagql.Fields[core.EnvVariable]{}.Install(srv)

	dagql.Fields[core.Port]{}.Install(srv)

	dagql.Fields[Label]{}.Install(srv)
	dagql.Fields[HealthcheckConfig]{}.Install(srv)

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
	}.Install(srv)
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

func getSchemaJSON(hiddenTypes []string, view call.View, srv *dagql.Server) ([]byte, error) {
	dagqlSchema := introspection.WrapSchema(srv.SchemaForView(view))

	introspectionResponse := codegenintrospection.Response{
		SchemaVersion: string(view),
		Schema:        &codegenintrospection.Schema{},
	}
	if queryName := dagqlSchema.QueryType().Name(); queryName != nil {
		introspectionResponse.Schema.QueryType.Name = *queryName
	}
	for _, dagqlType := range dagqlSchema.Types() {
		codeGenType, err := core.DagqlToCodegenType(dagqlType)
		if err != nil {
			return nil, err
		}
		introspectionResponse.Schema.Types = append(introspectionResponse.Schema.Types, codeGenType)
	}
	directives, err := dagqlSchema.Directives()
	if err != nil {
		return nil, err
	}
	for _, dagqlDirective := range directives {
		dd, err := core.DagqlToCodegenDirectiveDef(dagqlDirective)
		if err != nil {
			return nil, err
		}
		introspectionResponse.Schema.Directives = append(introspectionResponse.Schema.Directives, dd)
	}

	for _, rawType := range hiddenTypes {
		introspectionResponse.Schema.ScrubType(rawType)
		introspectionResponse.Schema.ScrubType(dagql.IDTypeNameForRawType(rawType))
	}

	moduleSchemaJSON, err := json.Marshal(introspectionResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection JSON: %w", err)
	}
	return moduleSchemaJSON, nil
}

type schemaJSONArgs struct {
	HiddenTypes []string `default:"[]"`
	Schema      string   `internal:"true" default:"" name:"schema"`
	RawDagOpInternalArgs
}

func (s *querySchema) schemaJSONFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args schemaJSONArgs,
) (inst dagql.ObjectResult[*core.File], rerr error) {
	const schemaJSONFilename = "schema.json"
	const perm fs.FileMode = 0644

	if args.InDagOp() {
		f, err := core.NewFileWithContents(ctx, schemaJSONFilename, []byte(args.Schema), perm, nil, parent.Self().Platform())
		if err != nil {
			return inst, err
		}

		return dagql.NewObjectResultForCurrentID(ctx, s.srv, f)
	}

	moduleSchemaJSON, err := getSchemaJSON(args.HiddenTypes, s.srv.View, s.srv)
	if err != nil {
		return inst, err
	}
	args.Schema = string(moduleSchemaJSON)

	newID := dagql.CurrentID(ctx).
		WithArgument(call.NewArgument(
			"schema",
			call.NewLiteralString(args.Schema),
			false,
		))
	ctxDagOp := dagql.ContextWithID(ctx, newID)

	f, effectID, err := DagOpFile(ctxDagOp, s.srv, parent.Self(), args, nil, WithStaticPath[*core.Query, schemaJSONArgs](schemaJSONFilename))
	if err != nil {
		return inst, err
	}

	if _, err := f.Evaluate(ctx); err != nil {
		return inst, err
	}

	curID := dagql.CurrentID(ctx)
	if effectID != "" {
		curID = curID.AppendEffectIDs(effectID)
	}
	return dagql.NewObjectResultForID(f, s.srv, curID)
}

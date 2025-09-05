package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/blob"
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

	dagql.MustInputSpec(PipelineLabel{}).Install(srv)
	dagql.MustInputSpec(core.PortForward{}).Install(srv)
	dagql.MustInputSpec(core.BuildArg{}).Install(srv)

	dagql.Fields[EnvVariable]{}.Install(srv)

	dagql.Fields[core.Port]{}.Install(srv)

	dagql.Fields[Label]{}.Install(srv)

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
	def, err := f.LLB(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get LLB definition: %w", err)
	}
	dgst, err := core.GetContentHashFromDef(ctx, bk, def, "/")
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

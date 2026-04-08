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
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type querySchema struct {
}

var _ SchemaResolvers = &querySchema{}

func (s *querySchema) Install(srv *dagql.Server) {
	introspection.Install[*core.Query](srv)
	dagql.Fields[*core.Query]{
		// Augment introspection with an API that returns the current schema serialized to
		// JSON and written to a core.File. This is currently used internally for calling
		// module SDKs and is thus hidden the same way the rest of introspection is hidden
		// (via the magic __ prefix).
		dagql.NodeFunc("__schemaJSONFile", s.schemaJSONFile).
			IsPersistable().
			WithInput(dagql.CurrentSchemaInput).
			Doc("Get the current schema as a JSON file.").
			Args(
				dagql.Arg("hiddenTypes").Doc("Types to hide from the schema JSON file."),
			),
		dagql.NodeFunc("_remoteGitMirror", s.remoteGitMirror).
			IsPersistable().
			Doc(`(Internal-only) Returns the persistent bare git mirror for a remote URL.`).
			Args(
				dagql.Arg("remoteURL").Doc("Normalized remote repository URL."),
			),
		dagql.NodeFunc("_clientFilesyncMirror", s.clientFilesyncMirror).
			IsPersistable().
			Doc(`(Internal-only) Returns the persistent filesync mirror for a stable client and drive.`).
			Args(
				dagql.Arg("stableClientID").Doc("Stable client identifier."),
				dagql.Arg("drive").Doc("Drive prefix for Windows clients; empty otherwise."),
			),
	}.Install(srv)

	srv.InstallScalar(core.JSON{})
	srv.InstallScalar(core.Void{})

	dagql.Fields[*core.RemoteGitMirror]{}.Install(srv)
	dagql.Fields[*core.ClientFilesyncMirror]{}.Install(srv)

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

type remoteGitMirrorArgs struct {
	RemoteURL string `name:"remoteURL"`
}

type clientFilesyncMirrorArgs struct {
	StableClientID string `name:"stableClientID"`
	Drive          string `default:""`
}

func (s *querySchema) pipeline(ctx context.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
	return parent.WithPipeline(args.Name, args.Description), nil
}

func (s *querySchema) version(_ context.Context, _ *core.Query, args struct{}) (string, error) {
	return engine.Version, nil
}

func (s *querySchema) remoteGitMirror(ctx context.Context, parent dagql.ObjectResult[*core.Query], args remoteGitMirrorArgs) (dagql.Result[*core.RemoteGitMirror], error) {
	mirror := core.NewRemoteGitMirror(args.RemoteURL)
	if err := mirror.EnsureCreated(ctx, parent.Self()); err != nil {
		return dagql.Result[*core.RemoteGitMirror]{}, err
	}
	return dagql.NewResultForCurrentCall(ctx, mirror)
}

func (s *querySchema) clientFilesyncMirror(ctx context.Context, parent dagql.ObjectResult[*core.Query], args clientFilesyncMirrorArgs) (dagql.Result[*core.ClientFilesyncMirror], error) {
	if args.StableClientID == "" {
		return dagql.Result[*core.ClientFilesyncMirror]{}, fmt.Errorf("stable client id is empty")
	}
	mirror := &core.ClientFilesyncMirror{
		StableClientID: args.StableClientID,
		Drive:          args.Drive,
	}
	if err := mirror.EnsureCreated(ctx, parent.Self()); err != nil {
		return dagql.Result[*core.ClientFilesyncMirror]{}, err
	}
	return dagql.NewResultForCurrentCall(ctx, mirror)
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
}

func (s *querySchema) schemaJSONFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args schemaJSONArgs,
) (inst dagql.ObjectResult[*core.File], rerr error) {
	const schemaJSONFilename = "schema.json"
	const perm fs.FileMode = 0644

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	moduleSchemaJSON, err := getSchemaJSON(args.HiddenTypes, dag.View, dag)
	if err != nil {
		return inst, err
	}

	var dirInst dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &dirInst, dagql.Selector{Field: "directory"}); err != nil {
		return inst, err
	}

	file := &core.File{
		Platform: parent.Self().Platform(),
		File:     new(core.LazyAccessor[string, *core.File]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]),
	}

	if err := file.WithContents(ctx, dirInst, schemaJSONFilename, moduleSchemaJSON, perm, nil); err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentCall(ctx, dag, file)
}

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
	core.ModuleSourceExperimentalFeatures.Install(srv)
	core.FunctionCachePolicyEnum.Install(srv)

	dagql.MustInputSpec(PipelineLabel{}).Install(srv)
	dagql.MustInputSpec(core.PortForward{}).Install(srv)
	dagql.MustInputSpec(core.BuildArg{}).Install(srv)

	dagql.Fields[core.EnvVariable]{}.Install(srv)

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
	dagqlSchema := introspection.WrapSchema(s.srv.Schema())

	introspectionResponse := codegenintrospection.Response{
		SchemaVersion: string(s.srv.View),
		Schema:        &codegenintrospection.Schema{},
	}
	if queryName := dagqlSchema.QueryType().Name(); queryName != nil {
		introspectionResponse.Schema.QueryType.Name = *queryName
	}
	for _, dagqlType := range dagqlSchema.Types() {
		introspectionResponse.Schema.Types = append(introspectionResponse.Schema.Types, dagqlToCodegenType(dagqlType))
	}
	for _, dagqlDirective := range dagqlSchema.Directives() {
		introspectionResponse.Schema.Directives = append(introspectionResponse.Schema.Directives, dagqlToCodegenDirectiveDef(dagqlDirective))
	}

	for _, typed := range core.TypesHiddenFromModuleSDKs {
		introspectionResponse.Schema.ScrubType(typed.Type().Name())
		introspectionResponse.Schema.ScrubType(dagql.IDTypeNameFor(typed))
	}
	for _, rawType := range args.HiddenTypes {
		introspectionResponse.Schema.ScrubType(rawType)
		introspectionResponse.Schema.ScrubType(dagql.IDTypeNameForRawType(rawType))
	}

	moduleSchemaJSON, err := json.Marshal(introspectionResponse)
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

func dagqlToCodegenType(dagqlType *introspection.Type) *codegenintrospection.Type {
	t := &codegenintrospection.Type{}

	t.Kind = codegenintrospection.TypeKind(dagqlType.Kind())

	if name := dagqlType.Name(); name != nil {
		t.Name = *name
	}

	t.Description = dagqlType.Description()

	dagqlFields := dagqlType.Fields(true)
	t.Fields = make([]*codegenintrospection.Field, 0, len(dagqlFields))
	for _, dagqlField := range dagqlFields {
		t.Fields = append(t.Fields, dagqlToCodegenField(dagqlField))
	}

	dagqlInputFields := dagqlType.InputFields(true)
	t.InputFields = make([]codegenintrospection.InputValue, 0, len(dagqlInputFields))
	for _, dagqlInputValue := range dagqlInputFields {
		t.InputFields = append(t.InputFields, dagqlToCodegenInputValue(dagqlInputValue))
	}

	dagqlEnumValues := dagqlType.EnumValues(true)
	t.EnumValues = make([]codegenintrospection.EnumValue, 0, len(dagqlEnumValues))
	for _, dagqlEnumValue := range dagqlEnumValues {
		t.EnumValues = append(t.EnumValues, dagqlToCodegenEnumValue(dagqlEnumValue))
	}

	dagqlInterfaces := dagqlType.Interfaces()
	t.Interfaces = make([]*codegenintrospection.Type, 0, len(dagqlInterfaces))
	for _, dagqlIface := range dagqlInterfaces {
		t.Interfaces = append(t.Interfaces, dagqlToCodegenType(dagqlIface))
	}

	dagqlDirectives := dagqlType.Directives()
	t.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		t.Directives = append(t.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return t
}

func dagqlToCodegenDirective(dagqlDirective *introspection.DirectiveApplication) *codegenintrospection.Directive {
	d := &codegenintrospection.Directive{
		Name: dagqlDirective.Name,
	}
	d.Args = make([]*codegenintrospection.DirectiveArg, 0, len(dagqlDirective.Args))
	for _, arg := range dagqlDirective.Args {
		d.Args = append(d.Args, dagqlToCodegenDirectiveArg(arg))
	}
	return d
}

func dagqlToCodegenDirectiveArg(dagqlArg *introspection.DirectiveApplicationArg) *codegenintrospection.DirectiveArg {
	val := dagqlArg.Value.String()
	arg := &codegenintrospection.DirectiveArg{
		Name:  dagqlArg.Name,
		Value: &val,
	}
	return arg
}

func dagqlToCodegenDirectiveDef(dagqlDirective *introspection.Directive) *codegenintrospection.DirectiveDef {
	d := &codegenintrospection.DirectiveDef{
		Name:        dagqlDirective.Name,
		Description: dagqlDirective.Description(),
		Locations:   dagqlDirective.Locations,
	}

	dagqlArgs := dagqlDirective.Args(true)
	d.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		d.Args = append(d.Args, dagqlToCodegenInputValue(dagqlInputValue))
	}
	return d
}

func dagqlToCodegenField(dagqlField *introspection.Field) *codegenintrospection.Field {
	f := &codegenintrospection.Field{}

	f.Name = dagqlField.Name

	f.Description = dagqlField.Description()

	f.TypeRef = dagqlToCodegenTypeRef(dagqlField.Type_)

	dagqlArgs := dagqlField.Args(true)
	f.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		f.Args = append(f.Args, dagqlToCodegenInputValue(dagqlInputValue))
	}

	f.IsDeprecated = dagqlField.IsDeprecated()
	f.DeprecationReason = dagqlField.DeprecationReason()

	dagqlDirectives := dagqlField.Directives()
	f.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		f.Directives = append(f.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return f
}

func dagqlToCodegenInputValue(dagqlInputValue *introspection.InputValue) codegenintrospection.InputValue {
	v := codegenintrospection.InputValue{}

	v.Name = dagqlInputValue.Name

	v.Description = dagqlInputValue.Description()

	v.DefaultValue = dagqlInputValue.DefaultValue

	v.TypeRef = dagqlToCodegenTypeRef(dagqlInputValue.Type_)

	v.DeprecationReason = dagqlInputValue.DeprecationReason()
	v.IsDeprecated = dagqlInputValue.IsDeprecated()

	dagqlDirectives := dagqlInputValue.Directives()
	v.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		v.Directives = append(v.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return v
}

func dagqlToCodegenEnumValue(dagqlInputValue *introspection.EnumValue) codegenintrospection.EnumValue {
	v := codegenintrospection.EnumValue{}

	v.Name = dagqlInputValue.Name

	v.Description = dagqlInputValue.Description()

	v.IsDeprecated = dagqlInputValue.IsDeprecated()
	v.DeprecationReason = dagqlInputValue.DeprecationReason()

	dagqlDirectives := dagqlInputValue.Directives()
	v.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		v.Directives = append(v.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return v
}

func dagqlToCodegenTypeRef(dagqlType *introspection.Type) *codegenintrospection.TypeRef {
	if dagqlType == nil {
		return nil
	}
	typeRef := &codegenintrospection.TypeRef{
		Kind: codegenintrospection.TypeKind(dagqlType.Kind()),
	}
	if name := dagqlType.Name(); name != nil {
		typeRef.Name = *name
	}
	if ofType := dagqlType.OfType(); ofType != nil && (dagqlType.Kind() == "NON_NULL" || dagqlType.Kind() == "LIST") {
		typeRef.OfType = dagqlToCodegenTypeRef(ofType)
	}
	return typeRef
}

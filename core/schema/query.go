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
		codeGenType, err := dagqlToCodegenType(dagqlType)
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
		dd, err := dagqlToCodegenDirectiveDef(dagqlDirective)
		if err != nil {
			return nil, err
		}
		introspectionResponse.Schema.Directives = append(introspectionResponse.Schema.Directives, dd)
	}

	for _, typed := range core.TypesHiddenFromModuleSDKs {
		introspectionResponse.Schema.ScrubType(typed.Type().Name())
		introspectionResponse.Schema.ScrubType(dagql.IDTypeNameFor(typed))
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

func dagqlToCodegenType(dagqlType *introspection.Type) (*codegenintrospection.Type, error) {
	t := &codegenintrospection.Type{}

	t.Kind = codegenintrospection.TypeKind(dagqlType.Kind())

	if name := dagqlType.Name(); name != nil {
		t.Name = *name
	}

	t.Description = dagqlType.Description()

	dagqlFields, err := dagqlType.Fields(true)
	if err != nil {
		return nil, err
	}
	t.Fields = make([]*codegenintrospection.Field, 0, len(dagqlFields))
	for _, dagqlField := range dagqlFields {
		codeGenType, err := dagqlToCodegenField(dagqlField)
		if err != nil {
			return nil, err
		}
		t.Fields = append(t.Fields, codeGenType)
	}

	dagqlInputFields, err := dagqlType.InputFields(true)
	if err != nil {
		return nil, err
	}
	t.InputFields = make([]codegenintrospection.InputValue, 0, len(dagqlInputFields))
	for _, dagqlInputValue := range dagqlInputFields {
		inputField, err := dagqlToCodegenInputValue(dagqlInputValue)
		if err != nil {
			return nil, err
		}
		t.InputFields = append(t.InputFields, inputField)
	}

	dagqlEnumValues := dagqlType.EnumValues(true)
	t.EnumValues = make([]codegenintrospection.EnumValue, 0, len(dagqlEnumValues))
	for _, dagqlEnumValue := range dagqlEnumValues {
		t.EnumValues = append(t.EnumValues, dagqlToCodegenEnumValue(dagqlEnumValue))
	}

	dagqlInterfaces := dagqlType.Interfaces()
	t.Interfaces = make([]*codegenintrospection.Type, 0, len(dagqlInterfaces))
	for _, dagqlIface := range dagqlInterfaces {
		codeGenType, err := dagqlToCodegenType(dagqlIface)
		if err != nil {
			return nil, err
		}
		t.Interfaces = append(t.Interfaces, codeGenType)
	}

	dagqlDirectives := dagqlType.Directives()
	t.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		t.Directives = append(t.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return t, nil
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

func dagqlToCodegenDirectiveDef(dagqlDirective *introspection.Directive) (*codegenintrospection.DirectiveDef, error) {
	d := &codegenintrospection.DirectiveDef{
		Name:        dagqlDirective.Name,
		Description: dagqlDirective.Description(),
		Locations:   dagqlDirective.Locations,
	}

	dagqlArgs := dagqlDirective.Args(true)
	d.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		arg, err := dagqlToCodegenInputValue(dagqlInputValue)
		if err != nil {
			return nil, err
		}
		d.Args = append(d.Args, arg)
	}
	return d, nil
}

func dagqlToCodegenField(dagqlField *introspection.Field) (*codegenintrospection.Field, error) {
	f := &codegenintrospection.Field{}

	f.Name = dagqlField.Name

	f.Description = dagqlField.Description()

	var err error
	f.TypeRef, err = dagqlToCodegenTypeRef(dagqlField.Type_)
	if err != nil {
		return nil, err
	}

	dagqlArgs := dagqlField.Args(true)
	f.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		arg, err := dagqlToCodegenInputValue(dagqlInputValue)
		if err != nil {
			return nil, err
		}
		f.Args = append(f.Args, arg)
	}

	f.IsDeprecated = dagqlField.IsDeprecated()
	f.DeprecationReason = dagqlField.DeprecationReason()

	dagqlDirectives := dagqlField.Directives()
	f.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		f.Directives = append(f.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return f, nil
}

func dagqlToCodegenInputValue(dagqlInputValue *introspection.InputValue) (codegenintrospection.InputValue, error) {
	v := codegenintrospection.InputValue{}

	v.Name = dagqlInputValue.Name

	v.Description = dagqlInputValue.Description()

	v.DefaultValue = dagqlInputValue.DefaultValue

	var err error
	v.TypeRef, err = dagqlToCodegenTypeRef(dagqlInputValue.Type_)
	if err != nil {
		return codegenintrospection.InputValue{}, err
	}

	v.DeprecationReason = dagqlInputValue.DeprecationReason()
	v.IsDeprecated = dagqlInputValue.IsDeprecated()

	dagqlDirectives := dagqlInputValue.Directives()
	v.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		v.Directives = append(v.Directives, dagqlToCodegenDirective(dagqlDirective))
	}

	return v, nil
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

func dagqlToCodegenTypeRef(dagqlType *introspection.Type) (*codegenintrospection.TypeRef, error) {
	if dagqlType == nil {
		return nil, nil
	}
	typeRef := &codegenintrospection.TypeRef{
		Kind: codegenintrospection.TypeKind(dagqlType.Kind()),
	}
	if name := dagqlType.Name(); name != nil {
		typeRef.Name = *name
	}
	ofType, err := dagqlType.OfType()
	if err != nil {
		return nil, err
	}
	if ofType != nil && (dagqlType.Kind() == "NON_NULL" || dagqlType.Kind() == "LIST") {
		type_, err := dagqlToCodegenTypeRef(ofType)
		if err != nil {
			return nil, err
		}
		typeRef.OfType = type_
	}
	return typeRef, nil
}

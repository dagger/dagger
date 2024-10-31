package introspection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func Install[T dagql.Typed](srv *dagql.Server) {
	dagql.Fields[T]{
		dagql.Func("__schema", func(ctx context.Context, self T, args struct{}) (*Schema, error) {
			return WrapSchema(srv.Schema()), nil
		}).Impure("A schema can be modified at runtime."),

		// custom dagger field
		dagql.Func("__schemaVersion", func(ctx context.Context, self T, args struct{}) (string, error) {
			return srv.View, nil
		}).View(dagql.AllView{}),

		dagql.Func("__type", func(ctx context.Context, self T, args struct {
			Name string
		}) (*Type, error) {
			def, ok := srv.Schema().Types[args.Name]
			if !ok {
				return nil, fmt.Errorf("unknown type: %q", args.Name)
			}
			return WrapTypeFromDef(srv.Schema(), def), nil
		}).Impure("A type can be modified at runtime."),
	}.Install(srv)

	TypeKinds.Install(srv)

	DirectiveLocations.Install(srv)

	for _, class := range []dagql.ObjectType{
		dagql.NewClass[*Directive](dagql.ClassOpts[*Directive]{
			NoIDs: true,
		}),
		dagql.NewClass[*DirectiveApplication](dagql.ClassOpts[*DirectiveApplication]{
			NoIDs: true,
		}),
		dagql.NewClass[*DirectiveApplicationArg](dagql.ClassOpts[*DirectiveApplicationArg]{
			NoIDs: true,
		}),
		dagql.NewClass[*EnumValue](dagql.ClassOpts[*EnumValue]{
			NoIDs: true,
		}),
		dagql.NewClass[*Field](dagql.ClassOpts[*Field]{
			NoIDs: true,
		}),
		dagql.NewClass[*InputValue](dagql.ClassOpts[*InputValue]{
			NoIDs: true,
		}),
		dagql.NewClass[*Schema](dagql.ClassOpts[*Schema]{
			NoIDs: true,
		}),
		dagql.NewClass[*Type](dagql.ClassOpts[*Type]{
			NoIDs: true,
		}),
	} {
		srv.InstallObject(class)
	}

	dagql.Fields[*Schema]{
		dagql.Func("description", func(ctx context.Context, self *Schema, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("types", func(ctx context.Context, self *Schema, args struct{}) (dagql.Array[*Type], error) {
			return self.Types(), nil
		}),
		dagql.Func("queryType", func(ctx context.Context, self *Schema, args struct{}) (*Type, error) {
			return self.QueryType(), nil
		}),
		dagql.Func("mutationType", func(ctx context.Context, self *Schema, args struct{}) (dagql.Nullable[*Type], error) {
			if self.MutationType() == nil {
				return dagql.Null[*Type](), nil
			}
			return dagql.NonNull(self.MutationType()), nil
		}),
		dagql.Func("subscriptionType", func(ctx context.Context, self *Schema, args struct{}) (dagql.Nullable[*Type], error) {
			if self.SubscriptionType() == nil {
				return dagql.Null[*Type](), nil
			}
			return dagql.NonNull(self.SubscriptionType()), nil
		}),
		dagql.Func("directives", func(ctx context.Context, self *Schema, args struct{}) (dagql.Array[*Directive], error) {
			return self.Directives(), nil
		}),
	}.Install(srv)

	dagql.Fields[*Type]{
		dagql.Func("name", func(ctx context.Context, self *Type, args struct{}) (dagql.Nullable[dagql.String], error) {
			if self.Name() == nil {
				return dagql.Null[dagql.String](), nil
			} else {
				return dagql.NonNull(dagql.NewString(*self.Name())), nil
			}
		}),
		dagql.NodeFunc("kind", func(ctx context.Context, self dagql.Instance[*Type], args struct{}) (TypeKind, error) {
			return TypeKinds.Lookup(self.Self.Kind())
		}),
		dagql.Func("description", func(ctx context.Context, self *Type, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("fields", func(ctx context.Context, self *Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[*Field], error) {
			return self.Fields(args.IncludeDeprecated.Bool()), nil
		}),
		dagql.Func("inputFields", func(ctx context.Context, self *Type, _ struct{}) (dagql.Array[*InputValue], error) {
			return self.InputFields(), nil
		}),
		dagql.Func("interfaces", func(ctx context.Context, self *Type, args struct{}) (dagql.Array[*Type], error) {
			return self.Interfaces(), nil
		}),
		dagql.Func("possibleTypes", func(ctx context.Context, self *Type, args struct{}) (dagql.Array[*Type], error) {
			return self.PossibleTypes(), nil
		}),
		dagql.Func("enumValues", func(ctx context.Context, self *Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[*EnumValue], error) {
			return self.EnumValues(args.IncludeDeprecated.Bool()), nil
		}),
		dagql.NodeFunc("ofType", func(ctx context.Context, self dagql.Instance[*Type], args struct{}) (dagql.Nullable[*Type], error) {
			switch self.Self.Kind() {
			case "LIST", "NON_NULL":
				return dagql.NonNull(self.Self.OfType()), nil
			default:
				return dagql.Null[*Type](), nil
			}
		}),
		dagql.Func("specifiedByURL", func(ctx context.Context, self *Type, args struct{}) (*string, error) {
			return self.SpecifiedByURL(), nil
		}),

		// custom dagger field
		dagql.Func("directives", func(ctx context.Context, self *Type, args struct{}) (dagql.Array[*DirectiveApplication], error) {
			return self.Directives(), nil
		}),
	}.Install(srv)

	dagql.Fields[*Directive]{
		dagql.Func("name", func(ctx context.Context, self *Directive, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self *Directive, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("locations", func(ctx context.Context, self *Directive, args struct{}) (dagql.Array[DirectiveLocation], error) {
			var locations []DirectiveLocation
			for _, loc := range self.Locations {
				enum, err := DirectiveLocations.Lookup(loc)
				if err != nil {
					return nil, err
				}
				locations = append(locations, enum)
			}
			return locations, nil
		}),
		dagql.Func("args", func(ctx context.Context, self *Directive, _ struct{}) (dagql.Array[*InputValue], error) {
			return self.Args, nil
		}),
	}.Install(srv)

	dagql.Fields[*Field]{
		dagql.Func("name", func(ctx context.Context, self *Field, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self *Field, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("args", func(ctx context.Context, self *Field, _ struct{}) (dagql.Array[*InputValue], error) {
			return self.Args, nil
		}),
		dagql.Func("type", func(ctx context.Context, self *Field, args struct{}) (*Type, error) {
			return self.Type_, nil
		}),
		dagql.Func("isDeprecated", func(ctx context.Context, self *Field, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		dagql.Func("deprecationReason", func(ctx context.Context, self *Field, args struct{}) (*string, error) {
			return self.DeprecationReason(), nil
		}),

		// custom dagger field
		dagql.Func("directives", func(ctx context.Context, self *Field, args struct{}) (dagql.Array[*DirectiveApplication], error) {
			return self.Directives(), nil
		}),
	}.Install(srv)

	dagql.Fields[*InputValue]{
		dagql.Func("name", func(ctx context.Context, self *InputValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self *InputValue, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("type", func(ctx context.Context, self *InputValue, args struct{}) (*Type, error) {
			return self.Type_, nil
		}),
		dagql.Func("defaultValue", func(ctx context.Context, self *InputValue, args struct{}) (dagql.Nullable[dagql.String], error) {
			if self.DefaultValue == nil {
				return dagql.Null[dagql.String](), nil
			} else {
				return dagql.NonNull(dagql.NewString(*self.DefaultValue)), nil
			}
		}),
		dagql.Func("isDeprecated", func(ctx context.Context, self *InputValue, args struct{}) (bool, error) {
			return self.IsDeprecated(), nil
		}),
		dagql.Func("deprecationReason", func(ctx context.Context, self *InputValue, args struct{}) (*string, error) {
			return self.DeprecationReason(), nil
		}),

		// custom dagger field
		dagql.Func("directives", func(ctx context.Context, self *InputValue, args struct{}) (dagql.Array[*DirectiveApplication], error) {
			return self.Directives(), nil
		}),
	}.Install(srv)

	dagql.Fields[*EnumValue]{
		dagql.Func("name", func(ctx context.Context, self *EnumValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self *EnumValue, args struct{}) (string, error) {
			return self.Description(), nil
		}),
		dagql.Func("isDeprecated", func(ctx context.Context, self *EnumValue, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		dagql.Func("deprecationReason", func(ctx context.Context, self *EnumValue, args struct{}) (*string, error) {
			return self.DeprecationReason(), nil
		}),

		// custom dagger field
		dagql.Func("directives", func(ctx context.Context, self *EnumValue, args struct{}) (dagql.Array[*DirectiveApplication], error) {
			return self.Directives(), nil
		}),
	}.Install(srv)

	// custom dagger type
	dagql.Fields[*DirectiveApplication]{
		dagql.Func("name", func(ctx context.Context, self *DirectiveApplication, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("args", func(ctx context.Context, self *DirectiveApplication, _ struct{}) (dagql.Array[*DirectiveApplicationArg], error) {
			return self.Args, nil
		}),
	}.Install(srv)

	// custom dagger type
	dagql.Fields[*DirectiveApplicationArg]{
		dagql.Func("name", func(ctx context.Context, self *DirectiveApplicationArg, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("value", func(ctx context.Context, self *DirectiveApplicationArg, args struct{}) (dagql.Nullable[dagql.String], error) {
			if self.Value == nil {
				return dagql.Null[dagql.String](), nil
			} else {
				return dagql.NonNull(dagql.NewString(self.Value.Raw)), nil
			}
		}),
	}.Install(srv)
}

type Schema struct {
	schema *ast.Schema
}

func (s *Schema) Description() string {
	return s.schema.Description
}

func (s *Schema) Types() []*Type {
	typeIndex := map[string]Type{}
	typeNames := make([]string, 0, len(s.schema.Types))
	for _, typ := range s.schema.Types {
		typeNames = append(typeNames, typ.Name)
		typeIndex[typ.Name] = *WrapTypeFromDef(s.schema, typ)
	}
	sort.Strings(typeNames)

	types := make([]*Type, len(typeNames))
	for i, t := range typeNames {
		cp := typeIndex[t]
		types[i] = &cp
	}
	return types
}

func (s *Schema) QueryType() *Type {
	return WrapTypeFromDef(s.schema, s.schema.Query)
}

func (s *Schema) MutationType() *Type {
	return WrapTypeFromDef(s.schema, s.schema.Mutation)
}

func (s *Schema) SubscriptionType() *Type {
	return WrapTypeFromDef(s.schema, s.schema.Subscription)
}

func (s *Schema) Directives() []*Directive {
	dIndex := map[string]Directive{}
	dNames := make([]string, 0, len(s.schema.Directives))

	for _, d := range s.schema.Directives {
		dNames = append(dNames, d.Name)
		dIndex[d.Name] = s.directiveFromDef(d)
	}
	sort.Strings(dNames)

	res := make([]*Directive, len(dNames))
	for i, d := range dNames {
		cp := dIndex[d]
		res[i] = &cp
	}

	return res
}

func (s *Schema) directiveFromDef(d *ast.DirectiveDefinition) Directive {
	locs := make([]string, len(d.Locations))
	for i, loc := range d.Locations {
		locs[i] = string(loc)
	}

	args := make([]*InputValue, len(d.Arguments))
	for i, arg := range d.Arguments {
		args[i] = &InputValue{
			Name:         arg.Name,
			description:  arg.Description,
			DefaultValue: defaultValue(arg.DefaultValue),
			Type_:        WrapTypeFromType(s.schema, arg.Type),
			deprecation:  arg.Directives.ForName("deprecated"),
		}
	}

	return Directive{
		Name:         d.Name,
		description:  d.Description,
		Locations:    locs,
		Args:         args,
		IsRepeatable: d.IsRepeatable,
	}
}

var _ dagql.Typed = &Schema{}

func (s *Schema) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Schema",
		NonNull:   true,
	}
}

func (s *Schema) TypeDescription() string {
	return "A GraphQL schema definition."
}

var _ dagql.Typed = &Type{}

func (t *Type) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Type",
		NonNull:   true,
	}
}

func (t *Type) TypeDescription() string {
	return "A GraphQL schema type."
}

var _ dagql.Typed = &Directive{}

func (d *Directive) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Directive",
		NonNull:   true,
	}
}

func (d *Directive) TypeDescription() string {
	return "A GraphQL schema directive."
}

func (d *Directive) Description() string {
	return d.description
}

var _ dagql.Typed = &InputValue{}

func (i *InputValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__InputValue",
		NonNull:   true,
	}
}

func (i *InputValue) TypeDescription() string {
	return "A GraphQL schema input field or argument."
}

var _ dagql.Typed = &Field{}

func (f *Field) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Field",
		NonNull:   true,
	}
}

func (f *Field) TypeDescription() string {
	return "A GraphQL object or input field."
}

var _ dagql.Typed = &EnumValue{}

func (e *EnumValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__EnumValue",
		NonNull:   true,
	}
}

func (e *EnumValue) TypeDescription() string {
	return "A possible value of a GraphQL enum."
}

type TypeKind string

var TypeKinds = dagql.NewEnum[TypeKind](
	"SCALAR",
	"OBJECT",
	"INTERFACE",
	"UNION",
	"ENUM",
	"INPUT_OBJECT",
	"LIST",
	"NON_NULL",
)

func (k TypeKind) Decoder() dagql.InputDecoder {
	return TypeKinds
}

func (k TypeKind) ToLiteral() call.Literal {
	return TypeKinds.Literal(k)
}

var _ dagql.Typed = TypeKind("")

func (k TypeKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__TypeKind",
		NonNull:   true,
	}
}

func (k TypeKind) TypeDescription() string {
	return "The kind of a GraphQL type."
}

type DirectiveLocation string

var DirectiveLocations = dagql.NewEnum[DirectiveLocation](
	"QUERY",
	"MUTATION",
	"SUBSCRIPTION",
	"FIELD",
	"FRAGMENT_DEFINITION",
	"FRAGMENT_SPREAD",
	"INLINE_FRAGMENT",
	"VARIABLE_DEFINITION",
	"SCHEMA",
	"SCALAR",
	"OBJECT",
	"FIELD_DEFINITION",
	"ARGUMENT_DEFINITION",
	"INTERFACE",
	"UNION",
	"ENUM",
	"ENUM_VALUE",
	"INPUT_OBJECT",
	"INPUT_FIELD_DEFINITION",
)

func (l DirectiveLocation) Decoder() dagql.InputDecoder {
	return DirectiveLocations
}

func (l DirectiveLocation) ToLiteral() call.Literal {
	return DirectiveLocations.Literal(l)
}

var _ dagql.Typed = DirectiveLocation("")

func (l DirectiveLocation) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__DirectiveLocation",
		NonNull:   true,
	}
}

func (l DirectiveLocation) TypeDescription() string {
	return "A location that a directive may be applied."
}

type Type struct {
	schema *ast.Schema
	def    *ast.Definition
	typ    *ast.Type
}

func WrapTypeFromDef(s *ast.Schema, def *ast.Definition) *Type {
	if def == nil {
		return nil
	}
	return &Type{schema: s, def: def}
}

func WrapTypeFromType(s *ast.Schema, typ *ast.Type) *Type {
	if typ == nil {
		return nil
	}

	if !typ.NonNull && typ.NamedType != "" {
		def, ok := s.Types[typ.NamedType]
		if !ok {
			panic("unknown type: " + typ.NamedType)
		}
		return &Type{schema: s, def: def}
	}
	return &Type{schema: s, typ: typ}
}

func (t *Type) Kind() string {
	if t.typ != nil {
		if t.typ.NonNull {
			return "NON_NULL"
		}

		if t.typ.Elem != nil {
			return "LIST"
		}
	} else {
		return string(t.def.Kind)
	}

	panic("UNKNOWN")
}

func (t *Type) Name() *string {
	if t.def == nil {
		return nil
	}
	return &t.def.Name
}

func (t *Type) Description() string {
	if t.def == nil {
		return ""
	}
	return t.def.Description
}

func (t *Type) Fields(includeDeprecated bool) []*Field {
	if t.def == nil || (t.def.Kind != ast.Object && t.def.Kind != ast.Interface) {
		return []*Field{}
	}
	fields := []*Field{}
	for _, f := range t.def.Fields {
		if strings.HasPrefix(f.Name, "_") {
			continue
		}

		if !includeDeprecated && f.Directives.ForName("deprecated") != nil {
			continue
		}

		var args []*InputValue
		for _, arg := range f.Arguments {
			args = append(args, &InputValue{
				Type_:        WrapTypeFromType(t.schema, arg.Type),
				Name:         arg.Name,
				description:  arg.Description,
				DefaultValue: defaultValue(arg.DefaultValue),
				deprecation:  arg.Directives.ForName("deprecated"),
				directives:   arg.Directives,
			})
		}

		fields = append(fields, &Field{
			Name:        f.Name,
			description: f.Description,
			Args:        args,
			Type_:       WrapTypeFromType(t.schema, f.Type),
			directives:  f.Directives,
			deprecation: f.Directives.ForName("deprecated"),
		})
	}
	return fields
}

func (t *Type) InputFields() []*InputValue {
	if t.def == nil || t.def.Kind != ast.InputObject {
		return []*InputValue{}
	}

	res := []*InputValue{}
	for _, f := range t.def.Fields {
		res = append(res, &InputValue{
			Name:         f.Name,
			description:  f.Description,
			Type_:        WrapTypeFromType(t.schema, f.Type),
			DefaultValue: defaultValue(f.DefaultValue),
			directives:   f.Directives,
			deprecation:  f.Directives.ForName("deprecated"),
		})
	}
	return res
}

func (t *Type) Directives() []*DirectiveApplication {
	return directiveApplications(t.def.Directives)
}

func defaultValue(value *ast.Value) *string {
	if value == nil {
		return nil
	}
	val := value.String()
	return &val
}

func (t *Type) Interfaces() []*Type {
	if t.def == nil || t.def.Kind != ast.Object {
		return []*Type{}
	}

	res := []*Type{}
	for _, intf := range t.def.Interfaces {
		res = append(res, WrapTypeFromDef(t.schema, t.schema.Types[intf]))
	}

	return res
}

func (t *Type) PossibleTypes() []*Type {
	if t.def == nil || (t.def.Kind != ast.Interface && t.def.Kind != ast.Union) {
		return []*Type{}
	}

	res := []*Type{}
	for _, pt := range t.schema.GetPossibleTypes(t.def) {
		res = append(res, WrapTypeFromDef(t.schema, pt))
	}
	return res
}

func (t *Type) EnumValues(includeDeprecated bool) []*EnumValue {
	if t.def == nil || t.def.Kind != ast.Enum {
		return []*EnumValue{}
	}

	res := []*EnumValue{}
	for _, val := range t.def.EnumValues {
		if !includeDeprecated && val.Directives.ForName("deprecated") != nil {
			continue
		}

		res = append(res, &EnumValue{
			Name:        val.Name,
			description: val.Description,
			directives:  val.Directives,
			deprecation: val.Directives.ForName("deprecated"),
		})
	}
	return res
}

func (t *Type) OfType() *Type {
	if t.typ == nil {
		return nil
	}
	if t.typ.NonNull {
		// fake non null nodes
		cpy := *t.typ
		cpy.NonNull = false

		return WrapTypeFromType(t.schema, &cpy)
	}
	if t.typ.Elem != nil {
		return WrapTypeFromType(t.schema, t.typ.Elem)
	}
	return nil
}

func (t *Type) SpecifiedByURL() *string {
	directive := t.def.Directives.ForName("specifiedBy")
	if t.def.Kind != ast.Scalar || directive == nil {
		return nil
	}
	// def: directive @specifiedBy(url: String!) on SCALAR
	// the argument "url" is required.
	url := directive.Arguments.ForName("url")
	return &url.Value.Raw
}

type (
	Directive struct {
		Name         string
		description  string
		Locations    []string
		Args         []*InputValue
		IsRepeatable bool
	}

	DirectiveApplication struct {
		Name string
		Args []*DirectiveApplicationArg
	}
	DirectiveApplicationArg struct {
		Name  string
		Value *ast.Value
	}

	EnumValue struct {
		Name        string
		description string
		directives  []*ast.Directive
		deprecation *ast.Directive
	}

	Field struct {
		Name        string
		description string
		Type_       *Type
		Args        []*InputValue
		directives  []*ast.Directive
		deprecation *ast.Directive
	}

	InputValue struct {
		Name         string
		description  string
		DefaultValue *string
		Type_        *Type
		directives   []*ast.Directive
		deprecation  *ast.Directive
	}
)

func WrapSchema(schema *ast.Schema) *Schema {
	return &Schema{schema: schema}
}

func (e *EnumValue) Description() string {
	return e.description
}

func (e *EnumValue) IsDeprecated() bool {
	return e.deprecation != nil
}

func (e *EnumValue) DeprecationReason() *string {
	if e.deprecation == nil {
		return nil
	}

	reason := e.deprecation.Arguments.ForName("reason")
	if reason == nil {
		return nil
	}

	return &reason.Value.Raw
}

func (e *EnumValue) Directives() []*DirectiveApplication {
	return directiveApplications(e.directives)
}

func (f *Field) Description() string {
	return f.description
}

func (f *Field) IsDeprecated() bool {
	return f.deprecation != nil
}

func (f *Field) DeprecationReason() *string {
	if f.deprecation == nil || !f.IsDeprecated() {
		return nil
	}

	reason := f.deprecation.Arguments.ForName("reason")

	if reason == nil {
		defaultReason := "No longer supported"
		return &defaultReason
	}

	return &reason.Value.Raw
}

func (f *Field) Directives() []*DirectiveApplication {
	return directiveApplications(f.directives)
}

func (i *InputValue) Description() string {
	return i.description
}

func (i *InputValue) IsDeprecated() bool {
	return i.deprecation != nil
}

func (i *InputValue) DeprecationReason() *string {
	if i.deprecation == nil {
		return nil
	}

	reason := i.deprecation.Arguments.ForName("reason")
	if reason == nil {
		return nil
	}

	return &reason.Value.Raw
}

func (i *InputValue) Directives() []*DirectiveApplication {
	return directiveApplications(i.directives)
}

var _ dagql.Typed = &DirectiveApplication{}

func (d *DirectiveApplication) Type() *ast.Type {
	return &ast.Type{
		// Some clients don't like custom introspection types like this :(
		// NamedType: "__DirectiveApplication",
		NamedType: "_DirectiveApplication",
		NonNull:   true,
	}
}

func (d *DirectiveApplication) TypeDescription() string {
	return "A GraphQL schema directive application."
}

var _ dagql.Typed = &DirectiveApplicationArg{}

func (d *DirectiveApplicationArg) Type() *ast.Type {
	return &ast.Type{
		// Some clients don't like custom introspection types like this :(
		// NamedType: "__DirectiveApplicationArg",
		NamedType: "_DirectiveApplicationArg",
		NonNull:   true,
	}
}

func (d *DirectiveApplicationArg) TypeDescription() string {
	return "A GraphQL schema directive application arg."
}

func directiveApplications(directives []*ast.Directive) []*DirectiveApplication {
	dIndex := map[string]DirectiveApplication{}
	dNames := make([]string, 0, len(directives))

	for _, d := range directives {
		dNames = append(dNames, d.Name)
		dIndex[d.Name] = directiveApplication(d)
	}
	sort.Strings(dNames)

	res := make([]*DirectiveApplication, len(dNames))
	for i, d := range dNames {
		cp := dIndex[d]
		res[i] = &cp
	}

	return res
}

func directiveApplication(d *ast.Directive) DirectiveApplication {
	args := make([]*DirectiveApplicationArg, len(d.Arguments))
	for i, arg := range d.Arguments {
		args[i] = &DirectiveApplicationArg{
			Name:  arg.Name,
			Value: arg.Value,
		}
	}

	return DirectiveApplication{
		Name: d.Name,
		Args: args,
	}
}

package core

import (
	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/dagql/introspection"
)

// DagqlToCodegenType converts a dagql introspection type to a codegen introspection type.
func DagqlToCodegenType(dagqlType *introspection.Type) (*codegenintrospection.Type, error) {
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
		codeGenType, err := DagqlToCodegenField(dagqlField)
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
		inputField, err := DagqlToCodegenInputValue(dagqlInputValue)
		if err != nil {
			return nil, err
		}
		t.InputFields = append(t.InputFields, inputField)
	}

	dagqlEnumValues := dagqlType.EnumValues(true)
	t.EnumValues = make([]codegenintrospection.EnumValue, 0, len(dagqlEnumValues))
	for _, dagqlEnumValue := range dagqlEnumValues {
		t.EnumValues = append(t.EnumValues, DagqlToCodegenEnumValue(dagqlEnumValue))
	}

	dagqlInterfaces := dagqlType.Interfaces()
	t.Interfaces = make([]*codegenintrospection.Type, 0, len(dagqlInterfaces))
	for _, dagqlIface := range dagqlInterfaces {
		codeGenType, err := DagqlToCodegenType(dagqlIface)
		if err != nil {
			return nil, err
		}
		t.Interfaces = append(t.Interfaces, codeGenType)
	}

	dagqlDirectives := dagqlType.Directives()
	t.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		t.Directives = append(t.Directives, DagqlToCodegenDirective(dagqlDirective))
	}

	return t, nil
}

// DagqlToCodegenDirective converts a dagql directive application to a codegen directive.
func DagqlToCodegenDirective(dagqlDirective *introspection.DirectiveApplication) *codegenintrospection.Directive {
	d := &codegenintrospection.Directive{
		Name: dagqlDirective.Name,
	}
	d.Args = make([]*codegenintrospection.DirectiveArg, 0, len(dagqlDirective.Args))
	for _, arg := range dagqlDirective.Args {
		d.Args = append(d.Args, DagqlToCodegenDirectiveArg(arg))
	}
	return d
}

// DagqlToCodegenDirectiveArg converts a dagql directive application arg to a codegen directive arg.
func DagqlToCodegenDirectiveArg(dagqlArg *introspection.DirectiveApplicationArg) *codegenintrospection.DirectiveArg {
	val := dagqlArg.Value.String()
	arg := &codegenintrospection.DirectiveArg{
		Name:  dagqlArg.Name,
		Value: &val,
	}
	return arg
}

// DagqlToCodegenDirectiveDef converts a dagql directive definition to a codegen directive def.
func DagqlToCodegenDirectiveDef(dagqlDirective *introspection.Directive) (*codegenintrospection.DirectiveDef, error) {
	d := &codegenintrospection.DirectiveDef{
		Name:        dagqlDirective.Name,
		Description: dagqlDirective.Description(),
		Locations:   dagqlDirective.Locations,
	}

	dagqlArgs := dagqlDirective.Args(true)
	d.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		arg, err := DagqlToCodegenInputValue(dagqlInputValue)
		if err != nil {
			return nil, err
		}
		d.Args = append(d.Args, arg)
	}
	return d, nil
}

// DagqlToCodegenField converts a dagql introspection field to a codegen field.
func DagqlToCodegenField(dagqlField *introspection.Field) (*codegenintrospection.Field, error) {
	f := &codegenintrospection.Field{}

	f.Name = dagqlField.Name

	f.Description = dagqlField.Description()

	var err error
	f.TypeRef, err = DagqlToCodegenTypeRef(dagqlField.Type_)
	if err != nil {
		return nil, err
	}

	dagqlArgs := dagqlField.Args(true)
	f.Args = make(codegenintrospection.InputValues, 0, len(dagqlArgs))
	for _, dagqlInputValue := range dagqlArgs {
		arg, err := DagqlToCodegenInputValue(dagqlInputValue)
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
		f.Directives = append(f.Directives, DagqlToCodegenDirective(dagqlDirective))
	}

	return f, nil
}

// DagqlToCodegenInputValue converts a dagql input value to a codegen input value.
func DagqlToCodegenInputValue(dagqlInputValue *introspection.InputValue) (codegenintrospection.InputValue, error) {
	v := codegenintrospection.InputValue{}

	v.Name = dagqlInputValue.Name

	v.Description = dagqlInputValue.Description()

	v.DefaultValue = dagqlInputValue.DefaultValue

	var err error
	v.TypeRef, err = DagqlToCodegenTypeRef(dagqlInputValue.Type_)
	if err != nil {
		return codegenintrospection.InputValue{}, err
	}

	v.DeprecationReason = dagqlInputValue.DeprecationReason()
	v.IsDeprecated = dagqlInputValue.IsDeprecated()

	dagqlDirectives := dagqlInputValue.Directives()
	v.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		v.Directives = append(v.Directives, DagqlToCodegenDirective(dagqlDirective))
	}

	return v, nil
}

// DagqlToCodegenEnumValue converts a dagql enum value to a codegen enum value.
func DagqlToCodegenEnumValue(dagqlInputValue *introspection.EnumValue) codegenintrospection.EnumValue {
	v := codegenintrospection.EnumValue{}

	v.Name = dagqlInputValue.Name

	v.Description = dagqlInputValue.Description()

	v.IsDeprecated = dagqlInputValue.IsDeprecated()
	v.DeprecationReason = dagqlInputValue.DeprecationReason()

	dagqlDirectives := dagqlInputValue.Directives()
	v.Directives = make(codegenintrospection.Directives, 0, len(dagqlDirectives))
	for _, dagqlDirective := range dagqlDirectives {
		v.Directives = append(v.Directives, DagqlToCodegenDirective(dagqlDirective))
	}

	return v
}

// DagqlToCodegenTypeRef converts a dagql introspection type to a codegen type ref.
func DagqlToCodegenTypeRef(dagqlType *introspection.Type) (*codegenintrospection.TypeRef, error) {
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
		type_, err := DagqlToCodegenTypeRef(ofType)
		if err != nil {
			return nil, err
		}
		typeRef.OfType = type_
	}
	return typeRef, nil
}

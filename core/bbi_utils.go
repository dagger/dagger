package core

import (
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

func argsToSelector(target *ast.Definition, fieldName string, fieldArgs map[string]any, schema *ast.Schema) (dagql.Selector, error) {
	// Find the field definition in the target type
	field := target.Fields.ForName(fieldName)
	if field == nil {
		return dagql.Selector{}, fmt.Errorf("field %q not found in type %q", fieldName, target.Name)
	}

	// If no args provided, return simple selector
	if len(fieldArgs) == 0 {
		return dagql.Selector{Field: fieldName}, nil
	}

	// Convert each argument according to its schema type
	var namedInputs []dagql.NamedInput
	for argName, argValue := range fieldArgs {
		// Look up argument definition
		argDef := field.Arguments.ForName(argName)
		if argDef == nil {
			return dagql.Selector{}, fmt.Errorf("argument %q not found in field %q", argName, fieldName)
		}

		// Convert the argument value to a typed Input
		input, err := valueToTypedInput(argValue, argDef.Type, schema)
		if err != nil {
			return dagql.Selector{}, fmt.Errorf("invalid value for argument %q: %w", argName, err)
		}

		namedInputs = append(namedInputs, dagql.NamedInput{
			Name:  argName,
			Value: input,
		})
	}

	return dagql.Selector{
		Field: fieldName,
		Args:  namedInputs,
	}, nil
}

// valueToTypedInput converts an untyped value to a typed Input according to the schema type
func valueToTypedInput(value any, typ *ast.Type, schema *ast.Schema) (dagql.Input, error) {
	if value == nil {
		if typ.NonNull {
			return nil, fmt.Errorf("null value not allowed for non-null type %s", typ)
		}
		return nil, nil
	}

	// Handle list types
	if typ.Elem != nil {
		return listToTypedInput(value, typ.Elem, schema)
	}

	// Look up named type
	namedType := schema.Types[typ.NamedType]
	if namedType == nil {
		return nil, fmt.Errorf("type %q not found in schema", typ.NamedType)
	}

	switch namedType.Kind {
	case ast.Scalar:
		return scalarToTypedInput(value, typ.NamedType)

	case ast.InputObject:
		return inputObjectToTypedInput(value, namedType, schema)

	case ast.Enum:
		return enumToTypedInput(value, namedType)

	default:
		return nil, fmt.Errorf("unsupported type kind: %v", namedType.Kind)
	}
}

func listToTypedInput(value any, elemType *ast.Type, schema *ast.Schema) (dagql.Input, error) {
	slice, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", value)
	}

	var inputs []dagql.Input
	for i, elem := range slice {
		input, err := valueToTypedInput(elem, elemType, schema)
		if err != nil {
			return nil, fmt.Errorf("invalid array element at index %d: %w", i, err)
		}
		inputs = append(inputs, input)
	}

	// Specify Input as the concrete type for ArrayInput
	return dagql.ArrayInput[dagql.Input](inputs), nil
}

func scalarToTypedInput(value any, typeName string) (dagql.Input, error) {
	switch typeName {
	case "String":
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", value)
		}
		return dagql.String(str), nil

	case "Int":
		// JSON numbers come as float64
		num, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("expected number, got %T", value)
		}
		return dagql.Int(int(num)), nil

	case "Float":
		num, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("expected number, got %T", value)
		}
		return dagql.Float(num), nil

	case "Boolean":
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected boolean, got %T", value)
		}
		return dagql.Boolean(b), nil

	default:
		return nil, fmt.Errorf("unsupported scalar type: %s", typeName)
	}
}

func inputObjectToTypedInput(value any, typ *ast.Definition, schema *ast.Schema) (dagql.Input, error) {
	objMap, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object, got %T", value)
	}

	var inputs []dagql.NamedInput
	for fieldName, fieldValue := range objMap {
		field := typ.Fields.ForName(fieldName)
		if field == nil {
			return nil, fmt.Errorf("field %q not found in input type %q", fieldName, typ.Name)
		}

		input, err := valueToTypedInput(fieldValue, field.Type, schema)
		if err != nil {
			return nil, fmt.Errorf("invalid value for field %q: %w", fieldName, err)
		}

		inputs = append(inputs, dagql.NamedInput{
			Name:  fieldName,
			Value: input,
		})
	}

	return mapInput(inputs), nil
}

// mapInput implements Input for []NamedInput
type mapInput []dagql.NamedInput

func (m mapInput) Type() *ast.Type {
	return &ast.Type{
		NamedType: "InputObject",
		NonNull:   true,
	}
}

func (m mapInput) ToLiteral() call.Literal {
	fields := make([]*call.Argument, len(m))
	for i, input := range m {
		fields[i] = call.NewArgument(input.Name, input.Value.ToLiteral(), false)
	}
	return call.NewLiteralObject(fields...)
}

func (m mapInput) Decoder() dagql.InputDecoder {
	return m
}

func (m mapInput) DecodeInput(val any) (dagql.Input, error) {
	switch v := val.(type) {
	case map[string]any:
		var inputs []dagql.NamedInput
		for k, val := range v {
			input, err := valueToTypedInput(val, &ast.Type{NamedType: "Any"}, nil)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", k, err)
			}
			inputs = append(inputs, dagql.NamedInput{
				Name:  k,
				Value: input,
			})
		}
		return mapInput(inputs), nil
	default:
		return nil, fmt.Errorf("expected map, got %T", val)
	}
}

func enumToTypedInput(value any, typ *ast.Definition) (dagql.Input, error) {
	str, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("expected string for enum, got %T", value)
	}

	// Validate enum value
	valid := false
	for _, enumVal := range typ.EnumValues {
		if enumVal.Name == str {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("invalid enum value %q for type %q", str, typ.Name)
	}

	return dagql.String(str), nil
}

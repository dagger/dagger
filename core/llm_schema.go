package core

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

const jsonSchemaIDAttr = "x-id-type"

// fieldArgsToJSONSchema converts a GraphQL field's arguments to a JSON schema
// object suitable for LLM tool calling. If includeSelf is true, a "self"
// parameter is added for object methods.
func fieldArgsToJSONSchema(schema *ast.Schema, typeDef *ast.Definition, field *ast.FieldDefinition, includeSelf bool) (map[string]any, error) {
	properties := map[string]any{}
	required := []string{}
	for _, arg := range field.Arguments {
		// Skip Workspace args — they are automatically filled in.
		if arg.Type.Name() == "WorkspaceID" {
			continue
		}

		argSchema, err := typeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}

		// Add description
		desc := arg.Description
		if idType, ok := argSchema[jsonSchemaIDAttr]; ok {
			if desc == "" {
				desc = fmt.Sprintf("(%s ID)", idType)
			} else {
				desc = fmt.Sprintf("(%s ID) %s", idType, desc)
			}
		}
		if desc != "" {
			argSchema["description"] = desc
		}

		// Add default value if present
		if arg.DefaultValue != nil {
			val, err := arg.DefaultValue.Value(nil)
			if err != nil {
				return nil, fmt.Errorf("default value: %w", err)
			}
			argSchema["default"] = val
		}

		properties[arg.Name] = argSchema

		if arg.Type.NonNull && arg.DefaultValue == nil {
			required = append(required, arg.Name)
		}
	}
	if includeSelf {
		properties["self"] = map[string]any{
			"type":        []string{"string", "null"},
			"description": "The ID of the object to call this method on",
		}
		required = append(required, "self")
	}
	jsonSchema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		jsonSchema["required"] = required
	}
	return jsonSchema, nil
}

func typeToJSONSchema(schema *ast.Schema, t *ast.Type) (map[string]any, error) {
	jsonSchema := map[string]any{}

	if t.Elem != nil {
		jsonSchema["type"] = "array"
		items, err := typeToJSONSchema(schema, t.Elem)
		if err != nil {
			return nil, fmt.Errorf("elem type: %w", err)
		}
		jsonSchema["items"] = items
		return jsonSchema, nil
	}

	switch t.NamedType {
	case "Int":
		jsonSchema["type"] = "integer"
	case "Float":
		jsonSchema["type"] = "number"
	case "String":
		jsonSchema["type"] = "string"
	case "Boolean":
		jsonSchema["type"] = "boolean"
	default:
		typeDef, found := schema.Types[t.NamedType]
		if !found {
			return nil, fmt.Errorf("unknown type (impossible?): %q", t.NamedType)
		}
		switch typeDef.Kind {
		case ast.InputObject:
			jsonSchema["type"] = "object"
			properties := map[string]any{}
			for _, f := range typeDef.Fields {
				fieldSpec, err := typeToJSONSchema(schema, f.Type)
				if err != nil {
					return nil, fmt.Errorf("field type: %w", err)
				}
				properties[f.Name] = fieldSpec
			}
			jsonSchema["properties"] = properties
		case ast.Enum:
			jsonSchema["type"] = "string"
			var enum []string
			for _, val := range typeDef.EnumValues {
				enum = append(enum, val.Name)
			}
			jsonSchema["enum"] = enum
		case ast.Scalar:
			if strings.HasSuffix(t.NamedType, "ID") {
				typeName := strings.TrimSuffix(t.NamedType, "ID")
				jsonSchema["type"] = "string"
				jsonSchema[jsonSchemaIDAttr] = typeName
			} else {
				jsonSchema["type"] = "string"
			}
		default:
			return nil, fmt.Errorf("unhandled type: %s (%s)", t, typeDef.Kind)
		}
	}

	return jsonSchema, nil
}

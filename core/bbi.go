package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// BBI stands for "Body-Brain Interface".
// A BBI implements a strategy for mapping a Dagger object's API to LLM function calls
// The perfect BBI has not yet been designed, so there are multiple BBI implementations,
// and an interface for easily swapping them out.
// Hopefully in the future the perfect BBI design will emerge, and we can retire
// the pluggable interface.

type BBI[T dagql.Object] interface {
	NewSession(self T, def ast.Definition) BBISession
}

type BBISession interface {
	// Return a set of tools for the next llm loop
	// The tools may modify the state without worrying about synchronization:
	// it's the agent's responsibility to not call tools concurrently.
	Tools() []Tool
	Self() dagql.Object
}

// A frontend for LLM tool calling
type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
	Call        func(context.Context, interface{}) (interface{}, error)
}

//
// BBI IMPLEMENTATIONS:
//

// The "one-one" BBI strategy
// Each Dagger function is mapped "one to one" to a tool.
// This is derived from aluzzardi's "langdag" Hack Day demo on Jan 23 2025
type OneOneBBI struct{}

type OneOneBBISession struct {
	self dagql.Object
	srv  *dagql.Server
	def  *ast.Definition
}

func (bbi OneOneBBI) NewSession(self dagql.Object, srv *dagql.Server) (BBISession, error) {
	typename := self.Type().Name()
	def, ok := srv.Schema().Types[typename]
	if !ok {
		return nil, fmt.Errorf("can't introspect type: %s", typename)
	}
	return &OneOneBBISession{
		self: self,
		srv:  srv,
		def:  def,
	}, nil
}

func (s *OneOneBBISession) Self() dagql.Object {
	return s.self
}

func (s OneOneBBISession) Tools() []Tool {
	var tools []Tool
	for _, field := range s.def.Fields {
		tools = append(tools, Tool{
			Name:        field.Name,
			Description: field.Description,
			Schema:      fieldArgsToJSONSchema(field),
			Call: func(ctx context.Context, args interface{}) (interface{}, error) {
				// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
				argsMap, ok := args.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", field.Name, args)
				}
				classField, ok := s.self.ObjectType().FieldSpec(field.Name)
				if !ok {
					return nil, fmt.Errorf("field %q not found in object type %q", field.Name, s.self.ObjectType().TypeName())
				}
				sel := dagql.Selector{
					Field: field.Name,
				}
				for _, arg := range classField.Args {
					val, ok := argsMap[arg.Name]
					if !ok {
						continue
					}
					input, err := arg.Type.Decoder().DecodeInput(val)
					if err != nil {
						return nil, fmt.Errorf("decode arg %q: %w", arg.Name, err)
					}
					sel.Args = append(sel.Args, dagql.NamedInput{
						Name:  arg.Name,
						Value: input,
					})
				}
				// 2. MAKE THE CALL
				val, id, err := s.self.Select(ctx, s.srv, sel)
				if err != nil {
					return nil, fmt.Errorf("select: %w", err)
				}
				// 3. CONVERT CALL OUTPUT (BODY -> BRAIN)
				if field.Type.IsCompatible(s.self.Type()) {
					// Field returns the object field (chaining).
					// We move the agent's cursor to the new value in the chain
					self, err := s.self.ObjectType().New(id, val)
					if err != nil {
						return nil, fmt.Errorf("new object: %w", err)
					}
					_, span := Tracer(ctx).Start(ctx, fmt.Sprintf("[🤖] 📦 new state: %s", self.ID().Digest()))
					s.self = self
					span.End()
					return "ok", nil
				}
				// Check if return type is an object by looking it up in the schema
				if objType, ok := s.srv.Schema().Types[field.Type.Name()]; ok && objType.Kind == ast.Object {
					return fmt.Sprintf("The returned %s is at id %s", field.Type.Name(), id.Digest()), nil
				}

				// For scalar types, just marshal to string
				b, err := json.Marshal(val)
				if err != nil {
					return nil, fmt.Errorf("marshal value: %w", err)
				}
				return string(b), nil
			},
		})
	}
	return tools
}

func fieldArgsToJSONSchema(field *ast.FieldDefinition) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
	properties := schema["properties"].(map[string]interface{})
	required := []string{}

	for _, arg := range field.Arguments {
		argSchema := typeToJSONSchema(arg.Type)

		// Add description if present
		if arg.Description != "" {
			argSchema["description"] = arg.Description
		}

		// Add default value if present
		if arg.DefaultValue != nil {
			argSchema["default"] = arg.DefaultValue.Raw
		}

		properties[arg.Name] = argSchema

		// Track required fields (non-null without default)
		if arg.Type.NonNull && arg.DefaultValue == nil {
			required = append(required, arg.Name)
		}
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	return schema
}

func typeToJSONSchema(t *ast.Type) map[string]interface{} {
	schema := map[string]interface{}{}

	// Handle lists
	if t.Elem != nil {
		schema["type"] = "array"
		schema["items"] = typeToJSONSchema(t.Elem)
		return schema
	}

	// Handle base types
	switch t.NamedType {
	case "Int":
		schema["type"] = "integer"
	case "Float":
		schema["type"] = "number"
	case "String":
		schema["type"] = "string"
	case "Boolean":
		schema["type"] = "boolean"
	case "ID":
		schema["type"] = "string"
		schema["format"] = "id"
	default:
		// For custom types, use string format with the type name
		schema["type"] = "string"
		schema["format"] = t.NamedType
	}

	return schema
}

package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
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
	IDs  map[string]*call.ID
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
		IDs:  make(map[string]*call.ID),
	}, nil
}

func (s *OneOneBBISession) Self() dagql.Object {
	return s.self
}

func (s *OneOneBBISession) Tools() []Tool {
	objectTypes := make(map[string]*ast.Definition)
	// Load top-level tools from the self type
	return s.tools(s.def, true, objectTypes)
}

func (s *OneOneBBISession) call(ctx context.Context, field *ast.FieldDefinition, args interface{}) (dagql.Typed, *call.ID, error) {
	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", field.Name, args)
	}
	classField, ok := s.self.ObjectType().FieldSpec(field.Name)
	if !ok {
		return nil, nil, fmt.Errorf("field %q not found in object type %q", field.Name, s.self.ObjectType().TypeName())
	}
	sel := dagql.Selector{
		Field: field.Name,
	}
	target := s.self
	for _, arg := range classField.Args {
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
		}
		// Is this argument of ID type?
		if strings.HasSuffix(arg.Type.Type().Name(), "ID") {
			// Translate ID digest back to the full-size ID
			id, ok := s.IDs[val.(string)]
			if !ok {
				return nil, nil, fmt.Errorf("ID lookup failed: %s", val.(string))
			}
			if arg.Name == "id" {
				obj, err := s.srv.Load(ctx, id)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to load %s %s: %s", arg.Type.Type().Name(), id.Display(), err.Error())
				}
				target = obj
				continue
			}
			idVal, err := id.Encode()
			if err != nil {
				return nil, nil, fmt.Errorf("encode ID after lookup: %s", err.Error())
			}
			val = idVal
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return nil, nil, fmt.Errorf("decode arg %q: %w", arg.Name, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	// 2. MAKE THE CALL
	return target.Select(ctx, s.srv, sel)
}

// Return true if the given type is an object
func (s *OneOneBBISession) isObjectType(t *ast.Type) bool {
	objType, ok := s.srv.Schema().Types[t.Name()]
	if !ok {
		return false
	}
	return objType.Kind == ast.Object
}

func (s *OneOneBBISession) tools(typedef *ast.Definition, toplevel bool, objectTypes map[string]*ast.Definition) []Tool {
	slog.Debug("Loading tools from type", "type", typedef.Name)
	var tools []Tool
	for _, field := range typedef.Fields {
		slog.Debug("Loading tool from field", "type", typedef.Name, "field", field.Name)
		var name = field.Name
		// Hide some special fields to avoid tool explosion
		if name == "asAgent" && name == "asModule" {
			slog.Debug("Hiding special field", "type", typedef.Name, "field", field.Name)
			continue
		}
		if !toplevel {
			slog.Debug("Adding special argument 'id' to non-toplevel tool", "type", typedef.Name, "field", field.Name)
			// We are extracting tools from a non-toplevel type
			// Let's prefix the tool with the type name (eg. WithNewFile in Directory will become DirectoryWithNewFile())
			name = typedef.Name + name
			// Let's add an ID argument
			field.Arguments = append(field.Arguments, &ast.ArgumentDefinition{
				Description: fmt.Sprintf("The ID of the input %s", typedef.Name),
				Name:        "id",
				Type: &ast.Type{
					NamedType: fmt.Sprintf("%sID", typedef.Name),
					NonNull:   true,
				},
			})
		}
		tool := Tool{
			Name:        name,
			Description: field.Description,
			Schema:      fieldArgsToJSONSchema(field),
		}
		// We process the result of the function differently depending on the return type
		if field.Type.IsCompatible(s.self.Type()) {
			slog.Debug("Field returns self-type. Tool will auto-chain", "type", typedef.Name, "field", field.Name)
			// CASE 1: the function returns the self type (chainable)
			tool.Call = func(ctx context.Context, args any) (any, error) {
				val, id, err := s.call(ctx, field, args)
				if err != nil {
					return nil, err
				}
				// We always mutate the agent's self state (auto-chaining)
				// FIXME: no way to create ephemeral copies of yourself.
				// 	maybe make chaining opt-in, with a special "return" tool?
				self, err := s.self.ObjectType().New(id, val)
				if err != nil {
					return nil, fmt.Errorf("new object: %w", err)
				}
				_, span := Tracer(ctx).Start(ctx, fmt.Sprintf("[🤖] 📦 new state: %s", self.ID().Digest()))
				s.self = self
				span.End()
				// FIXME: send the state digest for extra awareness of state changes?
				return "ok", nil
			}
		} else if s.isObjectType(field.Type) {
			slog.Debug("Field returns non-self object type. Tool will return ID digest", "type", typedef.Name, "field", field.Name)
			// CASE 2: the function returns an object type other than the self type
			tool.Call = func(ctx context.Context, args any) (any, error) {
				_, id, err := s.call(ctx, field, args)
				if err != nil {
					return nil, err
				}
				// We send the return object's ID (in digest form to save tokens) + the ID type (to facilitate chaining)
				// FIXME: gotta lookup the IDs from digests when receiving them..
				idDigest := id.Digest().String()
				s.IDs[idDigest] = id
				return fmt.Sprintf("<%[1]s>%s</%[1]s>", field.Type.Name(), idDigest), nil
			}
			// Track the return type so that we can extract that type's tools later
			objTypeName := field.Type.Name()
			objType := s.srv.Schema().Types[objTypeName]
			if _, alreadyFound := objectTypes[objTypeName]; !alreadyFound {
				slog.Debug("Field returns object type we haven't seen before", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
				if objTypeName != "Module" && (!strings.HasSuffix(objTypeName, "Agent")) {
					slog.Debug("Recursively loading tools from newly found return type", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
					// First time we see this type. Let's recursively scan it for tools
					objectTypes[objTypeName] = objType
					tools = append(tools, s.tools(objType, false, objectTypes)...)
				} else {
					slog.Debug("Skipping newly found return type: it's on the 'do not scan' list", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
				}
			}
		} else {
			slog.Debug("Field returns non-object type. Tool will return its value", "type", typedef.Name, "field", field.Name)
			// CASE 3: the function a non-object type
			tool.Call = func(ctx context.Context, args any) (any, error) {
				val, _, err := s.call(ctx, field, args)
				// We just return the value, and delegate marshalling it to the core implementation
				return val, err
			}
		}
		tools = append(tools, tool)
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

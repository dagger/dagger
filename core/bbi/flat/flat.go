// A BBI driver implementing the "flat" strategy
//
// The core idea, derived from aluzzardi's "langdag" Hack Day demo on Jan 23 2025,
// is this: each Dagger function in the host object is mapped one-to-one to a tool
// - Each Dagger function is mapped "one to one" to a tool.
// - Chaining is handled by flattening multiple type's functions in a single namespace

package flat

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/bbi"
)

func init() {
	bbi.Register("flat", new(Driver))
}

type Driver struct{}

type Session struct {
	self dagql.Object
	srv  *dagql.Server
	def  *ast.Definition
	IDs  map[string]*call.ID
}

func (d Driver) NewSession(self dagql.Object, srv *dagql.Server) bbi.Session {
	var def *ast.Definition
	if self != nil {
		def = srv.Schema().Types[self.Type().Name()]
	}
	return &Session{
		self: self,
		srv:  srv,
		def:  def,
		IDs:  make(map[string]*call.ID),
	}
}

func (s *Session) Self() dagql.Object {
	return s.self
}

func (s *Session) Tools() []bbi.Tool {
	objectTypes := make(map[string]*ast.Definition)
	// Load top-level tools from the self type
	return s.tools(s.def, true, objectTypes)
}

func (s *Session) LookupObject(ctx context.Context, idDigest string) (dagql.Object, error) {
	id, err := s.LookupObjectID(ctx, idDigest)
	if err != nil {
		return nil, err
	}
	return s.srv.Load(ctx, id)
}

func (s *Session) LookupObjectID(ctx context.Context, idDigest string) (*call.ID, error) {
	slog.Debug("looking up ID from digest", "digest", idDigest)
	id, ok := s.IDs[idDigest]
	if !ok {
		return nil, fmt.Errorf("ID lookup failed: %s", idDigest)
	}
	return id, nil
}

// Return true if the given dagql type has been returned to the model at least once
// We use this for tool optimization
func (s *Session) TypeWasReturned(typename string) bool {
	for _, id := range s.IDs {
		if id.Type().NamedType() == typename {
			return true
		}
	}
	return false
}

func (s *Session) call(ctx context.Context, fieldDef *ast.FieldDefinition, args interface{}, toplevel bool) (dagql.Typed, *call.ID, error) {
	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", fieldDef.Name, args)
	}
	// Target may be the state object, or another object we lookup by ID
	target := s.self
	if !toplevel {
		slog.Debug("processing special argument 'id'", "field", fieldDef.Name)
		obj, err := s.LookupObject(ctx, argsMap["id"].(string))
		if err != nil {
			return nil, nil, err
		}
		target = obj
	}
	targetType, ok := s.srv.ObjectType(target.Type().Name())
	if !ok {
		return nil, nil, fmt.Errorf("dagql object type not found: %s", targetType)
	}
	field, ok := targetType.FieldSpec(fieldDef.Name, "v0.13.2")
	if !ok {
		// FIXME: Container.withExec is not found here, why??
		return nil, nil, fmt.Errorf("field %q not found in object type %q (toplevel=%v)", fieldDef.Name, target.ObjectType().TypeName(), toplevel)
	}
	sel := dagql.Selector{
		Field: fieldDef.Name,
	}
	for _, arg := range field.Args {
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
		}
		// Is this argument of ID type?
		if strings.HasSuffix(arg.Type.Type().Name(), "ID") {
			// Translate ID digest back to the full-size ID
			id, err := s.LookupObjectID(ctx, val.(string))
			if err != nil {
				return nil, nil, err
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
func (s *Session) isObjectType(t *ast.Type) bool {
	objType, ok := s.srv.Schema().Types[t.Name()]
	if !ok {
		return false
	}
	slog.Debug("Checking if type is an object", "typeName", t.NamedType, "kind", objType.Kind)
	return objType.Kind == ast.Object
}

func (s *Session) tools(typedef *ast.Definition, toplevel bool, objectTypes map[string]*ast.Definition) []bbi.Tool {
	if typedef == nil {
		return nil
	}
	slog.Debug("Loading tools from type", "type", typedef.Name)
	var tools []bbi.Tool
	for _, field := range typedef.Fields {
		slog.Debug("Loading tool from field", "type", typedef.Name, "field", field.Name)
		var name = field.Name
		// Hide some special fields to avoid tool explosion
		if (name == "agent") || (name == "asModule") {
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
		tool := bbi.Tool{
			Name:        name,
			Description: field.Description,
			Schema:      fieldArgsToJSONSchema(field),
		}
		// We process the result of the function differently depending on the return type
		if field.Type.IsCompatible(s.self.Type()) {
			slog.Debug("Field returns self-type. Tool will auto-chain", "type", typedef.Name, "field", field.Name)
			// CASE 1: the function returns the self type (chainable)
			tool.Call = func(ctx context.Context, args any) (any, error) {
				_, id, err := s.call(ctx, field, args, toplevel)
				if err != nil {
					return nil, err
				}
				// We always mutate the agent's self state (auto-chaining)
				// FIXME: no way to create ephemeral copies of yourself.
				// 	maybe make chaining opt-in, with a special "return" tool?
				obj, err := s.srv.Load(ctx, id)
				if err != nil {
					return nil, err
				}
				// If target object has a sync() function, call it
				if _, syncID, err := obj.Select(ctx, s.srv, dagql.Selector{
					Field: "sync",
					View:  "v0.13.2",
				}); err == nil {
					syncObj, err := s.srv.Load(ctx, syncID)
					if err != nil {
						return nil, err
					}
					obj = syncObj
				}
				s.self = obj
				// FIXME: send the state digest for extra awareness of state changes?
				return "ok", nil
			}
		} else if s.isObjectType(field.Type) {
			slog.Debug("Field returns non-self object type. Tool will return ID digest", "type", typedef.Name, "field", field.Name)
			// CASE 2: the function returns an object type other than the self type
			tool.Call = func(ctx context.Context, args any) (any, error) {
				_, id, err := s.call(ctx, field, args, toplevel)
				if err != nil {
					return nil, err
				}
				// We send the return object's ID (in digest form to save tokens) + the ID type (to facilitate chaining)
				// FIXME: gotta lookup the IDs from digests when receiving them..
				idDigest := id.Digest().String()
				s.IDs[idDigest] = id
				//return fmt.Sprintf("<%[1]sID>%s</%[1]sID>", field.Type.Name(), idDigest), nil
				return idDigest, nil
			}
			// Track the return type so that we can extract that type's tools later
			objTypeName := field.Type.Name()
			objType := s.srv.Schema().Types[objTypeName]
			if _, alreadyFound := objectTypes[objTypeName]; !alreadyFound {
				slog.Debug("Field returns object type we haven't seen before", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
				if objTypeName != "Module" && (!strings.HasSuffix(objTypeName, "Agent")) {
					// First time we see this type. Let's recursively scan it for tools
					if s.TypeWasReturned(objTypeName) {
						slog.Debug("Recursively loading tools from newly found return type", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
						objectTypes[objTypeName] = objType
						tools = append(tools, s.tools(objType, false, objectTypes)...)
					} else {
						slog.Debug("Skipping newly found return type: we haven't returned it yet", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
					}
				} else {
					slog.Debug("Skipping newly found return type: it's on the 'do not scan' list", "type", typedef.Name, "field", field.Name, "fieldType", objTypeName)
				}
			}
		} else {
			slog.Debug("Field returns non-object type. Tool will return its value", "type", typedef.Name, "field", field.Name)
			// CASE 3: the function a non-object type
			tool.Call = func(ctx context.Context, args any) (any, error) {
				val, _, err := s.call(ctx, field, args, toplevel)
				if err != nil {
					return "", err
				}
				// We just return the value, and delegate marshalling it to the core implementation
				return val, nil
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

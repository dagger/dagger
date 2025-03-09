package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

// A frontend for LLM tool calling
type LlmTool struct {
	// Tool name
	Name string
	// Tool description
	Description string
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any
	// Function implementing the tool.
	Call func(context.Context, any) (any, error)
}

type LlmEnv struct {
	// History of values. Current selection is last. Remove last N values to rewind last N changes
	history []dagql.Typed
	// Saved objects
	objs map[string]dagql.Typed
	srv  *dagql.Server
}

// Lookup dagql typedef for a given dagql value
func (env *LlmEnv) typedef(val dagql.Typed) *ast.Definition {
	return env.srv.Schema().Types[val.Type().Name()]
}

// Return the current selection
func (env *LlmEnv) Current() dagql.Typed {
	if len(env.history) == 0 {
		return nil
	}
	return env.history[len(env.history)-1]
}

// Save a value at the given key
func (env *LlmEnv) Set(key string, value dagql.Typed) {
	env.objs[key] = value
}

// Get a value saved at the given key
func (env *LlmEnv) Get(key string) (dagql.Typed, error) {
	val, exists := env.objs[key]
	if !exists {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return val, nil
}

// Unset a saved value
func (env *LlmEnv) Unset(key string) {
	delete(env.objs, key)
}

func (env *LlmEnv) Tools() []LlmTool {
	tools := env.Builtins()
	if current := env.Current(); current != nil {
		typedef := env.typedef(current)
		for _, field := range typedef.Fields {
			tools = append(tools, LlmTool{
				Name:        field.Name,
				Description: field.Description,
				Schema:      fieldArgsToJSONSchema(field),
				Call: func(ctx context.Context, args any) (any, error) {
					val, id, err := env.call(ctx, field, args)
					if err != nil {
						return nil, err
					}
					if env.isObjectType(field.Type) {
						obj, err := env.srv.Load(ctx, id)
						if err != nil {
							return nil, err
						}
						env.history = append(env.history, obj)
						return "", nil
					}
					return val, nil
				},
			})
		}
	}
	return tools
}

// Low-level function call plumbing
func (env *LlmEnv) call(ctx context.Context,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	// The arguments to the call. Example: {"args": ["go", "build"], "redirectStderr", "/dev/null"}
	args any,
) (dagql.Typed, *call.ID, error) {
	target, ok := env.Current().(dagql.Object)
	if target == nil || !ok {
		return nil, nil, fmt.Errorf("function not found: %s", fieldDef.Name)
	}
	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", fieldDef.Name, args)
	}
	targetObjType, ok := env.srv.ObjectType(target.Type().Name())
	if !ok {
		return nil, nil, fmt.Errorf("dagql object type not found: %s", target.Type().Name())
	}
	// FIXME: we have to hardcode *a* version here, otherwise Container.withExec disappears
	// It's still kind of hacky
	field, ok := targetObjType.FieldSpec(fieldDef.Name, "v0.13.2")
	if !ok {
		return nil, nil, fmt.Errorf("field %q not found in object type %q", fieldDef.Name, targetObjType)
	}
	sel := dagql.Selector{
		Field: fieldDef.Name,
	}
	for _, arg := range field.Args {
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
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
	return target.Select(ctx, env.srv, sel)
}

func (env *LlmEnv) callObjects(ctx context.Context, _ any) (any, error) {
	var result string
	for name, obj := range env.objs {
		result += "- " + name + " (" + obj.Type().Name() + ")\n"
	}
	return result, nil
}

func (env *LlmEnv) callLoad(ctx context.Context, args any) (any, error) {
	name := args.(map[string]any)["name"].(string)
	value, err := env.Get(name)
	if err != nil {
		return nil, err
	}
	env.history = append(env.history, value)
	return value, nil
}

func (env *LlmEnv) callSave(ctx context.Context, args any) (any, error) {
	name := args.(map[string]any)["name"].(string)
	return name, nil
}

func (env *LlmEnv) callUndo(ctx context.Context, _ any) (any, error) {
	if len(env.history) > 0 {
		env.history = env.history[:len(env.history)-1]
	}
	return env.Current(), nil
}

func (env *LlmEnv) callType(ctx context.Context, args any) (any, error) {
	name := args.(map[string]any)["name"].(string)
	obj, err := env.Get(name)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	return obj.Type().Name(), nil
}

func (env *LlmEnv) callCurrent(ctx context.Context, _ any) (any, error) {
	if len(env.history) == 0 {
		return "", nil
	}
	return env.history[len(env.history)-1], nil
}

func (env *LlmEnv) Builtins() []LlmTool {
	builtins := []LlmTool{
		{
			Name:        "_objects",
			Description: "List saved objects with their types",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Call: env.callObjects,
		},
		{
			Name:        "_load",
			Description: "Load a saved object",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
			Call: env.callLoad,
		},
		{
			Name:        "_save",
			Description: "Save the current object",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
			Call: env.callSave,
		},
		{
			Name:        "_undo",
			Description: "Roll back the last action",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Call: env.callUndo,
		},
		{
			Name:        "_type",
			Description: "Print the type of a saved object",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
			Call: env.callType,
		},
		{
			Name:        "_current",
			Description: "Print the value of the current object",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Call: env.callCurrent,
		},
		{
			Name:        "_scratch",
			Description: "Clear the current object selection",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Call: func(ctx context.Context, _ any) (any, error) {
				env.history = append(env.history, nil)
				return nil, nil
			},
		},
	}
	// Attach builtin telemetry
	for i := range builtins {
		call := builtins[i].Call
		builtins[i].Call = func(ctx context.Context, args any) (any, error) {
			ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("🤖💻 %s %v", builtins[i].Name, args))
			defer span.End()
			return call(ctx, args)
		}
	}
	return builtins
}

func fieldArgsToJSONSchema(field *ast.FieldDefinition) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	properties := schema["properties"].(map[string]any)
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

func typeToJSONSchema(t *ast.Type) map[string]any {
	schema := map[string]any{}

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

// Return true if the given type is an object
func (env *LlmEnv) isObjectType(t *ast.Type) bool {
	objType, ok := env.srv.Schema().Types[t.Name()]
	if !ok {
		return false
	}
	slog.Debug("Checking if type is an object", "typeName", t.NamedType, "kind", objType.Kind)
	return objType.Kind == ast.Object
}

package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
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
	srv *dagql.Server

	// History of values. Current selection is last. Remove last N values to rewind last N changes
	history []dagql.Typed
	// Saved objects
	objs map[string]dagql.Typed
	// Saved objects by type + hash
	objsByHash map[digest.Digest]dagql.Typed
}

func NewLlmEnv(srv *dagql.Server) *LlmEnv {
	return &LlmEnv{
		srv:        srv,
		objs:       map[string]dagql.Typed{},
		objsByHash: map[digest.Digest]dagql.Typed{},
	}
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
	if obj, ok := dagql.UnwrapAs[dagql.Object](value); ok {
		env.objsByHash[obj.ID().Digest()] = value
	}
}

// Get a value saved at the given key
func (env *LlmEnv) Get(key string) (dagql.Typed, error) {
	if val, exists := env.objs[key]; exists {
		return val, nil
	}
	if _, hash, ok := strings.Cut(key, "@"); ok {
		// strip Type@ prefix if present
		// TODO: figure out the best place to do this
		key = hash
	}
	if val, exists := env.objsByHash[digest.Digest(key)]; exists {
		return val, nil
	}
	var dbg string
	for k, v := range env.objsByHash {
		dbg += fmt.Sprintf("hash %s: %s\n", k, v.Type().Name())
	}
	for k, v := range env.objs {
		dbg += fmt.Sprintf("var %s: %s\n", k, v.Type().Name())
	}
	return nil, fmt.Errorf("object not found: %s\n\n%s", key, dbg)
}

// Unset a saved value
func (env *LlmEnv) Unset(key string) {
	delete(env.objs, key)
}

func (env *LlmEnv) Tools() []LlmTool {
	tools := env.Builtins()
	typedefs := make(map[string]*ast.Definition)
	for _, val := range env.objs {
		typedef := env.typedef(val)
		typedefs[typedef.Name] = typedef
	}
	for typeName, typedef := range typedefs {
		for _, field := range typedef.Fields {
			tools = append(tools, LlmTool{
				Name:        typeName + "_" + field.Name,
				Description: field.Description,
				Schema:      fieldArgsToJSONSchema(field),
				Call: func(ctx context.Context, args any) (_ any, rerr error) {
					ctx, span := Tracer(ctx).Start(ctx,
						fmt.Sprintf("🤖💻 %s %v", typeName+"."+field.Name, args),
						telemetry.Passthrough(),
						telemetry.Reveal())
					defer telemetry.End(span, func() error { return rerr })
					val, id, err := env.call(ctx, field, args)
					if err != nil {
						return nil, err
					}
					if env.isObjectType(field.Type) {
						obj, err := env.srv.Load(ctx, id)
						if err != nil {
							return nil, err
						}
						env.objsByHash[obj.ID().Digest()] = obj
						env.history = append(env.history, obj)
						return id.Digest().String(), nil
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
	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", fieldDef.Name, args)
	}
	var target dagql.Object
	if varOrDigest, ok := argsMap["self"].(string); ok {
		val, err := env.Get(varOrDigest)
		if err != nil {
			return nil, nil, fmt.Errorf("tool call: %s: failed to get self: %w", fieldDef.Name, err)
		}
		if obj, ok := dagql.UnwrapAs[dagql.Object](val); ok {
			target = obj
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("function not found: %s", fieldDef.Name)
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
		if _, ok := dagql.UnwrapAs[dagql.IDable](arg.Type); ok {
			if idStr, ok := val.(string); ok {
				envVal, err := env.Get(idStr)
				if err != nil {
					return nil, nil, fmt.Errorf("tool call: %s: failed to get self: %w", fieldDef.Name, err)
				}
				if obj, ok := dagql.UnwrapAs[dagql.Object](envVal); ok {
					enc, err := obj.ID().Encode()
					if err != nil {
						return nil, nil, fmt.Errorf("tool call: %s: failed to encode ID: %w", fieldDef.Name, err)
					}
					val = enc
				} else {
					return nil, nil, fmt.Errorf("tool call: %s: expected object, got %T", fieldDef.Name, val)
				}
			} else {
				return nil, nil, fmt.Errorf("tool call: %s: expected string, got %T", fieldDef.Name, val)
			}
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return nil, nil, fmt.Errorf("decode arg %q (%T): %w", arg.Name, val, err)
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
			Description: "Load a saved object's functions/tools",
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
			Description: "Save the current object as a named variable",
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
		builtins[i].Call = func(ctx context.Context, args any) (_ any, rerr error) {
			ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("🤖💻 %s %v", builtins[i].Name, args))
			defer telemetry.End(span, func() error { return rerr })
			return call(ctx, args)
		}
	}
	return builtins
}

func fieldArgsToJSONSchema(field *ast.FieldDefinition) map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"self": map[string]any{
				"type":        "string",
				"description": "The object receiving the tool call",
			},
		},
	}
	properties := schema["properties"].(map[string]any)
	required := []string{"self"}
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

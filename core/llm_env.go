package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// A frontend for LLM tool calling
type LLMTool struct {
	// Tool name
	Name string
	// Tool description
	Description string
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any
	// Function implementing the tool.
	Call func(context.Context, any) (any, error)
}

type LLMEnv struct {
	// History of values. Current selection is last. Remove last N values to rewind last N changes
	history []dagql.Typed
	// Saved objects
	vars map[string]dagql.Typed
	// Saved objects by type + hash
	objsByHash map[digest.Digest]dagql.Typed
}

func NewLLMEnv() *LLMEnv {
	return &LLMEnv{
		vars:       map[string]dagql.Typed{},
		objsByHash: map[digest.Digest]dagql.Typed{},
	}
}

func (env *LLMEnv) Clone() *LLMEnv {
	cp := *env
	cp.history = cloneSlice(cp.history)
	cp.vars = cloneMap(cp.vars)
	cp.objsByHash = cloneMap(cp.objsByHash)
	return &cp
}

// Lookup dagql typedef for a given dagql value
func (env *LLMEnv) typedef(srv *dagql.Server, val dagql.Typed) *ast.Definition {
	return srv.Schema().Types[val.Type().Name()]
}

// Return the current selection
func (env *LLMEnv) Current() dagql.Typed {
	if len(env.history) == 0 {
		return nil
	}
	return env.history[len(env.history)-1]
}

func (env *LLMEnv) With(val dagql.Typed) {
	env.history = append(env.history, val)
}

// Save a value at the given key
func (env *LLMEnv) Set(key string, value dagql.Typed) string {
	prev := env.vars[key]
	env.vars[key] = value
	if obj, ok := dagql.UnwrapAs[dagql.Object](value); ok {
		env.objsByHash[obj.ID().Digest()] = value
	}
	if prev != nil {
		return fmt.Sprintf("The variable %q has changed from %s to %s.", key, env.describe(prev), env.describe(value))
	}
	return fmt.Sprintf("The variable %q has been set to %s.", key, env.describe(value))
}

// Get a value saved at the given key
func (env *LLMEnv) Get(key string) (dagql.Typed, error) {
	if val, exists := env.vars[key]; exists {
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
	for k, v := range env.vars {
		dbg += fmt.Sprintf("var %s: %s\n", k, v.Type().Name())
	}
	return nil, fmt.Errorf("object not found: %s\n\n%s", key, dbg)
}

// Unset a saved value
func (env *LLMEnv) Unset(key string) {
	delete(env.vars, key)
}

func (env *LLMEnv) Tools(srv *dagql.Server) []LLMTool {
	return append(env.Builtins(srv), env.tools(srv, env.Current())...)
}

func (env *LLMEnv) tools(srv *dagql.Server, obj dagql.Typed) []LLMTool {
	if obj == nil {
		return nil
	}
	typedef := env.typedef(srv, obj)
	typeName := typedef.Name
	var tools []LLMTool
	for _, field := range typedef.Fields {
		if strings.HasPrefix(field.Name, "_") {
			continue
		}
		if strings.HasPrefix(field.Name, "load") && strings.HasSuffix(field.Name, "FromID") {
			continue
		}
		tools = append(tools, LLMTool{
			Name:        typeName + "_" + field.Name, // TODO: try var_field.Name?
			Description: field.Description,
			Schema:      fieldArgsToJSONSchema(field),
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				ctx, span := Tracer(ctx).Start(ctx,
					fmt.Sprintf("🤖💻 %s %v", typeName+"."+field.Name, args),
					telemetry.Passthrough(),
					telemetry.Reveal())
				defer telemetry.End(span, func() error {
					return rerr
				})
				result, err := env.call(ctx, srv, field, args)
				if err != nil {
					return nil, err
				}
				stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
				defer stdio.Close()
				switch v := result.(type) {
				case string:
					fmt.Fprint(stdio.Stdout, v)
				default:
					enc := json.NewEncoder(stdio.Stdout)
					enc.SetIndent("", "  ")
					enc.Encode(v)
				}
				return result, nil
			},
		})
	}
	return tools
}

// Low-level function call plumbing
func (env *LLMEnv) call(ctx context.Context,
	srv *dagql.Server,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	// The arguments to the call. Example: {"args": ["go", "build"], "redirectStderr", "/dev/null"}
	args any,
) (any, error) {
	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", fieldDef.Name, args)
	}
	if env.Current() == nil {
		return nil, fmt.Errorf("no current context")
	}
	target, ok := dagql.UnwrapAs[dagql.Object](env.Current())
	if !ok {
		return nil, fmt.Errorf("current context is not an object, got %T", env.Current())
	}
	targetObjType, ok := srv.ObjectType(target.Type().Name())
	if !ok {
		return nil, fmt.Errorf("dagql object type not found: %s", target.Type().Name())
	}
	// FIXME: we have to hardcode *a* version here, otherwise Container.withExec disappears
	// It's still kind of hacky
	field, ok := targetObjType.FieldSpec(fieldDef.Name, "v0.13.2")
	if !ok {
		return nil, fmt.Errorf("field %q not found in object type %q", fieldDef.Name, targetObjType)
	}
	fieldSel := dagql.Selector{
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
					return nil, fmt.Errorf("tool call: %s: failed to get self: %w", fieldDef.Name, err)
				}
				if obj, ok := dagql.UnwrapAs[dagql.Object](envVal); ok {
					enc, err := obj.ID().Encode()
					if err != nil {
						return nil, fmt.Errorf("tool call: %s: failed to encode ID: %w", fieldDef.Name, err)
					}
					val = enc
				} else {
					return nil, fmt.Errorf("tool call: %s: expected object, got %T", fieldDef.Name, val)
				}
			} else {
				return nil, fmt.Errorf("tool call: %s: expected string, got %T", fieldDef.Name, val)
			}
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return nil, fmt.Errorf("decode arg %q (%T): %w", arg.Name, val, err)
		}
		fieldSel.Args = append(fieldSel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	// 2. MAKE THE CALL
	if retObjType, ok := srv.ObjectType(field.Type.Type().Name()); ok {
		var val dagql.Typed
		if sync, ok := retObjType.FieldSpec("sync"); ok {
			syncSel := dagql.Selector{
				Field: sync.Name,
			}
			idType, ok := retObjType.IDType()
			if !ok {
				return nil, fmt.Errorf("field %q is not an ID type", sync.Name)
			}
			if err := srv.Select(ctx, target, &idType, fieldSel, syncSel); err != nil {
				return nil, fmt.Errorf("failed to sync: %w", err)
			}
			syncedObj, err := srv.Load(ctx, idType.ID())
			if err != nil {
				return nil, fmt.Errorf("failed to load synced object: %w", err)
			}
			val = syncedObj
		} else if err := srv.Select(ctx, target, &val, fieldSel); err != nil {
			return nil, err
		}
		if obj, ok := dagql.UnwrapAs[dagql.Object](val); ok {
			env.objsByHash[obj.ID().Digest()] = val
		}
		env.history = append(env.history, val)
		return env.describe(val), nil
	}
	var val dagql.Typed
	if err := srv.Select(ctx, target, &val, fieldSel); err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}
	if id, ok := val.(dagql.IDType); ok {
		// avoid dumping full IDs, show the type and hash instead
		return env.describe(id), nil
	}
	return val, nil
}

func (env *LLMEnv) callObjects(ctx context.Context, _ any) (any, error) {
	var result string
	for name, obj := range env.vars {
		result += "- " + name + " (" + env.describe(obj) + ")\n"
	}
	return result, nil
}

func (env *LLMEnv) callSelectTools(ctx context.Context, args any) (any, error) {
	name := args.(map[string]any)["name"].(string)
	value, err := env.Get(name)
	if err != nil {
		return nil, err
	}
	env.history = append(env.history, value)
	return fmt.Sprintf("Switched tools to %s.", env.describe(value)), nil
}

func (env *LLMEnv) callSave(ctx context.Context, args any) (any, error) {
	name := args.(map[string]any)["name"].(string)
	return env.Set(name, env.Current()), nil
}

func (env *LLMEnv) callUndo(ctx context.Context, _ any) (any, error) {
	if len(env.history) > 0 {
		env.history = env.history[:len(env.history)-1]
	}
	return env.describe(env.Current()), nil
}

func (env *LLMEnv) callCurrent(ctx context.Context, _ any) (any, error) {
	if len(env.history) == 0 {
		return "", nil
	}
	return env.describe(env.history[len(env.history)-1]), nil
}

// describe returns a string representation of a typed object or object ID
func (env *LLMEnv) describe(val dagql.Typed) string {
	if val == nil {
		return fmt.Sprintf("<nil> (%T)", val)
	}
	if obj, ok := dagql.UnwrapAs[dagql.IDable](val); ok {
		return obj.ID().Type().ToAST().Name() + "@" + obj.ID().Digest().String()
	}
	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		return "[" + val.Type().Name() + "] (length: " + strconv.Itoa(list.Len()) + ")"
	}
	return val.Type().Name()
}

func (env *LLMEnv) Builtins(srv *dagql.Server) []LLMTool {
	builtins := []LLMTool{
		// {
		// 	Name:        "_objects",
		// 	Description: "List saved objects with their types. IMPORTANT: call this any time you seem to be missing objects for the request. Learn what objects are available, and then learn what tools they provide.",
		// 	Schema: map[string]any{
		// 		"type":       "object",
		// 		"properties": map[string]any{},
		// 	},
		// 	Call: env.callObjects,
		// },
		// {
		// 	Name:        "_selectTools",
		// 	Description: "Load an object's functions/tools. IMPORTANT: call this any time you seem to be missing tools for the request. This is a cheap option, so there is never a reason to give up without trying it first.",
		// 	Schema: map[string]any{
		// 		"type": "object",
		// 		"properties": map[string]any{
		// 			"name": map[string]any{
		// 				"type":        "string",
		// 				"description": "Variable name or hash of the object to load",
		// 			},
		// 		},
		// 		"required": []string{"name"},
		// 	},
		// 	Call: env.callSelectTools,
		// },
		{
			Name:        "_save",
			Description: "Save the current object as a named variable",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Variable name to save the object as",
					},
				},
				"required":             []string{"name"},
				"strict":               true,
				"additionalProperties": false,
			},
			Call: env.callSave,
		},
		{
			Name:        "_undo",
			Description: "Roll back the last action",
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"strict":               true,
				"required":             []string{},
				"additionalProperties": false,
			},
			Call: env.callUndo,
		},
		// TODO: don't think we need this
		// {
		// 	Name:        "_type",
		// 	Description: "Print the type of a saved object",
		// 	Schema: map[string]any{
		// 		"type": "object",
		// 		"properties": map[string]any{
		// 			"name": map[string]any{
		// 				"type":        "string",
		// 				"description": "Variable name to print the type of",
		// 			},
		// 		},
		// 	},
		// 	Call: env.callType,
		// },
		// {
		// 	Name:        "_current",
		// 	Description: "Print the value of the current object",
		// 	Schema: map[string]any{
		// 		"type":       "object",
		// 		"properties": map[string]any{},
		// 	},
		// 	Call: env.callCurrent,
		// },
		{
			Name:        "_scratch",
			Description: "Clear the current object selection",
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"strict":               true,
				"required":             []string{},
				"additionalProperties": false,
			},
			Call: func(ctx context.Context, _ any) (any, error) {
				env.history = append(env.history, nil)
				return nil, nil
			},
		},
	}
	for name, val := range env.vars {
		desc := fmt.Sprintf("Use tools for the variable %s (%s):\n", name, env.describe(val))
		tools := env.tools(srv, val)
		for _, tool := range tools {
			desc += fmt.Sprintf("\n- %s", tool.Name)
		}
		builtins = append(builtins, LLMTool{
			Name:        "_select_" + name,
			Description: desc,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"strict":               true,
				"required":             []string{},
				"additionalProperties": false,
			},
			Call: func(ctx context.Context, _ any) (any, error) {
				env.With(val)
				return fmt.Sprintf("Switched tools to %s.", env.describe(val)), nil
			},
		})
	}
	// Attach builtin telemetry
	for i, builtin := range builtins {
		builtins[i].Call = func(ctx context.Context, args any) (_ any, rerr error) {
			id := toolToID(builtin.Name, args)
			callAttr, err := id.Call().Encode()
			if err != nil {
				return nil, err
			}
			ctx, span := Tracer(ctx).Start(ctx, builtin.Name,
				trace.WithAttributes(
					attribute.String(telemetry.DagDigestAttr, id.Digest().String()),
					attribute.String(telemetry.DagCallAttr, callAttr),
					attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
				),
				telemetry.Reveal())
			defer telemetry.End(span, func() error { return rerr })
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			defer stdio.Close()
			res, err := builtin.Call(ctx, args)
			if err != nil {
				return nil, err
			}
			fmt.Fprintln(stdio.Stdout, res)
			return res, nil
		}
	}
	return builtins
}

func toolToID(name string, args any) *call.ID {
	var callArgs []*call.Argument
	if argsMap, ok := args.(map[string]any); ok {
		for k, v := range argsMap {
			lit, err := call.ToLiteral(v)
			if err != nil {
				lit = call.NewLiteralString(fmt.Sprintf("!(%v)(%s)", v, err))
			}
			callArgs = append(callArgs, call.NewArgument(k, lit, false))
		}
	}
	return call.New().Append(
		&ast.Type{
			NamedType: "String",
			NonNull:   true,
		},
		name, // fn name
		"",   // view
		nil,  // module
		0,    // nth
		"",   // custom digest
		callArgs...,
	)
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

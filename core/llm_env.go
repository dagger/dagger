package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// Return type (just a hint to the model)
	Returns string
	// Tool description
	Description string
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any
	// Function implementing the tool.
	Call func(context.Context, any) (any, error)
}

type LLMEnv struct {
	// History of values. Current selection is last. Remove last N values to rewind last N changes
	history      []dagql.Object
	functionMask map[string]bool
	// Saved objects
	vars map[string]dagql.Object
	// Saved objects by type + hash
	objsByHash map[digest.Digest]dagql.Object
	objsByID   map[string]dagql.Object
	// Auto incrementing number per-type
	typeCount map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
}

func NewLLMEnv() *LLMEnv {
	return &LLMEnv{
		vars:         map[string]dagql.Object{},
		objsByHash:   map[digest.Digest]dagql.Object{},
		objsByID:     map[string]dagql.Object{},
		typeCount:    map[string]int{},
		idByHash:     map[digest.Digest]string{},
		functionMask: map[string]bool{},
	}
}

func (env *LLMEnv) Clone() *LLMEnv {
	cp := *env
	cp.history = cloneSlice(cp.history)
	cp.vars = cloneMap(cp.vars)
	cp.objsByHash = cloneMap(cp.objsByHash)
	cp.typeCount = cloneMap(cp.typeCount)
	cp.idByHash = cloneMap(cp.idByHash)
	cp.objsByID = cloneMap(cp.objsByID)
	return &cp
}

// Lookup dagql typedef for a given dagql value
func (env *LLMEnv) typedef(srv *dagql.Server, val dagql.Typed) *ast.Definition {
	return srv.Schema().Types[val.Type().Name()]
}

// Return the current selection
func (env *LLMEnv) Current() dagql.Object {
	if len(env.history) == 0 {
		return nil
	}
	return env.history[len(env.history)-1]
}

func (env *LLMEnv) Select(obj dagql.Object, functions ...string) {
	env.history = append(env.history, obj)
	clear(env.functionMask)
	for _, fn := range functions {
		env.functionMask[fn] = true
	}
}

// Save a value at the given key
func (env *LLMEnv) Set(key string, obj dagql.Object) string {
	prev := env.vars[key]
	env.vars[key] = obj
	env.objsByHash[obj.ID().Digest()] = obj
	env.objsByID[env.llmID(obj)] = obj
	if prev != nil {
		return fmt.Sprintf("The variable %q has changed from %s to %s.", key, env.describe(prev), env.describe(obj))
	}
	return fmt.Sprintf("The variable %q has been set to %s.", key, env.describe(obj))
}

// Get a value saved at the given key
func (env *LLMEnv) Get(key string) (dagql.Object, error) {
	// strip $foo var prefix
	key = strings.TrimPrefix(key, "$")
	// first check for named vars
	if val, exists := env.vars[key]; exists {
		return val, nil
	}
	// next check for values by ID
	if val, exists := env.objsByID[key]; exists {
		return val, nil
	}
	// object by digest (xxh3:...)
	if _, hash, ok := strings.Cut(key, "@"); ok {
		// strip Type@ prefix if present
		key = hash
	}
	if val, exists := env.objsByHash[digest.Digest(key)]; exists {
		return val, nil
	}
	// check for non-xxh3: version too
	if val, exists := env.objsByHash[digest.Digest("xxh3:"+key)]; exists {
		return val, nil
	}
	helpfulErr := new(strings.Builder)
	fmt.Fprintf(helpfulErr, "Could not locate object %q.\n\n", key)
	if len(env.vars) > 0 {
		fmt.Fprintln(helpfulErr)
		fmt.Fprintln(helpfulErr, "Here are the defined variables:")
		for k, v := range env.vars {
			fmt.Fprintf(helpfulErr, "- %s (%s)\n", k, v.Type().Name())
		}
	}
	if len(env.objsByHash) > 0 {
		fmt.Fprintln(helpfulErr)
		fmt.Fprintln(helpfulErr, "Here are the available objects, by hash:")
		for k, v := range env.objsByHash {
			fmt.Fprintf(helpfulErr, "- %s (%s)\n", k, v.Type().Name())
		}
	}
	return nil, fmt.Errorf(helpfulErr.String())
}

// Unset a saved value
func (env *LLMEnv) Unset(key string) {
	delete(env.vars, key)
}

func (env *LLMEnv) Tools(srv *dagql.Server) []LLMTool {
	return append(env.Builtins(srv), env.tools(srv, env.Current())...)
}

// ToolFunc reuses our regular GraphQL args handling sugar for tools.
func ToolFunc[T any](fn func(context.Context, T) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, args any) (any, error) {
		vals, ok := args.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments: %T", args)
		}
		var t T
		specs, err := dagql.InputSpecsForType(t, true)
		if err != nil {
			return nil, err
		}
		inputs := map[string]dagql.Input{}
		for _, spec := range specs {
			var input dagql.Input
			if arg, provided := vals[spec.Name]; provided {
				input, err = spec.Type.Decoder().DecodeInput(arg)
				if err != nil {
					return nil, fmt.Errorf("decode arg %q (%+v): %w", spec.Name, arg, err)
				}
			} else if spec.Default != nil {
				input = spec.Default
			} else if spec.Type.Type().NonNull {
				return nil, fmt.Errorf("required argument %s not provided", spec.Name)
			}
			inputs[spec.Name] = input
		}
		if err := specs.Decode(inputs, &t); err != nil {
			return nil, err
		}
		return fn(ctx, t)
	}
}

func (env *LLMEnv) tools(srv *dagql.Server, obj dagql.Typed) []LLMTool {
	if obj == nil {
		return nil
	}
	typeDef := env.typedef(srv, obj)
	var tools []LLMTool
	masked := len(env.functionMask) > 0
	for _, field := range typeDef.Fields {
		if masked && !env.functionMask[field.Name] {
			continue
		}
		if strings.HasPrefix(field.Name, "_") {
			continue
		}
		if strings.HasPrefix(field.Name, "load") && strings.HasSuffix(field.Name, "FromID") {
			continue
		}
		if field.Name == "id" || field.Name == "sync" {
			// never a reason to call "sync" since we call it automatically
			continue
		}
		tools = append(tools, LLMTool{
			Name:        typeDef.Name + "_" + field.Name,
			Returns:     field.Type.String(),
			Description: field.Description,
			Schema:      fieldArgsToJSONSchema(field),
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				return env.call(ctx, srv, field, args)
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
) (_ any, rerr error) {
	var validated bool
	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("🤖💻 %s %v", fieldDef.Name, args),
		telemetry.Passthrough(),
		telemetry.Reveal())
	defer telemetry.End(span, func() error {
		if rerr != nil && !validated {
			// only reveal for "plumbing" errors, not errors from the field, since
			// those will already be shown
			span.SetAttributes(attribute.Bool(telemetry.UIPassthroughAttr, false))
		}
		return rerr
	})
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
					return nil, err
				}
				if obj, ok := dagql.UnwrapAs[dagql.Object](envVal); ok {
					enc, err := obj.ID().Encode()
					if err != nil {
						return nil, err
					}
					val = enc
				} else {
					return nil, fmt.Errorf("expected object, got %T", val)
				}
			} else {
				return nil, fmt.Errorf("expected string, got %T", val)
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
	validated = true
	// 2. MAKE THE CALL
	if retObjType, ok := srv.ObjectType(field.Type.Type().NamedType); ok {
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
			env.objsByHash[obj.ID().Digest()] = obj
			env.objsByID[env.llmID(obj)] = obj
			env.Select(obj)
			return env.currentState()
		} else {
			return nil, fmt.Errorf("impossible? object didn't return object: %T", val)
		}
	}
	var val dagql.Typed
	if err := srv.Select(ctx, target, &val, fieldSel); err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}
	switch x := val.(type) {
	case dagql.IDType:
		// avoid dumping full IDs, show the type and hash instead
		return env.describe(x), nil
	case dagql.String:
		bytes := []byte(x.String())
		if !utf8.Valid(bytes) {
			return toolStructuredResponse(map[string]any{
				"size":   len(bytes),
				"digest": digest.FromBytes(bytes),
			})
		}
		origSize := len(x)
		if origSize > maxStr {
			x = x[:maxStr]
			return toolStructuredResponse(map[string]any{
				"result":         x,
				"truncated":      true,
				"truncated_size": maxStr,
				"original_size":  origSize,
			})
		}
	}
	return toolStructuredResponse(map[string]any{
		"result": val,
	})
}

const maxStr = 8192

func (env *LLMEnv) callObjects(ctx context.Context, _ any) (any, error) {
	var result string
	for name, obj := range env.vars {
		result += "- " + name + " (" + env.describe(obj) + ")\n"
	}
	return result, nil
}

func (env *LLMEnv) callSelect(ctx context.Context, args struct {
	ID string `name:"id"`
}) (any, error) {
	obj, err := env.Get(args.ID)
	if err != nil {
		return nil, err
	}
	env.Select(obj)
	return env.currentState()
}

func (env *LLMEnv) callSave(ctx context.Context, args struct {
	Name string
}) (any, error) {
	_ = env.Set(args.Name, env.Current())
	return env.currentState()
}

func (env *LLMEnv) callRewind(ctx context.Context, _ struct{}) (any, error) {
	if len(env.history) > 0 {
		env.history = env.history[:len(env.history)-1]
	}
	return env.currentState()
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
		return "Void"
	}
	if obj, ok := dagql.UnwrapAs[dagql.IDable](val); ok {
		// NOTE: this covers both Objects and ID scalars
		return env.llmID(obj)
	}
	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		return val.Type().String() + " (length: " + strconv.Itoa(list.Len()) + ")"
	}
	return val.Type().String()
}

func (env *LLMEnv) llmID(idable dagql.IDable) string {
	id := idable.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	typeName := id.Type().NamedType()
	llmID, ok := env.idByHash[hash]
	if !ok {
		env.typeCount[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, env.typeCount[typeName])
		env.idByHash[hash] = llmID
	}
	return llmID
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
		// 	Name:        "_select",
		// 	Description: "Select an object as the current state",
		// 	Schema: map[string]any{
		// 		"type": "object",
		// 		"properties": map[string]any{
		// 			"id": map[string]any{
		// 				"type":        "string",
		// 				"description": "Object ID to select.",
		// 				"pattern":     IDPattern,
		// 			},
		// 		},
		// 		"required": []string{"id"},
		// 	},
		// 	Call: ToolFunc(env.callSelect),
		// },
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
		// {
		// 	Name:        "_scratch",
		// 	Returns:     "Void",
		// 	Description: "Clear the current object selection",
		// 	Schema: map[string]any{
		// 		"type":                 "object",
		// 		"properties":           map[string]any{},
		// 		"strict":               true,
		// 		"required":             []string{},
		// 		"additionalProperties": false,
		// 	},
		// 	Call: func(ctx context.Context, _ any) (any, error) {
		// 		env.history = append(env.history, nil)
		// 		return nil, nil
		// 	},
		// },
	}
	if cur := env.Current(); cur != nil {
		builtins = append(builtins,
			LLMTool{
				Name:        "_saveAs",
				Returns:     env.describe(cur),
				Description: "Save the current object to a named variable so you can return to it later using a `use_<name>` tool.\nOnly use this if you plan to leave the current object and may want to come back to it.",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Name of the variable to save",
						},
					},
					"strict":               true,
					"required":             []string{"name"},
					"additionalProperties": false,
				},
				Call: ToolFunc(env.callSave),
			},
			// LLMTool{
			// 	Name:        "_use",
			// 	Returns:     "Object",
			// 	Description: "Set the current state to an Object by ID",
			// 	Schema: map[string]any{
			// 		"type": "object",
			// 		"properties": map[string]any{
			// 			"id": map[string]any{
			// 				"type":        "string",
			// 				"description": "ID of the Object to use",
			// 			},
			// 		},
			// 		"strict":               true,
			// 		"required":             []string{"id"},
			// 		"additionalProperties": false,
			// 	},
			// 	Call: ToolFunc(env.callSelect),
			// },
		)
	}
	seenType := map[string]bool{}
	for _, val := range env.vars {
		typeName := val.Type().Name()
		if seenType[typeName] {
			continue
		}
		seenType[typeName] = true
		valDesc := env.describe(val)
		fnsDesc := "Available functions:\n"
		for _, tool := range env.tools(srv, val) {
			_, name, _ := strings.Cut(tool.Name, "_")
			fnsDesc += fmt.Sprintf("\n- %s", name)
		}
		builtins = append(builtins, LLMTool{
			Name:        "_use_" + typeName,
			Returns:     valDesc,
			Description: fmt.Sprintf("Set the current state to a %s.\n%s", typeName, fnsDesc),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"format":      typeName + "ID",
						"pattern":     IDPattern,
						"description": "ID of the Object to use.",
					},
					// "functions": map[string]any{
					// 	"type": "array",
					// 	"items": map[string]any{
					// 		"type": "string",
					// 	},
					// 	"description": "List of functions to use. " + fnsDesc,
					// },
				},
				"strict":               true,
				"required":             []string{"id"}, //, "functions"},
				"additionalProperties": false,
			},
			Call: ToolFunc(func(ctx context.Context, args struct {
				ID string `name:"id"`
				// Functions []string
			}) (any, error) {
				obj, err := env.Get(args.ID)
				if err != nil {
					return nil, err
				}
				env.Select(obj) //, args.Functions...)
				return env.currentState()
			}),
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

const IDPattern = `^[A-Z]\w+#\d+$`

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
		if strings.HasSuffix(t.NamedType, "ID") {
			schema["pattern"] = IDPattern
		}
	}

	return schema
}

func (env *LLMEnv) currentState() (string, error) {
	cur := env.Current()
	res := map[string]any{
		// "using" to hint to the model that it doesn't need to _use or _saveAs it
		"using": env.describe(cur),
	}
	// show when it's already a bound var to avoid unnecessary _saveAs
	for name, val := range env.vars {
		if cur.ID().Digest() == val.ID().Digest() {
			res["variable"] = name
		}
	}
	return toolStructuredResponse(res)
}

func toolStructuredResponse(val any) (string, error) {
	pl, err := json.Marshal(val)
	if err != nil {
		return "", fmt.Errorf("Failed to encode response %T: %w", val, err)
	}
	return string(pl), nil
}

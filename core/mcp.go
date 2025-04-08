package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
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

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	env *Env
	// The currently selected object.
	current dagql.Object
	// Whether the LLM needs instructions on how to use the tool scheme
	needsSystemPrompt bool
	// Only show these functions, if non-empty
	functionMask map[string]bool
	// Indicates that the model has returned
	returned bool
}

func newMCP(env *Env, endpoint *LLMEndpoint) *MCP {
	m := &MCP{
		env:          env,
		functionMask: map[string]bool{},
	}
	if env.Root() != nil {
		m.Select(env.Root())
	}
	if endpoint != nil {
		m.needsSystemPrompt = (endpoint.Provider == Google)
	}
	return m
}

//go:embed llm_dagger_prompt.md
var defaultSystemPrompt string

func (m *MCP) DefaultSystemPrompt() string {
	if m.needsSystemPrompt {
		return defaultSystemPrompt
	}
	return ""
}

func (m *MCP) WithEnvironment(env *Env) *MCP {
	m = m.Clone()
	m.env = env
	// We keep the current selection even if underlying environment is swapped out
	return m
}

func (m *MCP) Clone() *MCP {
	cp := *m
	cp.env = cp.env.Clone()
	cp.functionMask = cloneMap(cp.functionMask)
	return &cp
}

// Return the current selection
func (m *MCP) Current() dagql.Object {
	return m.current
}

// Change the current selection
func (m *MCP) Select(obj dagql.Object, functions ...string) {
	m.current = obj
	clear(m.functionMask)
	for _, fn := range functions {
		m.functionMask[fn] = true
	}
}

// Get an object saved at a given key
func (m *MCP) GetObject(key, expectedType string) (dagql.Object, error) {
	if expectedType != "" {
		// for maximal LLM compatibility, assume type for numeric ID args
		if onlyNum, err := strconv.Atoi(key); err == nil {
			key = fmt.Sprintf("%s#%d", expectedType, onlyNum)
		}
	}
	if b, exists := m.env.Input(key); exists {
		if obj, ok := b.AsObject(); ok {
			return obj, nil
		}
		return nil, fmt.Errorf("type error: %q exists but is not an object", key)
	}
	return nil, fmt.Errorf("unknown object %q", key)
}

func (m *MCP) Tools(srv *dagql.Server) ([]LLMTool, error) {
	builtins, err := m.Builtins(srv)
	if err != nil {
		return nil, err
	}
	if m.Current() == nil {
		return builtins, nil
	}
	objTools, err := m.tools(srv, m.Current().Type().Name())
	if err != nil {
		return nil, err
	}
	return append(builtins, objTools...), nil
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

func (m *MCP) tools(srv *dagql.Server, typeName string) ([]LLMTool, error) {
	schema := srv.Schema()
	typeDef, ok := schema.Types[typeName]
	if !ok {
		return nil, fmt.Errorf("type %q not found", typeName)
	}
	var tools []LLMTool
	masked := len(m.functionMask) > 0
	for _, field := range typeDef.Fields {
		if _, found := m.env.Input(field.Name); found {
			// If field conflicts with user input, user input wins
			continue
		}
		if masked && !m.functionMask[field.Name] {
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
		// Hide functions from the largest and most commonly used core types,
		// to prevent tool bloat
		switch typeName {
		case "Query":
			switch field.Name {
			case
				"currentModule",
				"currentTypeDefs",
				"defaultPlatform",
				"engine",
				"env",
				"error",
				"function",
				"generatedCode",
				"llm",
				"loadSecretFromName",
				"module",
				"moduleSource",
				"secret",
				"setSecret",
				"sourceMap",
				"typeDef",
				"version":
				continue
			}
		case "Container":
			switch field.Name {
			case
				"build",
				"defaultArgs",
				"entrypoint",
				"envVariable",
				"envVariables",
				"experimentalWithAllGPUs",
				"experimentalWithGPU",
				"export",
				"exposedPorts",
				"imageRef",
				"import",
				"label",
				"labels",
				"mounts",
				"pipeline",
				"platform",
				"rootfs",
				"terminal",
				"up",
				"user",
				"withAnnotation",
				"withDefaultTerminalCmd",
				"withFile",
				"withFiles",
				"withFocus",
				"withMountedCache",
				"withMountedDirectory",
				"withMountedFile",
				"withMountedSecret",
				"withMountedTemp",
				"withRootfs",
				"withoutAnnotation",
				"withoutDefaultArgs",
				"withoutEnvVariable",
				"withoutExposedPort",
				"withoutFile",
				"withoutFocus",
				"withoutMount",
				"withoutRegistryAuth",
				"withoutSecretVariable",
				"withoutUnixSocket",
				"withoutUser",
				"withoutWorkdir",
				"workdir":
				continue
			}
		case "Directory":
			switch field.Name {
			case
				// Nice to have, confusing
				"asModule",
				"asModuleSource",
				// Side effect
				"export",
				// Nice to have
				"name",
				// Side effect
				"terminal",
				// Nice to have, confusing
				"withFiles",
				"withTimestamps",
				"withoutDirectory",
				"withoutFile",
				"withoutFiles":
				continue
			}
		case "File":
			switch field.Name {
			case
				"export",
				"withName",
				"withTimestamps":
				continue
			}
		}
		if field.Directives.ForName(trivialFieldDirectiveName) != nil {
			// skip trivial fields on objects, only expose "real" functions
			// with implementations
			continue
		}
		schema, err := fieldArgsToJSONSchema(schema, field)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		var toolName string
		if typeName == "Query" {
			toolName = field.Name
		} else {
			toolName = typeDef.Name + "_" + field.Name
		}
		tools = append(tools, LLMTool{
			Name:        toolName,
			Returns:     field.Type.String(),
			Description: field.Description,
			Schema:      schema,
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				return m.call(ctx, srv, field, args)
			},
		})
	}
	return tools, nil
}

// Low-level function call plumbing
func (m *MCP) call(ctx context.Context,
	srv *dagql.Server,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	// The arguments to the call. Example: {"args": ["go", "build"], "redirectStderr", "/dev/null"}
	args any,
) (_ any, rerr error) {
	var validated bool
	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s %v", fieldDef.Name, args),
		telemetry.ActorEmoji("🤖"),
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
	//
	if m.Current() == nil {
		return nil, fmt.Errorf("no current context")
	}
	target, ok := dagql.UnwrapAs[dagql.Object](m.Current())
	if !ok {
		return nil, fmt.Errorf("current context is not an object, got %T", m.Current())
	}
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool call: %s: expected arguments to be a map - got %#v", fieldDef.Name, args)
	}
	fieldSel, err := m.toolCallToSelection(target, fieldDef, argsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to convert call inputs: %w", err)
	}
	validated = true

	// 2. MAKE THE CALL
	//
	return m.selectionToToolResult(ctx, srv, target, fieldDef, fieldSel)
}

func (m *MCP) selectionToToolResult(
	ctx context.Context,
	srv *dagql.Server,
	target dagql.Object,
	fieldDef *ast.FieldDefinition,
	fieldSel dagql.Selector,
) (any, error) {
	if retObjType, ok := srv.ObjectType(fieldDef.Type.NamedType); ok {
		// Handle object returns.
		//
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
			prev := m.Current()
			m.Select(obj)
			return m.currentState(prev)
		} else {
			return nil, fmt.Errorf("impossible? object didn't return object: %T", val)
		}
	} else if fieldDef.Type.Elem != nil {
		if _, isObj := srv.ObjectType(fieldDef.Type.Elem.NamedType); isObj {
			// Handle array object returns.
			//
			var objs []dagql.Object
			if err := srv.Select(ctx, target, &objs, fieldSel); err != nil {
				return nil, fmt.Errorf("failed to sync: %w", err)
			}
			var res []any
			for _, obj := range objs {
				res = append(res, m.env.Ingest(obj))
			}
			return toolStructuredResponse(map[string]any{
				"objects": res,
			})
		}
	}
	// Handle scalar or array-of-scalar returns.
	//
	var val dagql.Typed
	if err := srv.Select(ctx, target, &val, fieldSel); err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}
	if id, ok := dagql.UnwrapAs[dagql.IDType](val); ok {
		// avoid dumping full IDs, show the type and hash instead
		return m.describe(id), nil
	} else if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		bytes := []byte(str.String())
		if !utf8.Valid(bytes) {
			return toolStructuredResponse(map[string]any{
				"size":   len(bytes),
				"digest": digest.FromBytes(bytes),
			})
		}
		origSize := len(str)
		if origSize > maxStr {
			str = str[:maxStr]
			return toolStructuredResponse(map[string]any{
				"result":         str,
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

func (m *MCP) toolCallToSelection(
	target dagql.Object,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	argsMap map[string]any,
) (dagql.Selector, error) {
	sel := dagql.Selector{
		Field: fieldDef.Name,
	}
	targetObjType := target.ObjectType()
	field, ok := targetObjType.FieldSpec(fieldDef.Name, engine.Version)
	if !ok {
		return sel, fmt.Errorf("field %q not found in object type %q", fieldDef.Name, targetObjType)
	}
	for _, arg := range field.Args {
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
		}
		if _, ok := dagql.UnwrapAs[dagql.IDable](arg.Type); ok {
			if idStr, ok := val.(string); ok {
				idType := strings.TrimSuffix(arg.Type.Type().Name(), "ID")
				envVal, err := m.GetObject(idStr, idType)
				if err != nil {
					return sel, err
				}
				if obj, ok := dagql.UnwrapAs[dagql.Object](envVal); ok {
					enc, err := obj.ID().Encode()
					if err != nil {
						return sel, err
					}
					val = enc
				} else {
					return sel, fmt.Errorf("expected object, got %T", val)
				}
			} else {
				return sel, fmt.Errorf("expected string, got %T", val)
			}
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return sel, fmt.Errorf("decode arg %q (%T): %w", arg.Name, val, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	return sel, nil
}

const maxStr = 80 * 1024

func (m *MCP) describe(val dagql.Typed) string {
	if val == nil {
		return "Void"
	}
	if obj, ok := dagql.UnwrapAs[dagql.Object](val); ok {
		// NOTE: this covers both Objects and ID scalars
		return m.env.Ingest(obj)
	}
	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		return val.Type().String() + " (length: " + strconv.Itoa(list.Len()) + ")"
	}
	return val.Type().String()
}

func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall ToolCall) (string, bool) {
	var tool *LLMTool
	for _, t := range tools {
		if t.Name == toolCall.Function.Name {
			tool = &t
			break
		}
	}

	if tool == nil {
		errRes := map[string]any{
			"error": fmt.Sprintf("Tool '%s' is not available.", toolCall.Function.Name),
		}
		if typeName, _, ok := strings.Cut(toolCall.Function.Name, "_"); ok {
			if m.Current() == nil {
				errRes["hint"] = fmt.Sprintf("You have no current object. Try calling `select%s` first.", typeName)
			} else if m.Current().Type().Name() == typeName {
				errRes["hint"] = "The current object type does not provide this function."
			} else {
				errRes["hint"] = fmt.Sprintf("Your current object is a %s. Try calling `select%s` first.", m.Current().Type().Name(), typeName)
			}
		}
		payload, err := json.Marshal(errRes)
		if err != nil {
			return fmt.Sprintf("marshal error: %v", err), false
		}
		return string(payload), true
	}

	result, err := tool.Call(ctx, toolCall.Function.Arguments)
	if err != nil {
		errResponse := err.Error()
		// propagate error values to the model
		var extErr dagql.ExtendedError
		if errors.As(err, &extErr) {
			// TODO: return a structured error object instead?
			var exts []string
			for k, v := range extErr.Extensions() {
				var ext strings.Builder
				fmt.Fprintf(&ext, "<%s>\n", k)

				switch v := v.(type) {
				case string:
					ext.WriteString(v)
				default:
					jsonBytes, err := json.Marshal(v)
					if err != nil {
						fmt.Fprintf(&ext, "error marshalling value: %s", err.Error())
					} else {
						ext.Write(jsonBytes)
					}
				}

				fmt.Fprintf(&ext, "\n</%s>", k)

				exts = append(exts, ext.String())
			}
			if len(exts) > 0 {
				sort.Strings(exts)
				errResponse += "\n\n" + strings.Join(exts, "\n\n")
			}
		}
		return errResponse, true
	}

	switch v := result.(type) {
	case string:
		return v, false
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("Failed to marshal result: %s", err), true
		}
		return string(jsonBytes), false
	}
}

func (m *MCP) returnBuiltin() LLMTool {
	props := map[string]any{}
	required := []string{}
	for name, b := range m.env.outputsByName {
		typeName := b.expectedType.TypeName()
		required = append(required, name)
		props[name] = map[string]any{
			"type":           "string",
			"pattern":        idPattern(typeName),
			"description":    fmt.Sprintf("%s ID observed from a tool result, in \"%s#number\" format. %s", typeName, typeName, b.Description),
			jsonSchemaIDAttr: typeName,
		}
	}
	return LLMTool{
		Name: "return",
		Description: `Call this tool when you have gathered all required values and are ready to return them to the user.

Each parameter corresponds to a named result with a specific purpose. Do not call this tool until all values are ready.`,
		Schema: map[string]any{
			"type":                 "object",
			"properties":           props,
			"strict":               true,
			"required":             required,
			"additionalProperties": false,
		},
		Call: func(ctx context.Context, args any) (any, error) {
			vals, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid arguments: %T", args)
			}
			for name := range m.env.outputsByName {
				arg, ok := vals[name]
				if !ok {
					return nil, fmt.Errorf("required argument %s not provided", name)
				}
				argStr, ok := arg.(string)
				if !ok {
					return nil, fmt.Errorf("invalid type for argument %s: %T", name, arg)
				}
				bnd, ok := m.env.objsByID[argStr]
				if !ok {
					return nil, fmt.Errorf("object not found for argument %s: %s", name, argStr)
				}
				obj := bnd.Value
				if output, found := m.env.Output(name); found {
					if expected := output.expectedType; expected != nil {
						expectedType := expected.TypeName()
						actualType := obj.Type().Name()
						if expectedType != actualType {
							return nil, fmt.Errorf("incompatible types: %s must be %s, got %s", name, expectedType, actualType)
						}
					}
					output.Value = obj
				} else {
					return nil, fmt.Errorf("undefined output: %q", name)
				}
			}
			m.returned = true
			return "ok", nil
		},
	}
}

func (m *MCP) Builtins(srv *dagql.Server) ([]LLMTool, error) {
	builtins := []LLMTool{}

	if len(m.env.outputsByName) > 0 {
		builtins = append(builtins, m.returnBuiltin())
	}

	for _, typeName := range m.env.Types() {
		tools, err := m.tools(srv, typeName)
		if err != nil {
			return nil, fmt.Errorf("tools for %q: %w", typeName, err)
		}
		builtins = append(builtins, LLMTool{
			Name:    "select" + typeName,
			Returns: typeName,
			Description: (func() string {
				desc := fmt.Sprintf("Select a %s by its ID.", typeName)
				desc += "\n\nProvides the following tools:\n"
				for _, tool := range tools {
					desc += fmt.Sprintf("\n- %s", tool.Name)
				}
				return desc
			})(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":           "string",
						"pattern":        idPattern(typeName),
						"description":    fmt.Sprintf("The %s ID to select, in \"%s#number\" format.", typeName, typeName),
						jsonSchemaIDAttr: typeName,
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
				"required":             []string{"id"}, // , "functions"},
				"additionalProperties": false,
			},
			Call: ToolFunc(func(ctx context.Context, args struct {
				ID string `name:"id"`
				// Functions []string
			}) (any, error) {
				obj, err := m.GetObject(args.ID, typeName)
				if err != nil {
					return nil, err
				}
				prev := m.Current()
				m.Select(obj) // , args.Functions...)
				return m.currentState(prev)
			}),
		})
	}

	// register getters for all the env vars
	builtins = append(builtins, m.envGetters()...)

	// Attach builtin telemetry
	for i, builtin := range builtins {
		builtins[i].Call = func(ctx context.Context, args any) (_ any, rerr error) {
			attrs := []attribute.KeyValue{
				attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
			}
			// do an awkward dance to make sure we still show a span even if we fail
			// to construct parts of it (e.g. due to invalid input)
			setupErr := func() error {
				id, err := m.toolToID(builtin, args)
				if err != nil {
					return err
				}
				callAttr, err := id.Call().Encode()
				if err != nil {
					return err
				}
				attrs = append(attrs,
					attribute.String(telemetry.DagDigestAttr, id.Digest().String()),
					attribute.String(telemetry.DagCallAttr, callAttr))
				return nil
			}()
			ctx, span := Tracer(ctx).Start(ctx, builtin.Name,
				telemetry.Reveal(),
				trace.WithAttributes(attrs...),
			)
			defer telemetry.End(span, func() error { return rerr })
			if setupErr != nil {
				return nil, setupErr
			}
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
	return builtins, nil
}

func (m *MCP) envGetters() []LLMTool {
	var tools []LLMTool
	for _, input := range m.env.Inputs() {
		description := input.Description
		if description == "" {
			description = fmt.Sprintf("Retrieve the user input '%s' of type '%s'", input.Key, input.TypeName())
		}
		tools = append(tools, LLMTool{
			Name:        input.Key,
			Returns:     input.TypeName(),
			Description: description,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"strict":               true,
				"required":             []string{},
				"additionalProperties": false,
			},
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				if obj, isObj := input.AsObject(); isObj {
					prev := m.Current()
					m.Select(obj)
					return m.currentState(prev)
				}
				return input.Value, nil
			},
		})
	}
	return tools
}

func (m *MCP) IsDone() bool {
	return len(m.env.outputsByName) == 0 || m.returned
}

func (m *MCP) toolToID(tool LLMTool, args any) (*call.ID, error) {
	name := tool.Name
	props, ok := tool.Schema["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("malformed tool properties: expected %T, got %T",
			props,
			tool.Schema["properties"])
	}
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("malformed args: expected %T, got %T", argsMap, args)
	}
	var callArgs []*call.Argument
	for k, v := range argsMap {
		var lit call.Literal
		var litVal = v

		spec, found := props[k].(map[string]any)
		if !found {
			return nil, fmt.Errorf("unknown arg: %q", k)
		}

		if idType, ok := spec[jsonSchemaIDAttr].(string); ok {
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("expected string value, got %T", v)
			}
			obj, err := m.GetObject(str, idType)
			if err != nil {
				// drop it, maybe it's an invalid value
			} else {
				// show the real ID in telemetry
				litVal = obj.ID()
			}
		}

		pattern, found := spec["pattern"]
		if found {
			// if we have a pattern configured:
			//   1) validate the arg value as a sanity check, and
			//   2) if it's an ID pattern, map the value back to the real ID

			patternStr, ok := pattern.(string)
			if !ok {
				return nil, fmt.Errorf("expected string regex pattern, got %T", pattern)
			}
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("expected string value, got %T", v)
			}
			re, err := regexp.Compile(patternStr)
			if err != nil {
				return nil, fmt.Errorf("invalid regex for %q: %w", k, err)
			}
			if !re.MatchString(str) {
				return nil, fmt.Errorf("arg %q does not match pattern %s: %q", k, re.String(), str)
			}
		}

		lit, err := call.ToLiteral(litVal)
		if err != nil {
			return nil, err
		}
		callArgs = append(callArgs, call.NewArgument(k, lit, false))
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
	), nil
}

func fieldArgsToJSONSchema(schema *ast.Schema, field *ast.FieldDefinition) (map[string]any, error) {
	jsonSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	properties := jsonSchema["properties"].(map[string]any)
	required := []string{}
	for _, arg := range field.Arguments {
		argSchema, err := typeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}

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
		jsonSchema["required"] = required
	}
	return jsonSchema, nil
}

func typeToJSONSchema(schema *ast.Schema, t *ast.Type) (map[string]any, error) {
	jsonSchema := map[string]any{}

	// Handle lists
	if t.Elem != nil {
		jsonSchema["type"] = "array"
		items, err := typeToJSONSchema(schema, t.Elem)
		if err != nil {
			return nil, fmt.Errorf("elem type: %w", err)
		}
		jsonSchema["items"] = items
		return jsonSchema, nil
	}

	// Handle base types
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
		// For custom types, use string format with the type name
		typeDef, found := schema.Types[t.NamedType]
		if !found {
			return nil, fmt.Errorf("unknown type (impossible?): %q", t.NamedType)
		}
		desc := typeDef.Description
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
				jsonSchema["pattern"] = idPattern(typeName)
				jsonSchema[jsonSchemaIDAttr] = typeName
				desc = fmt.Sprintf("%s ID in \"%s#number\" format. %s", typeName, typeName, desc)
			} else {
				jsonSchema["type"] = "string"
			}
		default:
			return nil, fmt.Errorf("unhandled type: %s (%s)", t, typeDef.Kind)
		}
		jsonSchema["description"] = desc
	}

	return jsonSchema, nil
}

const jsonSchemaIDAttr = "x-id-type"

func idPattern(typeName string) string {
	return `^` + typeName + `#\d+$`
}

func (m *MCP) currentState(previous dagql.Object) (string, error) {
	cur := m.Current()
	res := map[string]any{
		// "selected" to hint to the model that it doesn't need to select it
		"selected": m.describe(cur),
	}
	if previous != nil {
		res["previous"] = m.describe(previous)
	}
	return toolStructuredResponse(res)
}

func toolStructuredResponse(val any) (string, error) {
	pl, err := json.Marshal(val)
	if err != nil {
		return "", fmt.Errorf("failed to encode response %T: %w", val, err)
	}
	return string(pl), nil
}

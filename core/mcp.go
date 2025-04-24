package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// A frontend for LLM tool calling
type LLMTool struct {
	// Tool name
	Name string `json:"name"`
	// Tool description
	Description string `json:"description"`
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any `json:"schema"`
	// Return type, used to hint to the model in tool lists - not exposed through
	// this field, since there's no such thing (I wish!)
	Returns *ast.Type `json:"-"`
	// Function implementing the tool.
	Call func(context.Context, any) (any, error) `json:"-"`
}

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	env *Env
	// Only show these functions, if non-empty
	selectedTools map[string]bool
	// The last value returned by a function.
	lastResult dagql.Typed
	// Indicates that the model has returned
	returned bool
}

func newMCP(env *Env) *MCP {
	return &MCP{
		env:           env,
		selectedTools: map[string]bool{},
	}
}

//go:embed llm_dagger_prompt.md
var defaultSystemPrompt string

func (m *MCP) DefaultSystemPrompt() string {
	return defaultSystemPrompt
}

func (m *MCP) Clone() *MCP {
	cp := *m
	cp.env = cp.env.Clone()
	cp.selectedTools = cloneMap(cp.selectedTools)
	cp.returned = false
	return &cp
}

func (m *MCP) Returned() bool {
	return m.returned
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
			objType := obj.Type().Name()
			if expectedType != "" && objType != expectedType {
				return nil, fmt.Errorf("type error: expected %q, got %q", expectedType, objType)
			}
			return obj, nil
		}
		return nil, fmt.Errorf("type error: %q exists but is not an object", key)
	}
	return nil, fmt.Errorf("unknown object %q", key)
}

func (m *MCP) LastResult() dagql.Typed {
	return m.lastResult
}

func (m *MCP) Tools(srv *dagql.Server) ([]LLMTool, error) {
	allTools, err := m.tools(srv)
	if err != nil {
		return nil, err
	}
	builtins, err := m.Builtins(srv, allTools)
	if err != nil {
		return nil, err
	}
	var tools []LLMTool
	for _, tool := range allTools {
		if m.selectedTools[tool.Name] {
			tools = append(tools, tool)
		}
	}
	return append(tools, builtins...), nil
}

// ToolFunc reuses our regular GraphQL args handling sugar for tools.
func ToolFunc[T any](srv *dagql.Server, fn func(context.Context, T) (any, error)) func(context.Context, any) (any, error) {
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
		for _, spec := range specs.Inputs(srv.View) {
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
		if err := specs.Decode(inputs, &t, srv.View); err != nil {
			return nil, err
		}
		return fn(ctx, t)
	}
}

func (m *MCP) tools(srv *dagql.Server) ([]LLMTool, error) {
	schema := srv.Schema()
	tools := []LLMTool{}
	typeNames := m.env.Types()
	if m.env.Root() != nil {
		typeNames = append(typeNames, schema.Query.Name)
	}
	for _, typeName := range typeNames {
		typeTools, err := m.typeTools(srv, schema, typeName)
		if err != nil {
			return nil, err
		}
		tools = append(tools, typeTools...)
	}
	return tools, nil
}

func (m *MCP) typeTools(srv *dagql.Server, schema *ast.Schema, typeName string) ([]LLMTool, error) {
	typeDef, ok := schema.Types[typeName]
	if !ok {
		return nil, fmt.Errorf("type %q not found", typeName)
	}
	var tools []LLMTool
	for _, field := range typeDef.Fields {
		if _, found := m.env.Input(field.Name); found {
			// If field conflicts with user input, user input wins
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
				"currentFunctionCall",
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
		if field.Directives.ForName(deprecatedDirectiveName) != nil {
			// don't expose deprecated APIs
			continue
		}
		if references(field, TypesHiddenFromEnvExtensions...) {
			// references a banned type
			continue
		}
		toolSchema, err := m.fieldArgsToJSONSchema(schema, typeName, field)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		var toolName string
		if typeName == schema.Query.Name {
			toolName = field.Name
		} else {
			toolName = typeDef.Name + "_" + strcase.ToSnake(field.Name)
		}
		tools = append(tools, LLMTool{
			Name:        toolName,
			Returns:     field.Type,
			Description: fmt.Sprintf("%s\n\nReturn type: %s", field.Description, field.Type),
			Schema:      toolSchema,
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				return m.call(ctx, srv, toolSchema, schema, typeName, field, args)
			},
		})
	}
	return tools, nil
}

func references(fieldDef *ast.FieldDefinition, types ...dagql.Typed) bool {
	names := map[string]bool{}
	for _, t := range types {
		names[t.Type().Name()] = true
	}
	if names[fieldDef.Type.Name()] {
		return true
	}
	for _, arg := range fieldDef.Arguments {
		if names[arg.Type.Name()] {
			return true
		}
	}
	return false
}

func displayArgs(args any) string {
	switch args := args.(type) {
	case map[string]any:
		var sb strings.Builder
		sb.WriteString("(")
		argList := make([]string, 0, len(args))
		for key, value := range args {
			argList = append(argList, fmt.Sprintf("%s: %v", key, value))
		}
		sort.Strings(argList)
		sb.WriteString(strings.Join(argList, ", "))
		sb.WriteString(")")
		return sb.String()
	default:
		return fmt.Sprintf(" %v", args)
	}
}

// Low-level function call plumbing
func (m *MCP) call(ctx context.Context,
	srv *dagql.Server,
	toolSchema map[string]any,
	schema *ast.Schema,
	selfType string,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	// The arguments to the call. Example: {"args": ["go", "build"], "redirectStderr", "/dev/null"}
	args any,
) (res string, rerr error) {
	var validated bool
	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s%s", fieldDef.Name, displayArgs(args)),
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

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer func() {
		fmt.Fprintln(stdio.Stdout, res)
		stdio.Close()
	}()

	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	//
	argsMap, ok := args.(map[string]any)
	if !ok {
		return "", fmt.Errorf("expected arguments to be a map - got %#v", args)
	}
	toolProps := toolSchema["properties"].(map[string]any)
	var target dagql.Object
	if m.env.Root() != nil && selfType == schema.Query.Name {
		target = m.env.Root()
	} else {
		self, ok := argsMap[selfType]
		if !ok {
			// default to the newest object of this type
			self = fmt.Sprintf("%s#%d", selfType, m.env.typeCounts[selfType])
		}
		recv, ok := self.(string)
		if !ok {
			return "", fmt.Errorf("expected %q to be a string - got %#v", selfType, recv)
		}
		// don't pass 'self' along to future arg validation
		delete(argsMap, selfType)
		var err error
		target, err = m.GetObject(recv, selfType)
		if err != nil {
			return "", err
		}
		if target.ObjectType().TypeName() != selfType {
			return "", fmt.Errorf("expected %q to be a %q - got %q", selfType, selfType, target.ObjectType().TypeName())
		}
	}
	fieldSel, err := m.toolCallToSelection(srv, target, fieldDef, argsMap, toolProps)
	if err != nil {
		return "", fmt.Errorf("failed to convert call inputs: %w", err)
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
) (string, error) {
	sels := []dagql.Selector{fieldSel}

	if retObjType, ok := srv.ObjectType(fieldDef.Type.NamedType); ok {
		if sync, ok := retObjType.FieldSpec("sync", srv.View); ok {
			// If the Object supports "sync", auto-select it.
			//
			syncSel := dagql.Selector{
				Field: sync.Name,
			}
			sels = append(sels, syncSel)
		}
	} else if fieldDef.Type.Elem != nil {
		if _, isObj := srv.ObjectType(fieldDef.Type.Elem.NamedType); isObj {
			// Handle arrays of objects by ingesting each object ID.
			//
			var objs []dagql.Object
			if err := srv.Select(ctx, target, &objs, fieldSel); err != nil {
				return "", fmt.Errorf("failed to sync: %w", err)
			}
			var res []any
			for _, obj := range objs {
				res = append(res, m.env.Ingest(obj, ""))
			}
			return toolStructuredResponse(map[string]any{
				"objects": res,
			})
		}
	}

	// Make the DagQL call.
	var val dagql.Typed
	if err := srv.Select(ctx, target, &val, sels...); err != nil {
		return "", fmt.Errorf("failed to sync: %w", err)
	}

	if id, ok := dagql.UnwrapAs[dagql.IDType](val); ok {
		// Handle ID results by turning them back into Objects, since these are
		// typically implementation details hinting to SDKs to unlazy the call.
		//
		syncedObj, err := srv.Load(ctx, id.ID())
		if err != nil {
			return "", fmt.Errorf("failed to load synced object: %w", err)
		}
		val = syncedObj
	}

	m.lastResult = val

	if obj, ok := dagql.UnwrapAs[dagql.Object](val); ok {
		// Handle object returns by switching to them.
		return m.newState(obj)
	}

	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		// Handle strings by guarding against non-utf8 or giant payloads.
		//
		bytes := []byte(str.String())
		if !utf8.Valid(bytes) {
			return toolStructuredResponse(map[string]any{
				"size":   len(bytes),
				"digest": digest.FromBytes(bytes),
			})
		}
		// Return string content directly, without wrapping it in JSON.
		return str.String(), nil
	}

	// Handle scalars or arrays of scalars.
	//
	return toolStructuredResponse(map[string]any{
		"result": val,
	})
}

func (m *MCP) toolCallToSelection(
	srv *dagql.Server,
	target dagql.Object,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	argsMap map[string]any,
	propsSchema map[string]any,
) (dagql.Selector, error) {
	sel := dagql.Selector{
		Field: fieldDef.Name,
	}
	targetObjType := target.ObjectType()
	field, ok := targetObjType.FieldSpec(fieldDef.Name, dagql.View(engine.Version))
	if !ok {
		return sel, fmt.Errorf("field %q not found in object type %q",
			fieldDef.Name,
			targetObjType.TypeName())
	}
	var unknownArgs error
	for _, arg := range field.Args.Inputs(srv.View) {
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
		}
		argSchema, ok := propsSchema[arg.Name].(map[string]any)
		if !ok {
			unknownArgs = errors.Join(unknownArgs, fmt.Errorf("unknown arg: %q", arg.Name))
			continue
		}
		if idType, ok := argSchema[jsonSchemaIDAttr].(string); ok {
			idStr, ok := val.(string)
			if !ok {
				return sel, fmt.Errorf("arg %q: expected string, got %T", arg.Name, val)
			}
			envVal, err := m.GetObject(idStr, idType)
			if err != nil {
				return sel, fmt.Errorf("arg %q: %w", arg.Name, err)
			}
			obj, ok := dagql.UnwrapAs[dagql.Object](envVal)
			if !ok {
				return sel, fmt.Errorf("arg %q: expected object, got %T", arg.Name, envVal)
			}
			enc, err := obj.ID().Encode()
			if err != nil {
				return sel, err
			}
			val = enc
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return sel, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	if unknownArgs != nil {
		return sel, unknownArgs
	}
	return sel, nil
}

func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall LLMToolCall) (string, bool) {
	var tool *LLMTool
	for _, t := range tools {
		if t.Name == toolCall.Function.Name {
			tool = &t
			break
		}
	}

	if tool == nil {
		res, err := toolStructuredResponse(map[string]any{
			"error": fmt.Sprintf("Tool '%s' is not available.", toolCall.Function.Name),
		})
		if err != nil {
			return fmt.Sprintf("marshal error: %v", err), false
		}
		return res, true
	}

	result, err := tool.Call(ctx, toolCall.Function.Arguments)
	if err != nil {
		return toolErrorMessage(err), true
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

func toolErrorMessage(err error) string {
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
	return errResponse
}

func (m *MCP) allIDs(typeName string) []string {
	total := m.env.typeCounts[typeName]
	ids := make([]string, 0, total)
	for i := total; i > 0; i-- {
		ids = append(ids, fmt.Sprintf("%s#%d", typeName, i))
	}
	return ids
}

func (m *MCP) returnBuiltin() (LLMTool, bool) {
	if len(m.env.outputsByName) == 0 {
		// no outputs desired
		return LLMTool{}, false
	}

	props := map[string]any{}
	required := []string{}

	desc := "Save your work, making the requested outputs available to the user:\n"

	var outputs []string
	for name, b := range m.env.outputsByName {
		required = append(required, name)

		typeName := b.ExpectedType

		outputs = append(outputs,
			fmt.Sprintf("- %s (%s): %s", name, typeName, b.Description))

		argSchema := map[string]any{
			"type": "string",
		}

		var desc string
		if typeName == "String" {
			desc = b.Description
		} else {
			argSchema[jsonSchemaIDAttr] = typeName
			desc = fmt.Sprintf("(%s ID) %s", typeName, b.Description)
		}

		argSchema["description"] = desc

		props[name] = argSchema
	}

	// append outputs
	sort.Strings(outputs)
	for _, out := range outputs {
		desc += fmt.Sprintf("\n%s", out)
	}

	return LLMTool{
		Name:        "save",
		Description: desc,
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
			for name, output := range m.env.outputsByName {
				arg, ok := vals[name]
				if !ok {
					return nil, fmt.Errorf("required output %s not provided", name)
				}
				argStr, ok := arg.(string)
				if !ok {
					return nil, fmt.Errorf("invalid type for argument %s: %T", name, arg)
				}
				if output.ExpectedType == "String" {
					output.Value = dagql.String(argStr)
				} else {
					bnd, ok := m.env.objsByID[argStr]
					if !ok {
						return nil, fmt.Errorf("object not found for argument %s: %s", name, argStr)
					}

					obj := bnd.Value
					actualType := obj.Type().Name()
					if output.ExpectedType != actualType {
						return nil, fmt.Errorf("incompatible types: %s must be %s, got %s", name, output.ExpectedType, actualType)
					}

					// Propagate description from output to binding so that outputs are
					// described under `Available objects:`
					bnd.Description = output.Description
					output.Value = obj
				}
			}
			m.returned = true
			return "ok", nil
		},
	}, true
}

func (m *MCP) Builtins(srv *dagql.Server, typeTools []LLMTool) ([]LLMTool, error) {
	schema := srv.Schema()

	builtins := []LLMTool{
		{
			Name:        "think",
			Description: `A tool for thinking through problems, brainstorming ideas, or planning without executing any actions`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thought": map[string]any{
						"type":        "string",
						"description": "Your thoughts.",
					},
				},
				"required": []string{"thought"},
			},
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Thought string
			}) (_ any, rerr error) {
				ctx, span := Tracer(ctx).Start(ctx, "think",
					telemetry.Reveal(),
					trace.WithAttributes(
						attribute.String(telemetry.UIMessageAttr, "received"),
						attribute.String(telemetry.UIActorEmojiAttr, "💭"),
					),
				)
				defer telemetry.End(span, func() error { return rerr })
				stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
					log.String(telemetry.ContentTypeAttr, "text/markdown"))
				defer stdio.Close()
				fmt.Fprint(stdio.Stdout, args.Thought)
				return "Finished thinking.", nil
			}),
		},
	}

	if m.env.writable {
		allTypes := map[string]dagql.Type{
			"String":  dagql.String(""),
			"Int":     dagql.Int(0),
			"Float":   dagql.Float(0.0),
			"Boolean": dagql.Boolean(false),
		}
		for name := range schema.Types {
			objectType, ok := srv.ObjectType(name)
			if !ok {
				continue
			}
			if slices.ContainsFunc(TypesHiddenFromEnvExtensions, func(t dagql.Typed) bool {
				return t.Type().Name() == name
			}) {
				continue
			}
			allTypes[name] = objectType
		}
		builtins = append(builtins, LLMTool{
			Name:        "declare_output",
			Description: "Declare a new output that can have a value saved to it",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The name of the output, following shell naming conventions.",
						"format":      "[a-z][a-z0-9_]*",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "The type of the output.",
						"enum":        slices.Sorted(maps.Keys(allTypes)),
					},
					"description": map[string]any{
						"type":        "string",
						"description": "A description of the output.",
					},
				},
				"required": []string{"name", "description"},
			},
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Name        string
				Type        string
				Description string
			}) (any, error) {
				return "done", m.env.DeclareOutput(args.Name, allTypes[args.Type], args.Description)
			}),
		})
	}

	builtins = append(builtins, LLMTool{
		Name:        "list_objects",
		Description: "List all available objects.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "List objects of a particular type.",
				},
			},
		},
		Call: ToolFunc(func(ctx context.Context, args struct {
			Type string `default:""`
		}) (any, error) {
			desc := "Available objects:"
			var objects []string
			for _, typeName := range slices.Sorted(maps.Keys(m.env.typeCounts)) {
				if args.Type != "" && args.Type != typeName {
					continue
				}
				count := m.env.typeCounts[typeName]
				for i := 1; i <= count; i++ {
					bnd := m.env.objsByID[fmt.Sprintf("%s#%d", typeName, i)]
					objects = append(objects, fmt.Sprintf("%s: %s", bnd.ID(), bnd.Description))
				}
			}
			if len(objects) > 0 {
				for _, input := range objects {
					desc += fmt.Sprintf("\n- %s", input)
				}
			} else {
				desc += "\n- No objects available."
			}
			return desc, nil
		}),
	})

	if len(typeTools) > 0 {
		builtins = append(builtins, LLMTool{
			Name: "select_tools",
			Description: (func() string {
				desc := `Select tools for interacting with the available objects.`
				desc += "\n\nAvailable tools:"
				for _, tool := range typeTools {
					if m.selectedTools[tool.Name] {
						// already have it
						continue
					}
					desc += "\n- " + tool.Name + " (returns " + tool.Returns.String() + ")"
				}
				return desc
			})(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tools": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "The tools to select.",
					},
				},
				"required": []string{"tools"},
			},
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Tools []string `json:"tools"`
			}) (any, error) {
				toolCounts := make(map[string]int)
				for _, tool := range args.Tools {
					toolCounts[tool]++
				}
				// perform a sanity check; some LLMs will do silly things like request
				// the same tool 3 times when told to call it 3 times
				for tool, count := range toolCounts {
					if count > 1 {
						return "", fmt.Errorf("tool %s selected more than once (%d times)", tool, count)
					}
				}
				var selectedTools []LLMTool
				var unknownTools []string
				for tool := range toolCounts {
					var foundTool LLMTool
					for _, t := range typeTools {
						if t.Name == tool {
							foundTool = t
							break
						}
					}
					if foundTool.Name == "" {
						unknownTools = append(unknownTools, tool)
					} else {
						m.selectedTools[tool] = true
						selectedTools = append(selectedTools, foundTool)
					}
				}
				sort.Slice(selectedTools, func(i, j int) bool {
					return selectedTools[i].Name < selectedTools[j].Name
				})
				res := map[string]any{
					"added_tools": selectedTools,
				}
				if len(unknownTools) > 0 {
					res["unknown_tools"] = unknownTools
				}
				return toolStructuredResponse(res)
			}),
		}, LLMTool{
			Name:        "chain_tools",
			Description: `Invoke multiple tool calls sequentially, passing the result of one call as the receiver of the next`,
			// Description: "Run a batch of chained tool calls, passing the result of one call as the receiver of the next",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"calls": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"tool": map[string]any{
									"type":        "string",
									"description": "The name of the tool to call.",
								},
								"params": map[string]any{
									"type":                 "object",
									"description":          "The parameters to pass to the tool.",
									"additionalProperties": true,
								},
							},
							"required": []string{"tool", "params"},
						},
						"description": "The tools to select.",
					},
				},
				"required": []string{"calls"},
			},
			Call: func(ctx context.Context, argsAny any) (_ any, rerr error) {
				var args struct {
					Calls []ChainedCall `json:"calls"`
				}
				pl, err := json.Marshal(argsAny)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(pl, &args); err != nil {
					return nil, err
				}
				if err := m.validateChain(args.Calls, typeTools, schema); err != nil {
					return nil, err
				}
				var res any
				for i, call := range args.Calls {
					var tool LLMTool
					if call.Tool == "" {
						return nil, errors.New("tool name cannot be empty")
					}
					for _, t := range typeTools {
						if t.Name == call.Tool {
							tool = t
							break
						}
					}
					if tool.Name == "" {
						return nil, fmt.Errorf("tool not found: %q", call.Tool)
					}
					if call.Params == nil {
						call.Params = make(map[string]any)
					}
					args := cloneMap(call.Params)
					if i > 0 {
						if obj, ok := dagql.UnwrapAs[dagql.Object](m.LastResult()); ok {
							lastType := obj.Type().Name()
							// override, since the whole point is to chain from the previous
							// value; any value here is surely mistaken or hallucinated
							args[lastType] = m.env.Ingest(obj, "")
						}
					}
					res, err = tool.Call(ctx, args)
					if err != nil {
						return nil, fmt.Errorf("call %q: %w", call.Tool, err)
					}
				}
				return res, nil
			},
		})
	}

	if returnTool, ok := m.returnBuiltin(); ok {
		builtins = append(builtins, returnTool)
	}

	builtins = append(builtins, m.userProvidedValues()...)

	// Attach builtin telemetry
	for i, builtin := range builtins {
		if builtin.Name == "think" {
			// has its own custom telemetry
			continue
		}
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
					attribute.String(telemetry.DagCallAttr, callAttr),
					attribute.String(telemetry.DagCallScopeAttr, "llm"),
				)
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

func (m *MCP) userProvidedValues() []LLMTool {
	desc := "The following values have been provided by the user:"
	var anyProvided bool
	for _, input := range m.env.Inputs() {
		anyProvided = true
		desc += "\n\n---"
		description := input.Description
		if description == "" {
			description = input.Key
		}
		desc += "\n\n" + description + "\n\n"
		if obj, isObj := input.AsObject(); isObj {
			desc += m.env.Ingest(obj, input.Description)
		} else if payload, err := json.Marshal(input.Value); err == nil {
			desc += string(payload)
		} else {
			desc += "MARSHAL ERROR: " + err.Error()
		}
	}
	desc += "\n\nNOTE: This tool does nothing but provide this description. You don't need to call it."
	if !anyProvided {
		return nil
	}
	return []LLMTool{
		{
			Name:        "user_provided_values",
			Description: desc,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"strict":               true,
				"required":             []string{},
				"additionalProperties": false,
			},
			Call: func(ctx context.Context, args any) (any, error) {
				return desc, nil
			},
		},
	}
}

type ChainedCall struct {
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

func (m *MCP) validateChain(calls []ChainedCall, typeTools []LLMTool, schema *ast.Schema) error {
	if len(calls) == 0 {
		return errors.New("no tools called")
	}
	var returnedType *ast.Type
	for _, call := range calls {
		if call.Tool == "" {
			return fmt.Errorf("tool name cannot be empty")
		}
		var tool LLMTool
		for _, t := range typeTools {
			if t.Name == call.Tool {
				tool = t
				break
			}
		}
		if tool.Name == "" {
			return fmt.Errorf("unknown tool: %q", call.Tool)
		}
		if returnedType != nil {
			if returnedType.Elem != nil {
				return fmt.Errorf("cannot chain %q call from array result", tool.Name)
			}
			typeDef, found := schema.Types[returnedType.Name()]
			if !found {
				return fmt.Errorf("unknown type: %q", returnedType.Name())
			}
			if typeDef.Kind != ast.Object {
				return fmt.Errorf("cannot chain %q call from non-Object type: %q (%s)", tool.Name, returnedType.Name(), typeDef.Kind)
			}
			props := tool.Schema["properties"].(map[string]any)
			if _, found := props[typeDef.Name]; !found {
				return fmt.Errorf("tool %q does not chain from type %q", tool.Name, typeDef.Name)
			}
		}
		returnedType = tool.Returns
	}
	return nil
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

func (m *MCP) fieldArgsToJSONSchema(schema *ast.Schema, typeName string, field *ast.FieldDefinition) (map[string]any, error) {
	jsonSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	properties := jsonSchema["properties"].(map[string]any)
	required := []string{}
	if typeName != schema.Query.Name {
		schema := map[string]any{
			"type":        "string",
			"description": fmt.Sprintf("The %s to operate against, e.g. Potato#123.", typeName),
		}
		properties[typeName] = schema

		// mark this required, as a hint. lots of models forget to pass it, so we
		// tolerate that too, but our schema should suggest the right mental model.
		required = append(required, typeName)
	}
	for _, arg := range field.Arguments {
		argSchema, err := m.typeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}

		// Add description
		desc := arg.Description
		if idType, ok := argSchema[jsonSchemaIDAttr]; ok {
			// If it's an object ID, be sure to mention the type. JSON schema doesn't
			// help here since they're all type 'string'.
			if desc == "" {
				desc = fmt.Sprintf("(%s ID)", idType)
			} else {
				desc = fmt.Sprintf("(%s ID) %s", idType, desc)
			}
		}
		argSchema["description"] = desc

		// Add default value if present
		if arg.DefaultValue != nil {
			val, err := arg.DefaultValue.Value(nil)
			if err != nil {
				return nil, fmt.Errorf("default value: %w", err)
			}
			argSchema["default"] = val
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

func (m *MCP) typeToJSONSchema(schema *ast.Schema, t *ast.Type) (map[string]any, error) {
	jsonSchema := map[string]any{}

	// Handle lists
	if t.Elem != nil {
		jsonSchema["type"] = "array"
		items, err := m.typeToJSONSchema(schema, t.Elem)
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
		switch typeDef.Kind {
		case ast.InputObject:
			jsonSchema["type"] = "object"
			properties := map[string]any{}
			for _, f := range typeDef.Fields {
				fieldSpec, err := m.typeToJSONSchema(schema, f.Type)
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

const jsonSchemaIDAttr = "x-id-type"

func (m *MCP) newState(target dagql.Object) (string, error) {
	return m.env.Ingest(target, ""), nil
}

func toolStructuredResponse(val any) (string, error) {
	str := new(strings.Builder)
	enc := json.NewEncoder(str)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(val); err != nil {
		return "", fmt.Errorf("failed to encode response %T: %w", val, err)
	}
	return str.String(), nil
}

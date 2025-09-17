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
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
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
	// Whether the tool schema is strict.
	// https://platform.openai.com/docs/guides/structured-outputs?api-mode=chat
	Strict bool `json:"-"`
	// GraphQL API field that this tool corresponds to
	Field *ast.FieldDefinition `json:"-"`
	// Function implementing the tool.
	Call func(context.Context, any) (any, error) `json:"-"`
}

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	env *Env
	// Only show these functions, if non-empty
	selectedMethods map[string]bool
	// The last value returned by a function.
	lastResult dagql.AnyResult
	// Indicates that the model has returned
	returned bool
}

func newMCP(env *Env) *MCP {
	return &MCP{
		env:             env,
		selectedMethods: map[string]bool{},
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
	cp.selectedMethods = maps.Clone(cp.selectedMethods)
	cp.returned = false
	return &cp
}

func (m *MCP) Returned() bool {
	return m.returned
}

// Get an object saved at a given key
func (m *MCP) GetObject(key, expectedType string) (dagql.AnyObjectResult, error) {
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

func (m *MCP) LastResult() dagql.AnyResult {
	return m.lastResult
}

func (m *MCP) Tools() ([]LLMTool, error) {
	allTools := map[string]LLMTool{}
	if err := m.allTypeTools(m.env.srv, allTools); err != nil {
		return nil, err
	}
	return m.Builtins(m.env.srv, allTools)
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

func (m *MCP) allTypeTools(srv *dagql.Server, allTools map[string]LLMTool) error {
	schema := srv.Schema()
	typeNames := m.env.Types()
	if m.env.IsPrivileged() {
		typeNames = append(typeNames, schema.Query.Name)
	}
	for _, typeName := range typeNames {
		typeDef, ok := schema.Types[typeName]
		if !ok {
			return fmt.Errorf("type %q not found", typeName)
		}
		if err := m.typeTools(allTools, srv, schema, typeDef); err != nil {
			return fmt.Errorf("load %q tools: %w", typeName, err)
		}
	}
	return nil
}

func (m *MCP) typeTools(allTools map[string]LLMTool, srv *dagql.Server, schema *ast.Schema, typeDef *ast.Definition) error {
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
		switch typeDef.Name {
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
		toolSchema, err := m.fieldArgsToJSONSchema(schema, field)
		if err != nil {
			return fmt.Errorf("field %q: %w", field.Name, err)
		}
		var toolName string
		if typeDef.Name == schema.Query.Name {
			toolName = field.Name
		} else {
			toolName = typeDef.Name + "." + field.Name
		}
		allTools[toolName] = LLMTool{
			Name:        toolName,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      toolSchema,
			Strict:      false, // unused
			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				return m.call(ctx, srv, toolSchema, schema, typeDef.Name, field, args)
			},
		}
	}
	return nil
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
	var target dagql.AnyObjectResult
	if m.env.IsPrivileged() && selfType == schema.Query.Name {
		target = srv.Root()
	} else {
		self, ok := argsMap["self"]
		if !ok {
			// default to the newest object of this type
			self = fmt.Sprintf("%s#%d", selfType, m.env.typeCounts[selfType])
		}
		recv, ok := self.(string)
		if !ok {
			return "", fmt.Errorf("expected 'self' to be a string - got %#v", self)
		}
		// don't pass 'self' along to future arg validation
		delete(argsMap, "self")
		var err error
		target, err = m.GetObject(recv, selfType)
		if err != nil {
			return "", err
		}
		if target.ObjectType().TypeName() != selfType {
			return "", fmt.Errorf("expected %q to be a %q - got %q", recv, selfType, target.ObjectType().TypeName())
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
	target dagql.AnyObjectResult,
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
			var objs []dagql.AnyResult
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
	var val dagql.AnyResult
	if err := srv.Select(
		// reveal cache hits, even if we've already seen them within the session
		dagql.WithRepeatedTelemetry(ctx),
		target,
		&val,
		sels...,
	); err != nil {
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

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// Handle object returns
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

	// Handle null response.
	if val == nil {
		return toolStructuredResponse(map[string]any{
			"result": nil,
		})
	}

	// Handle scalars or arrays of scalars.
	//
	return toolStructuredResponse(map[string]any{
		"result": val.Unwrap(),
	})
}

func (m *MCP) toolCallToSelection(
	srv *dagql.Server,
	target dagql.AnyObjectResult,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	argsMap map[string]any,
	propsSchema map[string]any,
) (dagql.Selector, error) {
	sel := dagql.Selector{
		Field: fieldDef.Name,
	}
	targetObjType := target.ObjectType()
	field, ok := targetObjType.FieldSpec(fieldDef.Name, call.View(engine.Version))
	if !ok {
		return sel, fmt.Errorf("field %q not found in object type %q",
			fieldDef.Name,
			targetObjType.TypeName())
	}
	remainingArgs := make(map[string]any)
	maps.Copy(remainingArgs, argsMap)
	for _, arg := range field.Args.Inputs(srv.View) {
		if arg.Internal {
			continue // skip internal args
		}
		val, ok := argsMap[arg.Name]
		if !ok {
			continue
		}
		delete(remainingArgs, arg.Name)
		argSchema, ok := propsSchema[arg.Name].(map[string]any)
		if !ok {
			return sel, fmt.Errorf("arg %q: missing from schema", arg.Name)
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
			obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](envVal)
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
	if len(remainingArgs) > 0 {
		return sel, fmt.Errorf("unknown args: %v", remainingArgs)
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
			"required":             required,
			"additionalProperties": false,
		},
		Strict: true,
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

//nolint:gocyclo
func (m *MCP) Builtins(srv *dagql.Server, allMethods map[string]LLMTool) ([]LLMTool, error) {
	schema := srv.Schema()

	builtins := []LLMTool{}

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
						"description": "The name of the output, following shell naming conventions ([a-z][a-z0-9_]*).",
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
				"required":             []string{"name", "type", "description"},
				"additionalProperties": false,
			},
			Strict: true,
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Name        string
				Type        string
				Description string
			}) (any, error) {
				if err := m.env.DeclareOutput(args.Name, allTypes[args.Type], args.Description); err != nil {
					return nil, err
				}
				return "Output '%s' declared successfully.", nil
			}),
		})
	}

	builtins = append(builtins, LLMTool{
		Name:        "list_objects",
		Description: "List available objects.",
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
		Call: ToolFunc(srv, func(ctx context.Context, args struct{}) (any, error) {
			type objDesc struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			}
			var objects []objDesc
			for _, typeName := range slices.Sorted(maps.Keys(m.env.typeCounts)) {
				count := m.env.typeCounts[typeName]
				for i := 1; i <= count; i++ {
					bnd := m.env.objsByID[fmt.Sprintf("%s#%d", typeName, i)]
					objects = append(objects, objDesc{
						ID:          bnd.ID(),
						Description: bnd.Description,
					})
				}
			}
			return toolStructuredResponse(objects)
		}),
	})

	builtins = append(builtins, LLMTool{
		Name:        "list_methods",
		Description: "List the methods that can be selected.",
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
		Call: ToolFunc(srv, func(ctx context.Context, args struct{}) (any, error) {
			type toolDesc struct {
				Name         string            `json:"name"`
				Returns      string            `json:"returns"`
				RequiredArgs map[string]string `json:"required_args,omitempty"`
			}
			var methods []toolDesc
			for _, method := range allMethods {
				reqArgs := map[string]string{}
				var returns string
				if method.Field != nil {
					returns = method.Field.Type.String()
					for _, arg := range method.Field.Arguments {
						if arg.DefaultValue != nil || !arg.Type.NonNull {
							// optional
							continue
						}
						reqArgs[arg.Name] = arg.Type.String()
					}
				}
				methods = append(methods, toolDesc{
					Name:         method.Name,
					RequiredArgs: reqArgs,
					Returns:      returns,
				})
			}
			sort.Slice(methods, func(i, j int) bool {
				return methods[i].Name < methods[j].Name
			})
			return toolStructuredResponse(methods)
		}),
	})

	if len(allMethods) > 0 {
		builtins = append(builtins, LLMTool{
			Name:        "select_methods",
			Description: "Select methods for interacting with the available objects. Never guess - only select methods previously returned by list_methods.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"methods": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":        "string",
							"description": "The name of the method to select, as seen in list_methods.",
						},
						"description": "The methods to select.",
					},
				},
				"required":             []string{"methods"},
				"additionalProperties": false,
			},
			Strict: true,
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Methods []string
			}) (any, error) {
				methodCounts := make(map[string]int)
				for _, toolName := range args.Methods {
					methodCounts[toolName]++
				}
				// perform a sanity check; some LLMs will do silly things like request
				// the same tool 3 times when told to call it 3 times
				for tool, count := range methodCounts {
					if count > 1 {
						return "", fmt.Errorf("tool %s selected more than once (%d times)", tool, count)
					}
				}
				type methodDef struct {
					Name        string         `json:"name"`
					Returns     string         `json:"returns,omitempty"`
					Description string         `json:"description"`
					Schema      map[string]any `json:"argsSchema"`
				}
				var selectedMethods []methodDef
				var unknownMethods []string
				for methodName := range methodCounts {
					method, found := allMethods[methodName]
					if found {
						var returns string
						if method.Field != nil {
							returns = method.Field.Type.String()
						}
						selectedMethods = append(selectedMethods, methodDef{
							Name:        method.Name,
							Returns:     returns,
							Description: method.Description,
							Schema:      method.Schema,
						})
					} else {
						unknownMethods = append(unknownMethods, methodName)
					}
				}
				if len(unknownMethods) > 0 {
					return nil, fmt.Errorf("unknown methods: %v; use list_methods first", unknownMethods)
				}
				for _, method := range selectedMethods {
					m.selectedMethods[method.Name] = true
				}
				sort.Slice(selectedMethods, func(i, j int) bool {
					return selectedMethods[i].Name < selectedMethods[j].Name
				})
				res := map[string]any{
					"added_methods": selectedMethods,
				}
				if len(unknownMethods) > 0 {
					res["unknown_methods"] = unknownMethods
				}
				return toolStructuredResponse(res)
			}),
		})

		builtins = append(builtins, LLMTool{
			Name:        "call_method",
			Description: "Call a method on an object. Methods must be selected with `select_methods` before calling them. Self represents the object to call the method on, and args specify any additional parameters to pass.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method": map[string]any{
						"type":        "string",
						"description": "The name of the method to call.",
					},
					"self": map[string]any{
						"type":        []string{"string", "null"},
						"description": "The object to call the method on. Not specified for top-level methods.",
					},
					"args": map[string]any{
						"type":                 []string{"object", "null"},
						"description":          "The arguments to pass to the method.",
						"additionalProperties": true,
					},
				},
				"required":             []string{"method", "self", "args"},
				"additionalProperties": false,
			},
			Strict: false,
			Call: func(ctx context.Context, argsAny any) (_ any, rerr error) {
				var call struct {
					Self   string         `json:"self"`
					Method string         `json:"method"`
					Args   map[string]any `json:"args"`
				}
				pl, err := json.Marshal(argsAny)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(pl, &call); err != nil {
					return nil, err
				}
				if call.Args == nil {
					call.Args = make(map[string]any)
				}
				// Add self parameter to the method call
				if call.Self != "" {
					call.Args["self"] = call.Self
					matches := idRegex.FindStringSubmatch(call.Self)
					if matches == nil {
						return nil, fmt.Errorf("invalid ID format: %q", call.Self)
					}
					typeName := matches[idRegex.SubexpIndex("type")]
					if !strings.Contains(call.Method, ".") {
						// allow omitting the TypeName. prefix, which models are more prone
						// to guessing
						call.Method = fmt.Sprintf("%s.%s", typeName, call.Method)
					}
				}
				var method LLMTool
				method, found := allMethods[call.Method]
				if !found {
					return nil, fmt.Errorf("method not defined: %q; use list_methods first", call.Method)
				}
				if !m.selectedMethods[call.Method] {
					return nil, fmt.Errorf("method not selected: %q; use select_methods first", call.Method)
				}
				return method.Call(ctx, call.Args)
			},
		}, LLMTool{
			Name: "chain_methods",
			Description: `Invoke multiple methods sequentially, passing the result of one method as the receiver of the next

NOTE: you must select methods before chaining them`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"self": map[string]any{
						"type":        []string{"string", "null"},
						"description": "The object to call the method on. Not specified for top-level methods.",
					},
					"chain": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"method": map[string]any{
									"type":        "string",
									"description": "The name of the method to call.",
								},
								"args": map[string]any{
									"type":                 "object",
									"description":          "The arguments to pass to the method.",
									"additionalProperties": true,
								},
							},
							"required": []string{"method", "args"},
						},
						"description": "The chain of method calls.",
					},
				},
				"required":             []string{"chain", "self"},
				"additionalProperties": false,
			},
			Strict: false,
			Call: func(ctx context.Context, argsAny any) (_ any, rerr error) {
				var toolArgs struct {
					Self  string        `json:"self"`
					Chain []ChainedCall `json:"chain"`
				}
				pl, err := json.Marshal(argsAny)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(pl, &toolArgs); err != nil {
					return nil, err
				}
				if err := m.validateAndNormalizeChain(toolArgs.Self, toolArgs.Chain, allMethods, schema); err != nil {
					return nil, err
				}
				var res any
				for i, call := range toolArgs.Chain {
					var tool LLMTool
					tool, found := allMethods[call.Method]
					if !found {
						return nil, fmt.Errorf("tool not found: %q", call.Method)
					}
					if call.Args == nil {
						call.Args = make(map[string]any)
					}
					args := maps.Clone(call.Args)
					if i > 0 {
						if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](m.LastResult()); ok {
							// override, since the whole point is to chain from the previous
							// value; any value here is surely mistaken or hallucinated
							args["self"] = m.env.Ingest(obj, "")
						}
					} else {
						args["self"] = toolArgs.Self
					}
					res, err = tool.Call(ctx, args)
					if err != nil {
						return nil, fmt.Errorf("call %q: %w", call.Method, err)
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
		builtins[i].Call = func(ctx context.Context, args any) (_ any, rerr error) {
			attrs := []attribute.KeyValue{
				attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
			}
			if builtin.Name == "call_method" || builtin.Name == "chain_methods" {
				attrs = append(attrs, attribute.Bool(telemetry.UIPassthroughAttr, true))
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
			ctx, span := Tracer(ctx).Start(ctx,
				fmt.Sprintf("%s %v", builtin.Name, args),
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

type ChainedCall struct {
	Method string         `json:"method"`
	Args   map[string]any `json:"args"`
}

func (m *MCP) validateAndNormalizeChain(self string, calls []ChainedCall, allMethods map[string]LLMTool, schema *ast.Schema) error {
	if len(calls) == 0 {
		return errors.New("no methods called")
	}
	var currentType *ast.Type
	if self != "" {
		obj, err := m.GetObject(self, "")
		if err != nil {
			return err
		}
		currentType = obj.Type()
	}
	var errs error
	for i, call := range calls {
		if call.Method == "" {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: method name cannot be empty", i))
			continue
		}
		if !strings.Contains(call.Method, ".") && currentType != nil {
			// add type prefix to method name
			call.Method = currentType.Name() + "." + call.Method
			calls[i] = call
		}
		method, found := allMethods[call.Method]
		if !found {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: unknown method: %q", i, call.Method))
			continue
		}
		if !m.selectedMethods[method.Name] {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: method %q is not selected", i, method.Name))
		}
		if currentType != nil {
			if currentType.Elem != nil {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: cannot chain %q call from array result", i, method.Name))
				continue
			}
			typeDef, found := schema.Types[currentType.Name()]
			if !found {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: unknown type: %q", i, currentType.Name()))
				continue
			}
			if typeDef.Kind != ast.Object {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: cannot chain %q call from non-Object type: %q (%s)", i, method.Name, currentType.Name(), typeDef.Kind))
			}
		}
		currentType = method.Field.Type
	}
	return errs
}

func (m *MCP) userProvidedValues() []LLMTool {
	inputs := m.env.Inputs()
	if len(inputs) == 0 {
		return nil
	}
	desc := "The following values have been provided by the user:"
	type valueDesc struct {
		Description string `json:"description"`
		Value       any    `json:"value"`
	}
	var values []valueDesc
	for _, input := range inputs {
		description := input.Description
		if description == "" {
			description = input.Key
		}
		if obj, isObj := input.AsObject(); isObj {
			values = append(values, valueDesc{
				Value:       m.env.Ingest(obj, input.Description),
				Description: description,
			})
		} else {
			values = append(values, valueDesc{
				Value:       input.Value,
				Description: description,
			})
		}
	}
	formatted, err := toolStructuredResponse(values)
	if err != nil {
		return nil
	}
	desc += "\n\n" + formatted
	desc += "\n\nNOTE: This tool does nothing but provide this description. You don't need to call it."
	return []LLMTool{
		{
			Name:        "user_provided_values",
			Description: desc,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"required":             []string{},
				"additionalProperties": false,
			},
			Strict: true,
			Call: func(ctx context.Context, args any) (any, error) {
				return desc, nil
			},
		},
	}
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
		if v == nil {
			// explicitly passing null for required arg (maybe due to strict mode); omit
			continue
		}

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
	sort.Slice(callArgs, func(i, j int) bool {
		return callArgs[i].Name() < callArgs[j].Name()
	})
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

var idRegex = regexp.MustCompile(`^(?P<type>[A-Z]\w*)#(?P<nth>\d+)$`)

func (m *MCP) fieldArgsToJSONSchema(schema *ast.Schema, field *ast.FieldDefinition) (map[string]any, error) {
	jsonSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	properties := jsonSchema["properties"].(map[string]any)
	required := []string{}
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

func (m *MCP) newState(target dagql.AnyObjectResult) (string, error) {
	typeName := target.Type().Name()
	_, known := m.env.typeCounts[typeName]
	res := map[string]any{
		"result": m.env.Ingest(target, ""),
	}
	if !known {
		res["hint"] = fmt.Sprintf("New methods available for type %q.", typeName)
	}
	return toolStructuredResponse(res)
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

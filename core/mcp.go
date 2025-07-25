package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/multiprefixw"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// Hide functions from the largest and most commonly used core types, to prevent
// tool bloat
var defaultBlockedMethods = map[string][]string{
	"Query": {
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
		"version",
	},
	"Container": {
		"build",
		"defaultArgs",
		"entrypoint",
		"envVariable",
		"envVariables",
		"experimentalWithAllGPUs",
		"experimentalWithGPU",
		"export",
		"exportImage",
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
		"workdir",
	},
	"Directory": {
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
		"withoutFiles",
	},
	"File": {
		"export",
		"withName",
		"withTimestamps",
	},
}

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
	env dagql.ObjectResult[*Env]
	// Only show these functions, if non-empty
	selectedMethods map[string]bool
	// Never show these functions, grouped by type
	blockedMethods map[string][]string
	// The last value returned by a function.
	lastResult dagql.Typed
	// Indicates that the model has returned
	returned bool
	// The last log ID that we've seen
	lastLogID int64
	// Saved objects by ID (Foo#123)
	objsByID map[string]*Binding
	// Auto incrementing number per-type
	typeCounts map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
}

func newMCP(env dagql.ObjectResult[*Env]) *MCP {
	blocked := maps.Clone(defaultBlockedMethods)
	for typeName, methods := range blocked {
		blocked[typeName] = slices.Clone(methods)
	}
	return &MCP{
		env:             env,
		selectedMethods: map[string]bool{},
		blockedMethods:  blocked,
		objsByID:        map[string]*Binding{},
		typeCounts:      map[string]int{},
		idByHash:        map[digest.Digest]string{},
	}
}

//go:embed llm_dagger_prompt.md
var defaultSystemPrompt string

func (m *MCP) DefaultSystemPrompt() string {
	return defaultSystemPrompt
}

func (m *MCP) Clone() *MCP {
	cp := *m
	cp.selectedMethods = maps.Clone(cp.selectedMethods)
	cp.blockedMethods = maps.Clone(cp.blockedMethods)
	for typeName, methods := range cp.blockedMethods {
		cp.blockedMethods[typeName] = slices.Clone(methods)
	}
	cp.objsByID = maps.Clone(cp.objsByID)
	cp.typeCounts = maps.Clone(cp.typeCounts)
	cp.idByHash = maps.Clone(cp.idByHash)
	cp.returned = false
	return &cp
}

// Lookup an input binding
func (m *MCP) Input(key string) (*Binding, bool) {
	// next check for values by ID
	if val, exists := m.objsByID[key]; exists {
		return val, true
	}
	return m.env.Self().Input(key)
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
	if b, exists := m.Input(key); exists {
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

func (m *MCP) Server(ctx context.Context) (*dagql.Server, error) {
	return m.env.Self().deps.Schema(ctx)
}

func (m *MCP) Tools(ctx context.Context) ([]LLMTool, error) {
	srv, err := m.Server(ctx)
	if err != nil {
		return nil, err
	}
	schema := srv.Schema()
	objectMethods := map[string]LLMTool{}
	if err := m.reachableObjectMethods(srv, objectMethods); err != nil {
		return nil, err
	}
	moduleTools := map[string]LLMTool{}
	for _, mod := range m.env.Self().installedModules {
		modTypeName := strcase.ToCamel(mod.Name())
		modTypeDef := schema.Types[modTypeName]
		for _, obj := range mod.ObjectDefs {
			def := obj.AsObject.Value
			if strcase.ToCamel(def.Name) != modTypeName {
				// we're only concerned with the entrypoint object
				continue
			}
			var hasRequiredArgs bool
			if def.Constructor.Valid {
				for _, arg := range def.Constructor.Value.Args {
					if !arg.TypeDef.Optional && arg.DefaultPath == "" {
						hasRequiredArgs = true
						break
					}
				}
			}
			if hasRequiredArgs {
				// FIXME: better error
				return nil, fmt.Errorf("module %s constructor has required arguments", mod.Name())
			}
			if err := m.typeTools(moduleTools, srv, schema, modTypeDef, def); err != nil {
				return nil, err
			}
		}
	}
	builtins, err := m.Builtins(srv, objectMethods)
	if err != nil {
		return nil, err
	}
	modTools := staticTools(moduleTools)
	allTools := append(modTools, builtins...)
	return allTools, nil
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

func (m *MCP) reachableObjectMethods(srv *dagql.Server, allTools map[string]LLMTool) error {
	schema := srv.Schema()
	typeNames := m.Types()
	if m.env.Self().IsPrivileged() {
		typeNames = append(typeNames, schema.Query.Name)
	}
	for _, typeName := range typeNames {
		typeDef, ok := schema.Types[typeName]
		if !ok {
			return fmt.Errorf("type %q not found", typeName)
		}
		if err := m.typeTools(allTools, srv, schema, typeDef, nil); err != nil {
			return fmt.Errorf("load %q tools: %w", typeName, err)
		}
	}
	return nil
}

func (m *MCP) typeTools(allTools map[string]LLMTool, srv *dagql.Server, schema *ast.Schema, typeDef *ast.Definition, autoConstruct *ObjectTypeDef) error {
	for _, field := range typeDef.Fields {
		if _, found := m.Input(field.Name); found {
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
		// Skip explicitly blocked methods
		if slices.Contains(m.blockedMethods[typeDef.Name], field.Name) {
			continue
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
		if typeDef.Name == schema.Query.Name ||
			(autoConstruct != nil && allTools[toolName].Name == "") {
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
				argsMap, ok := args.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid arguments type: %T", args)
				}
				return m.call(ctx, srv, toolSchema, schema, typeDef.Name, field, argsMap, autoConstruct)
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
	args map[string]any,
	// Whether the call should be made against a freshly constructed module
	autoConstruct *ObjectTypeDef,
) (res string, rerr error) {
	var validated bool
	var toolArgNames []string
	var toolArgValues []string
	for _, arg := range fieldDef.Arguments {
		val, ok := args[arg.Name].(string)
		if !ok {
			continue
		}
		toolArgNames = append(toolArgNames, arg.Name)
		toolArgValues = append(toolArgValues, val)
	}

	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s%s", fieldDef.Name, displayArgs(args)),
		trace.WithAttributes(
			attribute.String(telemetry.LLMToolAttr, fieldDef.Name),
			attribute.StringSlice(telemetry.LLMToolArgNamesAttr, toolArgNames),
			attribute.StringSlice(telemetry.LLMToolArgValuesAttr, toolArgValues),
		),
		telemetry.ActorEmoji("🤖"),
		telemetry.Reveal())

	defer telemetry.End(span, func() error {
		if rerr != nil && !validated {
			// only reveal for "plumbing" errors, not errors from the field, since
			// those will already be shown
			span.SetAttributes(attribute.Bool(telemetry.UIPassthroughAttr, false))
		}
		return rerr
	})

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.Bool(telemetry.LogsVerboseAttr, true))
	defer func() {
		fmt.Fprintln(stdio.Stdout, res)
		stdio.Close()
	}()

	// Capture logs produced by the tool call and append them to the result
	defer func() {
		logs, err := m.captureLogs(ctx)
		if err != nil {
			if res != "" {
				res += "\n\n"
			}
			res += fmt.Sprintf("WARNING: Failed to capture logs (%v)", err)
		} else if len(logs) > 0 {
			content := strings.Join(logs, "\n")
			if len(content) > llmLogsMaxChars {
				skipped := len(content) - llmLogsMaxChars
				content = content[skipped:]
				content = fmt.Sprintf("[truncated %d characters]\n", skipped) + content
			}
			if res != "" {
				if !strings.HasSuffix(res, "\n") {
					// avoid double trailing linebreak (e.g. pretty JSON)
					res += "\n"
				}
				res += "\n"
			}
			res += fmt.Sprintf("<logs>\n%s\n</logs>",
				// avoid double trailing linebreak
				strings.TrimSuffix(content, "\n"),
			)
		}
	}()

	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	//
	toolProps := toolSchema["properties"].(map[string]any)
	var target dagql.AnyObjectResult
	if m.env.Self().IsPrivileged() && selfType == schema.Query.Name {
		target = srv.Root()
	} else if autoConstruct != nil {
		def := autoConstruct.Constructor
		consArgs := []dagql.NamedInput{}
		if def.Valid {
			for _, arg := range def.Value.Args {
				if arg.DefaultPath != "" {
					dir := m.env.Self().Hostfs
					switch path.Clean(arg.DefaultPath) {
					case ".", "/":
					default:
						if err := srv.Select(ctx, dir, &dir, dagql.Selector{
							Field: "directory", // TODO: handle files too
							Args: []dagql.NamedInput{
								{
									Name:  "path",
									Value: dagql.String(arg.DefaultPath),
								},
							},
						}); err != nil {
							return "", err
						}
					}
					consArgs = append(consArgs, dagql.NamedInput{
						Name: arg.Name,
						// NOTE: these are always nullable??? i guess??? cause it has a default/???
						Value: dagql.Opt(dagql.NewID[*Directory](dir.ID())),
					})
				}
			}
		}
		if err := srv.Select(ctx, srv.Root(), &target, dagql.Selector{
			Field: gqlFieldName(selfType),
			Args:  consArgs,
		}); err != nil {
			return "", err
		}
	} else {
		self, ok := args["self"]
		if !ok {
			// default to the newest object of this type
			self = fmt.Sprintf("%s#%d", selfType, m.typeCounts[selfType])
		}
		recv, ok := self.(string)
		if !ok {
			return "", fmt.Errorf("expected 'self' to be a string - got %#v", self)
		}
		// don't pass 'self' along to future arg validation
		delete(args, "self")
		var err error
		target, err = m.GetObject(recv, selfType)
		if err != nil {
			return "", err
		}
		if target.ObjectType().TypeName() != selfType {
			return "", fmt.Errorf("expected %q to be a %q - got %q", recv, selfType, target.ObjectType().TypeName())
		}
	}
	fieldSel, err := m.toolCallToSelection(srv, target, fieldDef, args, toolProps)
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
				return "", err
			}
			var res []any
			var displays []string
			for _, obj := range objs {
				res = append(res, m.Ingest(obj, ""))
				if displayer, ok := dagql.UnwrapAs[LLMDisplayer](obj); ok {
					displays = append(displays, displayer.LLMDisplay())
				}
			}
			if len(displays) > 0 {
				return toolStructuredResponse(map[string]any{
					"objects":   res,
					"summaries": displays,
				})
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

	m.lastResult = val

	res, err := m.outputToLLM(ctx, srv, val)
	if err != nil {
		return "", err
	}
	return res, nil
}

func (m *MCP) outputToLLM(ctx context.Context, srv *dagql.Server, val dagql.Typed) (string, error) {
	if id, ok := dagql.UnwrapAs[dagql.IDType](val); ok {
		// Handle ID results by turning them back into Objects, since these are
		// typically implementation details hinting to SDKs to unlazy the call.
		syncedObj, err := srv.Load(ctx, id.ID())
		if err != nil {
			return "", fmt.Errorf("failed to load synced object: %w", err)
		}
		val = syncedObj
	}

	if newEnv, ok := dagql.UnwrapAs[dagql.ObjectResult[*Env]](val); ok {
		// Hide this unless it fails, since it's just noise
		ctx, span := Tracer(ctx).Start(ctx, "examine environment",
			telemetry.Encapsulated())
		oldFS := m.env.Self().Hostfs
		newFS := newEnv.Self().Hostfs
		var entries []string
		err := srv.Select(ctx, newFS, &entries, dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{
					Name:  "other",
					Value: dagql.NewID[*Directory](oldFS.ID()),
				},
			},
		}, dagql.Selector{
			Field: "glob",
			Args: []dagql.NamedInput{
				{
					Name:  "pattern",
					Value: dagql.NewString("**/*"),
				},
			},
		})
		defer telemetry.End(span, func() error { return err })
		if err != nil {
			return "", fmt.Errorf("failed to diff filesystems: %w", err)
		}
		msg := "Environment updated."
		// remove directories
		entries = slices.DeleteFunc(entries, func(s string) bool {
			return strings.HasSuffix(s, "/")
		})
		if len(entries) > 0 {
			msg += "\n\nFiles changed:\n- " + strings.Join(entries, "\n- ")
		}
		// Swap out the Env for the updated one
		m.env = newEnv
		return msg, nil
	}

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// Handle object returns
		return m.newState(ctx, srv, obj)
	}

	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		var res []any
		var displays []string
		for i := 1; i <= list.Len(); i++ {
			// Handle arrays of objects by ingesting each object ID.
			val, err := list.Nth(i)
			if err != nil {
				return "", fmt.Errorf("failed to get ID for object %d: %w", i, err)
			}
			if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
				// Refer to objects by their IDs
				res = append(res, m.Ingest(obj, ""))
			} else if resT, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
				// Unwrap any Result[T]s
				res = append(res, resT.Unwrap())
			} else {
				// Not really sure what else there would be, but...
				res = append(res, val)
			}
			if displayer, ok := dagql.UnwrapAs[LLMDisplayer](val); ok {
				displays = append(displays, displayer.LLMDisplay())
			}
		}
		if len(displays) > 0 {
			return toolStructuredResponse(map[string]any{
				"results":   res,
				"summaries": displays,
			})
		}
		return toolStructuredResponse(map[string]any{
			"results": res,
		})
	}

	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		// Handle strings by guarding against non-utf8 or giant payloads.
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

	if val == nil {
		// Handle null response.
		return toolStructuredResponse(map[string]any{
			"result": nil,
		})
	}

	if anyRes, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
		// Unwrap any Result[T]s
		val = anyRes.Unwrap()
	}

	if val == (Void{}) {
		// TODO: should there be a message here?
		return "", nil
	}

	// Handle scalars or arrays of scalars.
	return toolStructuredResponse(map[string]any{
		"result": val,
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
	field, ok := targetObjType.FieldSpec(fieldDef.Name, dagql.View(engine.Version))
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

const llmLastLogs = 10
const llmLogsMaxChars = 30000

func (m *MCP) BlockFunction(ctx context.Context, typeName, funcName string) error {
	srv, err := m.Server(ctx)
	if err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	obj, ok := srv.ObjectType(typeName)
	if !ok {
		return fmt.Errorf("object type %q not found", typeName)
	}
	_, ok = obj.FieldSpec(funcName, srv.View)
	if !ok {
		return fmt.Errorf("function %q not found on type %q", funcName, typeName)
	}
	m.blockedMethods[typeName] = append(m.blockedMethods[typeName], funcName)
	return nil
}

func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall LLMToolCall) (res string, failed bool) {
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

	result, err := tool.Call(EnvToContext(ctx, m.env), toolCall.Function.Arguments)
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

const logsBatchSize = 1000

// captureLogs returns nicely Heroku-formatted lines of all logs seen since the
// last capture.
func (m *MCP) captureLogs(ctx context.Context) ([]string, error) {
	root, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, close, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, err
	}
	defer close()

	spanCtx := trace.SpanContextFromContext(ctx)

	formattedLines := new(strings.Builder)
	mpw := multiprefixw.New(formattedLines)

	for {
		logs, err := q.SelectLogsBeneathSpan(ctx, clientdb.SelectLogsBeneathSpanParams{
			ID:     m.lastLogID,
			SpanID: sql.NullString{Valid: true, String: spanCtx.SpanID().String()},
			Limit:  logsBatchSize,
		})
		if err != nil {
			return nil, err
		}
		if len(logs) == 0 {
			break
		}

		for _, log := range logs {
			m.lastLogID = log.ID

			var logAttrs []*otlpcommonv1.KeyValue
			if err := clientdb.UnmarshalProtoJSONs(log.Attributes, &otlpcommonv1.KeyValue{}, &logAttrs); err != nil {
				slog.Warn("failed to unmarshal log attributes", "error", err)
				continue
			}
			var skip bool
			for _, attr := range logAttrs {
				if attr.Key == telemetry.StdioEOFAttr || attr.Key == telemetry.LogsVerboseAttr {
					if attr.Value.GetBoolValue() {
						skip = true
						break
					}
				}
			}
			if skip {
				// don't generate a line for EOF events
				continue
			}

			var prefix string
			if log.SpanID.Valid {
				span, err := q.SelectSpan(ctx, clientdb.SelectSpanParams{
					TraceID: log.TraceID.String,
					SpanID:  log.SpanID.String,
				})
				if err != nil {
					return nil, err
				}
				var spanAttrs []*otlpcommonv1.KeyValue
				if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &spanAttrs); err != nil {
					slog.Warn("failed to unmarshal span attributes", "error", err)
					continue
				}
				var isNoise bool
				for _, attr := range spanAttrs {
					if attr.Key == telemetry.LLMRoleAttr || attr.Key == telemetry.LLMToolAttr {
						isNoise = true
						break
					}
				}
				if isNoise {
					// don't show logs from the LLM spans themselves
					continue
				}
				prefix = span.Name
				spanBytes, err := hex.DecodeString(span.SpanID)
				if err != nil {
					slog.Warn("failed to decode hex span ID", "error", err)
					continue
				}
				base64Span := base64.StdEncoding.EncodeToString(spanBytes)
				base64Links, err := q.SelectLinkedSpans(ctx, base64Span)
				if err != nil {
					return nil, err
				}
				var linkSuffix string
				// linked spans get added to the name (Container.withExec => exec foo bar)
				for _, link := range base64Links {
					linkedSpan, err := q.SelectSpan(ctx, clientdb.SelectSpanParams{
						TraceID: link.TraceID,
						SpanID:  link.SourceSpanID,
					})
					if err != nil {
						slog.Warn("failed to query linked span", "error", err)
						continue
					}
					linkSuffix = "(" + linkedSpan.Name + ")"
				}
				prefix += linkSuffix
				prefix += ":\n"
			}

			mpw.SetPrefix(prefix)

			var bodyPb otlpcommonv1.AnyValue
			if err := proto.Unmarshal(log.Body, &bodyPb); err != nil {
				slog.Warn("failed to unmarshal log body", "error", err, "client", mainMeta.ClientID, "log", log.ID)
				continue
			}
			switch x := bodyPb.GetValue().(type) {
			case *otlpcommonv1.AnyValue_StringValue:
				fmt.Fprint(mpw, x.StringValue)
			case *otlpcommonv1.AnyValue_BytesValue:
				mpw.Write(x.BytesValue)
			default:
				// default to something troubleshootable
				fmt.Fprintf(mpw, "UNHANDLED: %+v", x)
			}
		}
	}
	if formattedLines.Len() == 0 {
		return nil, nil
	}
	return strings.Split(formattedLines.String(), "\n"), nil
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
	if len(m.env.Self().outputsByName) == 0 {
		// no outputs desired
		return LLMTool{}, false
	}

	props := map[string]any{}
	required := []string{}

	desc := "Save your work, making the requested outputs available to the user:\n"

	var outputs []string
	for name, b := range m.env.Self().outputsByName {
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
			for name, output := range m.env.Self().outputsByName {
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
					bnd, ok := m.objsByID[argStr]
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

	if m.env.Self().writable {
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
				var dest dagql.ObjectResult[*Env]
				err := srv.Select(ctx, m.env, &dest, dagql.Selector{
					Field: "with" + args.Type + "Output",
					Args: []dagql.NamedInput{
						{
							Name:  "name",
							Value: dagql.String(args.Name),
						},
						{
							Name:  "description",
							Value: dagql.String(args.Description),
						},
					},
				})
				if err != nil {
					return nil, err
				}
				return dest, nil
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
			for _, typeName := range slices.Sorted(maps.Keys(m.typeCounts)) {
				count := m.typeCounts[typeName]
				for i := 1; i <= count; i++ {
					bnd := m.objsByID[fmt.Sprintf("%s#%d", typeName, i)]
					objects = append(objects, objDesc{
						ID:          bnd.ID(),
						Description: bnd.Description,
					})
				}
			}
			return toolStructuredResponse(objects)
		}),
	})

	if m.env.Self().staticTools {
	} else {
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
								args["self"] = m.Ingest(obj, "")
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
	}

	if returnTool, ok := m.returnBuiltin(); ok {
		builtins = append(builtins, returnTool)
	}

	builtins = append(builtins, m.userProvidedValues()...)

	// Attach builtin telemetry
	for i, builtin := range builtins {
		builtins[i].Call = func(ctx context.Context, args any) (_ any, rerr error) {
			argsMap, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid arguments type")
			}
			var toolArgNames []string
			var toolArgValues []string
			if requiredArgs, ok := builtin.Schema["required"].([]string); ok {
				for _, arg := range requiredArgs {
					val, ok := argsMap[arg]
					if !ok {
						continue
					}
					if str, ok := val.(string); ok {
						toolArgNames = append(toolArgNames, arg)
						toolArgValues = append(toolArgValues, str)
					}
				}
			}
			attrs := []attribute.KeyValue{
				attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
				attribute.String(telemetry.LLMToolAttr, builtin.Name),
				attribute.StringSlice(telemetry.LLMToolArgNamesAttr, toolArgNames),
				attribute.StringSlice(telemetry.LLMToolArgValuesAttr, toolArgValues),
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
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
				log.Bool(telemetry.LogsVerboseAttr, true))
			defer stdio.Close()
			res, err := builtin.Call(ctx, args)
			if err != nil {
				return nil, err
			}
			fmt.Fprintln(stdio.Stdout, res)
			return res, nil
		}
	}

	if m.env.Self().staticTools {
		builtins = append(builtins, staticTools(allMethods)...)
	}

	return builtins, nil
}

func staticTools(allMethods map[string]LLMTool) []LLMTool {
	staticTools := slices.Collect(maps.Values(allMethods))
	for i := range staticTools {
		// sanitize tool names; Foo.bar is more intuitive for call_method form
		staticTools[i].Name = regexp.MustCompile(`[^a-zA-Z0-9_-]`).
			ReplaceAllString(staticTools[i].Name, "_")
	}
	sort.Slice(staticTools, func(i, j int) bool {
		return staticTools[i].Name < staticTools[j].Name
	})
	return staticTools
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
	inputs := m.env.Self().Inputs()
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
				Value:       m.Ingest(obj, input.Description),
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
	return len(m.env.Self().outputsByName) == 0 || m.returned
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

func (m *MCP) newState(ctx context.Context, srv *dagql.Server, target dagql.AnyObjectResult) (string, error) {
	schema := srv.Schema()
	typeName := target.Type().Name()
	_, known := m.typeCounts[typeName]
	res := map[string]any{
		"result": m.Ingest(target, ""),
	}
	if displayer, ok := dagql.UnwrapAs[LLMDisplayer](target); ok {
		res["display"] = displayer.LLMDisplay()
	}
	data := map[string]any{}
	for _, field := range schema.Types[typeName].Fields {
		trivial := field.Directives.ForName(trivialFieldDirectiveName) != nil
		if !trivial {
			continue
		}
		val, err := target.Select(ctx, srv, dagql.Selector{
			Field: field.Name,
		})
		if err != nil {
			return "", err
		}
		var datum any
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
			datum = m.Ingest(obj, "")
		} else {
			// TODO: lists of objects?
			datum = val.Unwrap()
		}
		data[field.Name] = datum
	}
	if len(data) > 0 {
		res["data"] = data
	}
	if !known {
		res["hint"] = fmt.Sprintf("New methods available for type %q.", typeName)
	}
	return toolStructuredResponse(res)
}

func (m *MCP) Ingest(obj dagql.AnyResult, desc string) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	typeName := id.Type().NamedType()
	llmID, ok := m.idByHash[hash]
	if !ok {
		m.typeCounts[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, m.typeCounts[typeName])
		if desc == "" {
			desc = m.describe(obj.ID())
		}
		m.idByHash[hash] = llmID
		m.objsByID[llmID] = &Binding{
			Key:          llmID,
			Value:        obj,
			Description:  desc,
			ExpectedType: obj.Type().Name(),
		}
	}
	return llmID
}

func (m *MCP) describe(id *call.ID) string {
	str := new(strings.Builder)
	if recv := id.Receiver(); recv != nil {
		if llmID, ok := m.idByHash[recv.Digest()]; ok {
			str.WriteString(llmID)
		} else {
			str.WriteString(recv.Digest().String())
		}
		str.WriteString(".")
	}
	str.WriteString(id.Field())

	// Include arguments in the description
	if args := id.Args(); len(args) > 0 {
		str.WriteString("(")
		for i, arg := range args {
			if i > 0 {
				str.WriteString(", ")
			}
			str.WriteString(arg.Name())
			str.WriteString(": ")
			str.WriteString(m.displayLit(arg.Value()))
		}
		str.WriteString(")")
	}
	return str.String()
}

func (m *MCP) displayLit(lit call.Literal) string {
	switch x := lit.(type) {
	case *call.LiteralID:
		// For ID arguments, try to use LLM IDs
		if llmID, ok := m.idByHash[x.Value().Digest()]; ok {
			return llmID
		} else {
			return x.Value().Type().NamedType()
		}
	case *call.LiteralList:
		list := "["
		_ = x.Range(func(i int, value call.Literal) error {
			if i > 0 {
				list += ","
			}
			list += m.displayLit(value)
			return nil
		})
		list += "]"
		return list
	case *call.LiteralObject:
		obj := "{"
		_ = x.Range(func(i int, name string, value call.Literal) error {
			if i > 0 {
				obj += ","
			}
			obj += name + ": " + m.displayLit(value)
			return nil
		})
		obj += "}"
		return obj
	default:
		return lit.Display()
	}
}

func (m *MCP) Types() []string {
	types := make([]string, 0, len(m.typeCounts))
	for typ := range m.typeCounts {
		types = append(types, typ)
	}
	return types
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

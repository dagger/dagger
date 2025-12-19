package core

import (
	"context"
	"database/sql"
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
	"sync"
	"unicode/utf8"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/prompts"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/iancoleman/strcase"
	"github.com/jedevc/diffparser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// A frontend for LLM tool calling
type LLMTool struct {
	// Tool name
	Name string `json:"name"`
	// MCP server name providing the tool, if any
	Server string
	// Tool description
	Description string `json:"description"`
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any `json:"schema"`
	// Whether the tool schema is strict.
	// https://platform.openai.com/docs/guides/structured-outputs?api-mode=chat
	Strict bool `json:"-"`
	// Whether we should hide the LLM tool call span in favor of just showing its
	// child spans.
	HideSelf bool `json:"-"`
	// Whether the tool is read-only (from MCP ReadOnlyHint annotation)
	ReadOnly bool `json:"-"`
	// GraphQL API field that this tool corresponds to
	Field *ast.FieldDefinition `json:"-"`
	// Function implementing the tool.
	Call LLMToolFunc `json:"-"`
}

type LLMToolFunc = func(context.Context, any) (any, error)

type LLMToolSet = dagui.OrderedSet[string, LLMTool]

func NewLLMToolSet() *LLMToolSet {
	return dagui.NewOrderedSet[string, LLMTool](func(t LLMTool) string {
		return t.Name
	})
}

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	env dagql.ObjectResult[*Env]
	// Expose a static toolset for calling methods, rather than directly exposing
	// a dynamic set of methods as tools
	staticTools bool
	// Only show these functions, if non-empty
	selectedMethods map[string]bool
	// Never show these functions, grouped by type
	blockedMethods map[string][]string
	// The last value returned by a function.
	lastResult dagql.Typed
	// Indicates that the model has returned
	returned bool
	// Saved objects by ID (Foo#123)
	objsByID map[string]contextualBinding
	// Auto incrementing number per-type
	typeCounts map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
	// Configured MCP servers.
	mcpServers map[string]*MCPServerConfig
	// Persistent MCP sessions.
	mcpSessions map[string]*mcp.ClientSession
	// Synchronize any concurrent tool call results.
	mu *sync.Mutex
}

// MCPServerConfig represents configuration for an external MCP server
type MCPServerConfig struct {
	// Name of the MCP server
	Name string

	// Command to run the MCP server
	Service dagql.ObjectResult[*Service]
}

func (srv *MCPServerConfig) Dial(ctx context.Context) (_ *mcp.ClientSession, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "start mcp server: "+srv.Name, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	return mcp.NewClient(&mcp.Implementation{
		Title:   "Dagger",
		Version: engine.Version,
	}, nil).Connect(ctx, &ServiceMCPTransport{
		Service: srv.Service,
	}, nil)
}

type contextualBinding func(context.Context, dagql.ObjectResult[*Env]) (*Binding, error)

func newMCP(env dagql.ObjectResult[*Env]) *MCP {
	blocked := maps.Clone(defaultBlockedMethods)
	for typeName, methods := range blocked {
		blocked[typeName] = slices.Clone(methods)
	}
	return &MCP{
		env:             env,
		selectedMethods: map[string]bool{},
		blockedMethods:  blocked,
		objsByID:        map[string]contextualBinding{},
		typeCounts:      map[string]int{},
		idByHash:        map[digest.Digest]string{},
		mcpServers:      make(map[string]*MCPServerConfig),
		mcpSessions:     map[string]*mcp.ClientSession{},
		mu:              &sync.Mutex{},
	}
}

func (m *MCP) DefaultSystemPrompt() string {
	env := m.env.Self()
	var promptFiles []string
	if len(env.inputsByName) > 0 ||
		env.privileged ||
		len(env.installedModules) > 0 {
		promptFiles = append(promptFiles, "basics.md")
	}
	if m.staticTools {
		promptFiles = append(promptFiles, "static.md")
	}
	if len(env.outputsByName) > 0 {
		promptFiles = append(promptFiles, "outputs.md")
	}
	if env.writable {
		promptFiles = append(promptFiles, "writable.md")
	}
	var prompt string
	for _, file := range promptFiles {
		content, err := prompts.FS.ReadFile(file)
		if err != nil {
			// this should be caught at dev time
			panic(err)
		}
		if len(prompt) > 0 {
			prompt += "\n"
		}
		prompt += string(content)
	}
	if !m.staticTools {
		if values, err := m.userProvidedValues(); err == nil && len(values) > 0 {
			if prompt != "" {
				prompt += "\n\n"
			}
			prompt += "## User-provided values\n\n"
			prompt += "The following values have been provided:\n\n"
			prompt += fmt.Sprintf("```\n%s\n```", values)
		}
	}
	return prompt
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
	cp.mcpServers = maps.Clone(cp.mcpServers)
	cp.mcpSessions = maps.Clone(cp.mcpSessions)
	cp.returned = false
	cp.mu = &sync.Mutex{}
	return &cp
}

// Lookup an input binding
func (m *MCP) Input(ctx context.Context, key string) (*Binding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if val, exists := m.objsByID[key]; exists {
		bnd, err := val(ctx, m.env)
		if err != nil {
			return nil, false, err
		}
		return bnd, true, nil
	}
	bnd, found := m.env.Self().Input(key)
	return bnd, found, nil
}

func (m *MCP) Returned() bool {
	return m.returned
}

// Get an object saved at a given key
func (m *MCP) GetObject(ctx context.Context, key, expectedType string) (dagql.AnyObjectResult, error) {
	if expectedType != "" {
		// for maximal LLM compatibility, assume type for numeric ID args
		if onlyNum, err := strconv.Atoi(key); err == nil {
			key = fmt.Sprintf("%s#%d", expectedType, onlyNum)
		}
	}
	b, exists, err := m.Input(ctx, key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("unknown object %q", key)
	}
	if obj, ok := b.AsObject(); ok {
		objType := obj.Type().Name()
		if expectedType != "" && objType != expectedType {
			return nil, fmt.Errorf("type error for %q: expected %q, got %q", key, expectedType, objType)
		}
		return obj, nil
	}
	return nil, fmt.Errorf("type error: %q exists but is not an object", key)
}

func (m *MCP) LastResult() dagql.Typed {
	return m.lastResult
}

func (m *MCP) Server(ctx context.Context) (*dagql.Server, error) {
	return m.env.Self().deps.Schema(ctx)
}

func (m *MCP) WithMCPServer(srv *MCPServerConfig) *MCP {
	m = m.Clone()
	m.mcpServers[srv.Name] = srv
	return m
}

func (m *MCP) Tools(ctx context.Context) ([]LLMTool, error) {
	srv, err := m.Server(ctx)
	if err != nil {
		return nil, err
	}

	allTools := NewLLMToolSet()
	if err := m.loadMCPTools(ctx, allTools); err != nil {
		return nil, err
	}
	if err := m.loadModuleTools(srv, allTools); err != nil {
		return nil, err
	}
	objectMethods := NewLLMToolSet()
	if err := m.loadReachableObjectMethods(srv, objectMethods); err != nil {
		return nil, err
	}
	if !m.staticTools {
		// directly expose object methods as a dynamic toolchain
		for _, t := range objectMethods.Order {
			allTools.Add(t)
		}
	}
	m.loadBuiltins(srv, allTools, objectMethods)
	return allTools.Order, nil
}

func (m *MCP) syncMCPSessions(ctx context.Context) error {
	stop := maps.Clone(m.mcpSessions)
	for _, mcpSrv := range m.mcpServers {
		delete(stop, mcpSrv.Name)
		if _, ok := m.mcpSessions[mcpSrv.Name]; ok {
			continue
		}
		sess, err := mcpSrv.Dial(ctx)
		if err != nil {
			return fmt.Errorf("dial mcp %q: %w", mcpSrv.Name, err)
		}
		m.mcpSessions[mcpSrv.Name] = sess
	}
	for name, srv := range stop {
		if err := srv.Close(); err != nil {
			return err
		}
		delete(m.mcpSessions, name)
	}
	return nil
}

func (m *MCP) loadMCPTools(ctx context.Context, allTools *LLMToolSet) error {
	if err := m.syncMCPSessions(ctx); err != nil {
		return err
	}
	for serverName, sess := range m.mcpSessions {
		for tool, err := range sess.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			schema, err := toAny(tool.InputSchema)
			if err != nil {
				return err
			}
			if schema["properties"] == nil {
				// OpenAI is very particular; it wants there to always be properties,
				// even if empty.
				schema["properties"] = map[string]any{}
			}

			// Check if the tool is read-only from MCP annotations
			isReadOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint

			allTools.Add(LLMTool{
				Name:        tool.Name,
				Server:      serverName,
				Description: tool.Description,
				Schema:      schema,
				ReadOnly:    isReadOnly,
				Call: func(ctx context.Context, args any) (any, error) {
					res, err := sess.CallTool(ctx, &mcp.CallToolParams{
						Name:      tool.Name,
						Arguments: args,
					})
					if err != nil {
						return nil, fmt.Errorf("call tool %q on mcp %q: %w", tool.Name, serverName, err)
					}

					var out string
					for _, content := range res.Content {
						switch x := content.(type) {
						case *mcp.TextContent:
							out += x.Text
						default:
							out += fmt.Sprintf("WARNING: unsupported content type %T", x)
						}
					}
					if res.StructuredContent != nil {
						str, err := toolStructuredResponse(res.StructuredContent)
						if err != nil {
							return nil, err
						}
						out += str
					}
					if res.IsError {
						return "", errors.New(out)
					}
					return out, nil
				},
			})
		}
	}
	return nil
}

func (m *MCP) updateEnvWorkspace(ctx context.Context, workspace dagql.ObjectResult[*Directory]) error {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("get dagql server: %w", err)
	}

	var newEnv dagql.ObjectResult[*Env]
	if err := srv.Select(ctx, m.env, &newEnv, dagql.Selector{
		View:  srv.View,
		Field: "withWorkspace",
		Args: []dagql.NamedInput{
			{
				Name:  "workspace",
				Value: dagql.NewID[*Directory](workspace.ID()),
			},
		},
	}); err != nil {
		return err
	}
	m.env = newEnv
	return nil
}

func (m *MCP) summarizePatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) (string, error) {
	var rawPatch string
	if err := srv.Select(ctx, changes, &rawPatch, dagql.Selector{
		View:  srv.View,
		Field: "asPatch",
	}, dagql.Selector{
		View:  srv.View,
		Field: "contents",
	}); err != nil {
		return fmt.Sprintf("WARNING: failed to fetch patch summary: %s", err), nil
	}
	if rawPatch == "" {
		// No changes; don't say anything, since saying "No changes" could be
		// confusing depending on other context (like logs from a `git show`)
		return "", nil
	}
	if strings.Count(rawPatch, "\n") > 100 {
		// If the patch is too large, show a summary instead
		var addedPaths, removedPaths []string
		if err := srv.Select(ctx, changes, &addedPaths, dagql.Selector{
			View:  srv.View,
			Field: "addedPaths",
		}); err != nil {
			return fmt.Sprintf("WARNING: failed to fetch added paths: %s", err), nil
		}
		if err := srv.Select(ctx, changes, &removedPaths, dagql.Selector{
			View:  srv.View,
			Field: "removedPaths",
		}); err != nil {
			return fmt.Sprintf("WARNING: failed to fetch removed paths: %s", err), nil
		}
		addedDirectories := slices.DeleteFunc(addedPaths, func(s string) bool {
			return !strings.HasSuffix(s, "/")
		})
		removedDirectories := slices.DeleteFunc(removedPaths, func(s string) bool {
			return !strings.HasSuffix(s, "/")
		})
		patch, err := diffparser.Parse(rawPatch)
		if err != nil {
			return "", fmt.Errorf("parse patch: %w", err)
		}
		preview := &idtui.PatchPreview{
			Patch:       patch,
			AddedDirs:   addedDirectories,
			RemovedDirs: removedDirectories,
		}
		var res strings.Builder
		llmOut := termenv.NewOutput(&res, termenv.WithProfile(termenv.Ascii))
		if err := preview.Summarize(llmOut, 80); err != nil {
			return fmt.Sprintf("WARNING: failed to render patch summary: %s", err), nil
		}
		return res.String(), nil
	}
	return rawPatch, nil
}

func toAny(v any) (res map[string]any, rerr error) {
	pl, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return res, json.Unmarshal(pl, &res)
}

func (m *MCP) loadModuleTools(srv *dagql.Server, allTools *LLMToolSet) error {
	schema := srv.Schema()
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
					if !arg.TypeDef.Optional && arg.DefaultPath == "" && arg.DefaultValue == nil {
						hasRequiredArgs = true
						break
					}
				}
			}
			if hasRequiredArgs {
				// FIXME: better error
				return fmt.Errorf("TODO: module %s constructor cannot have required arguments", mod.Name())
			}
			if err := m.typeTools(allTools, srv, schema, modTypeDef, def); err != nil {
				return err
			}
		}
	}
	return nil
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

func (m *MCP) loadReachableObjectMethods(srv *dagql.Server, allTools *LLMToolSet) error {
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

func (m *MCP) typeTools(allTools *LLMToolSet, srv *dagql.Server, schema *ast.Schema, typeDef *ast.Definition, autoConstruct *ObjectTypeDef) error {
	for _, field := range typeDef.Fields {
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
		// Check if this is a trivial field (field accessor with no logic)
		isTrivial := field.Directives.ForName(trivialFieldDirectiveName) != nil

		// Skip trivial fields that return scalars/non-objects since they're shown in toolObjectResponse
		if isTrivial {
			// But DO expose trivial fields that return objects, as these are field accessors
			// like sdk().rust() that the LLM needs to navigate the object graph
			fieldType := field.Type
			if fieldType.Elem != nil {
				// Skip arrays for now (too complex)
				continue
			}
			typeDef, isObject := schema.Types[fieldType.NamedType]
			if !isObject || typeDef.Kind != ast.Object {
				// Not an object type - skip it (scalars shown in toolObjectResponse)
				continue
			}
			// Fall through - this is an object-returning field accessor, expose it as a tool
		}
		if field.Directives.ForName(deprecatedDirectiveName) != nil {
			// don't expose deprecated APIs
			continue
		}
		if references(field, TypesHiddenFromEnvExtensions...) {
			// references a banned type
			continue
		}
		toolSchema, err := m.fieldArgsToJSONSchema(schema, typeDef, field, autoConstruct)
		if err != nil {
			return fmt.Errorf("field %q: %w", field.Name, err)
		}
		var toolName string
		if typeDef.Name == schema.Query.Name ||
			(autoConstruct != nil && allTools.Map[toolName].Name == "") {
			toolName = field.Name
		} else {
			toolName = typeDef.Name + "_" + field.Name
		}

		contextual := autoConstruct != nil

		allTools.Add(LLMTool{
			Name:        toolName,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      toolSchema,

			// TODO: would be nice, but have to 'or-null' all args and list everything
			// in required, which is annoying.
			Strict: false,

			// Only set Passthrough if this is a plain object method call, as opposed
			// to a contextual module tool.
			HideSelf: !contextual,

			// Tools that return Changeset or Env modify the environment.
			ReadOnly: field.Type.NamedType != "Env" && field.Type.NamedType != "Changeset",

			Call: func(ctx context.Context, args any) (_ any, rerr error) {
				argsMap, ok := args.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid arguments type: %T", args)
				}
				if !contextual {
					// reveal cache hits for raw (non-contextual) calls, even if we've
					// already seen them within the session
					ctx = dagql.WithRepeatedTelemetry(ctx)
				}
				return m.call(ctx, srv, schema, typeDef.Name, field, argsMap, autoConstruct)
			},
		})
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
	case nil:
		return ""
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
	schema *ast.Schema,
	selfType string,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	// The arguments to the call. Example: {"args": ["go", "build"], "redirectStderr", "/dev/null"}
	args map[string]any,
	// Whether the call should be made against a freshly constructed module
	autoConstruct *ObjectTypeDef,
) (res string, rerr error) {
	defer func() {
		// Capture logs produced by the tool call and prepend them to the response
		spanID := trace.SpanContextFromContext(ctx).SpanID()
		logs, err := m.captureLogs(ctx, spanID.String())
		if err != nil {
			slog.Error("failed to capture logs", "error", err)
		} else if len(logs) > 0 {
			// Show only the last 10 lines by default
			logs = limitLines(spanID.String(), logs, llmLogsLastLines, llmLogsMaxLineLen)

			// Avoid any extra surrounding whitespace (i.e. blank logs somehow)
			res = strings.Trim(strings.Join(logs, "\n")+"\n\n"+res, "\n")
		}
	}()

	// 1. CONVERT CALL INPUTS (BRAIN -> BODY)
	//
	var target dagql.AnyObjectResult
	var err error
	if self, ok := args["self"]; ok && self != nil {
		recv, ok := self.(string)
		if !ok {
			return "", fmt.Errorf("expected 'self' to be a string - got %#v", self)
		}
		target, err = m.GetObject(ctx, recv, selfType)
		if err != nil {
			return "", err
		}
	} else if selfType == srv.Root().ObjectType().TypeName() || autoConstruct != nil {
		// no self provided; either targeting Query, or auto-constructing from it
		target = srv.Root()
	} else if latest := m.TypeCounts()[selfType]; latest > 0 {
		// default to the newest object of this type
		target, err = m.GetObject(ctx, fmt.Sprintf("%s#%d", selfType, latest), selfType)
		if err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("no object of type %s found", selfType)
	}

	doSelect := func(ctx context.Context, env dagql.ObjectResult[*Env]) (dagql.AnyResult, error) {
		sels, err := m.toolCallToSelections(ctx, srv, schema, target.ObjectType(), fieldDef, args, autoConstruct)
		if err != nil {
			return nil, fmt.Errorf("failed to convert call inputs: %w", err)
		}
		var val dagql.AnyResult
		if err := srv.Select(ctx, target, &val, sels...); err != nil {
			return nil, err
		}
		if id, ok := dagql.UnwrapAs[dagql.IDType](val); ok {
			// Handle ID results by turning them back into Objects, since these are
			// typically implementation details hinting to SDKs to unlazy the call.
			syncedObj, err := srv.Load(ctx, id.ID())
			if err != nil {
				return nil, fmt.Errorf("failed to load synced object: %w", err)
			}
			val = syncedObj
		}
		return val, nil
	}

	val, err := doSelect(ctx, m.env)
	if err != nil {
		return "", err
	}

	// NOTE: returning an Env takes special meaning, at a higher precedence than
	// everything else; no object is actually returned, instead it directly
	// updates the MCP environment.
	if newEnv, ok := dagql.UnwrapAs[dagql.ObjectResult[*Env]](val); ok {
		// Swap out the Env for the updated one
		m.env = newEnv
		// No particular message needed here. At one point we diffed the Env.workspace
		// and printed which files were modified, but it's not really necessary to
		// show things like that unilaterally vs. just allowing each Env-returning
		// tool to control the messaging.
		return "", nil
	}

	// NOTE: returning a Changeset behaves similarly to returning an Env; it is
	// directly applied to the Env.
	if changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](val); ok {
		var newWS dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, m.env.Self().Workspace, &newWS, dagql.Selector{
			View:  srv.View,
			Field: "withChanges",
			Args: []dagql.NamedInput{
				{
					Name:  "changes",
					Value: dagql.NewID[*Changeset](changes.ID()),
				},
			},
		}); err != nil {
			return "", err
		}
		if err := m.updateEnvWorkspace(ctx, newWS); err != nil {
			return "", err
		}
		return m.summarizePatch(ctx, srv, changes)
	}

	if autoConstruct != nil {
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
			argsPayload, err := json.Marshal(args)
			if err != nil {
				return "", err
			}

			hash := hashutil.HashStrings(
				target.ObjectType().TypeName(),
				fieldDef.Name,
				string(argsPayload),
			)

			return m.toolObjectResponse(ctx, srv, obj, m.IngestContextual(
				hash,
				fmt.Sprintf("%s.%s %s", target.ObjectType().TypeName(), fieldDef.Name, string(argsPayload)),
				obj.ObjectType().TypeName(),
				doSelect,
			))
		}
	}

	return m.outputToLLM(ctx, srv, val)
}

func (m *MCP) outputToLLM(ctx context.Context, srv *dagql.Server, val dagql.Typed) (string, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// Handle object returns specially
		return m.toolObjectResponse(ctx, srv, obj, m.Ingest(obj, ""))
	}

	result, err := m.sanitizeResult(val)
	if err != nil {
		return "", fmt.Errorf("failed to simplify result: %w", err)
	}

	if str, ok := result.(string); ok {
		// Return string content directly, without wrapping it in JSON.
		return str, nil
	}

	if result == nil {
		// No response; just show logs, if any (handled above).
		return "", nil
	}

	// Handle scalars, arrays, etc
	return toolStructuredResponse(map[string]any{
		"result": result,
	})
}

func (m *MCP) sanitizeResult(val dagql.Typed) (any, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// Handle objects by showing their LLM ID, i.e. Container#123
		return m.Ingest(obj, ""), nil
	}

	if anyRes, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
		// Unwrap any Result[T]s so we don't encode a giant ID
		return m.sanitizeResult(anyRes.Unwrap())
	}

	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		// Handle arrays by sanitizing each value
		var res []any
		for i := 1; i <= list.Len(); i++ {
			val, err := list.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get ID for object %d: %w", i, err)
			}
			simpl, err := m.sanitizeResult(val)
			if err != nil {
				return nil, fmt.Errorf("failed to simplify list element %d: %w", i, err)
			}
			res = append(res, simpl)
		}
		return res, nil
	}

	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		// Handle strings by guarding against non-utf8 payloads.
		bytes := []byte(str.String())
		if !utf8.Valid(bytes) {
			return map[string]any{
				"type":   "non-utf8-string",
				"bytes":  len(bytes),
				"digest": digest.FromBytes(bytes),
			}, nil
		}
		// Return string content directly, without wrapping it in JSON.
		return str.String(), nil
	}

	if val == (Void{}) {
		// Represent Void as null. It's usually a 'null Void', but handle this
		// anyway for sanity's sake.
		return nil, nil
	}

	// Nothing else fishy, trust its marshaling
	return val, nil
}

func (m *MCP) toolCallToSelections(
	ctx context.Context,
	srv *dagql.Server,
	schema *ast.Schema,
	targetObjType dagql.ObjectType,
	// The definition of the dagql field to call. Example: Container.withExec
	fieldDef *ast.FieldDefinition,
	argsMap map[string]any,
	autoConstruct *ObjectTypeDef,
) ([]dagql.Selector, error) {
	var sels []dagql.Selector

	if autoConstruct != nil {
		consFieldDef := schema.Query.Fields.ForName(gqlFieldName(autoConstruct.Name))
		consSels, err := m.toolCallToSelections(ctx, srv, schema, srv.Root().ObjectType(), consFieldDef, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert constructor call inputs: %w", err)
		}
		// prepend constructor to the selection
		sels = append(sels, consSels...)

		// target the auto-constructed type for the field selection
		t, ok := srv.ObjectType(autoConstruct.Name)
		if !ok {
			return nil, fmt.Errorf("object type %q not found", autoConstruct.Name)
		}
		targetObjType = t
	}

	sel := dagql.Selector{
		View:  srv.View,
		Field: fieldDef.Name,
	}
	field, ok := targetObjType.FieldSpec(fieldDef.Name, call.View(engine.Version))
	if !ok {
		return nil, fmt.Errorf("field %q not found in object type %q",
			fieldDef.Name,
			targetObjType.TypeName())
	}
	remainingArgs := make(map[string]any)
	maps.Copy(remainingArgs, argsMap)
	delete(remainingArgs, "self") // ignore the meta 'self' arg

	for _, arg := range field.Args.Inputs(srv.View) {
		if arg.Internal {
			continue // skip internal args
		}
		val, ok := argsMap[arg.Name]
		if !ok {
			// value not available; arg not present
			continue
		}
		delete(remainingArgs, arg.Name)
		argDef := fieldDef.Arguments.ForName(arg.Name)
		scalar, ok := srv.ScalarType(argDef.Type.Name())
		if !ok {
			return nil, fmt.Errorf("arg %q: unknown scalar type %q", arg.Name, argDef.Type.Name())
		}
		if idType, ok := dagql.UnwrapAs[dagql.IDType](scalar); ok {
			idStr, ok := val.(string)
			if ok {
				// Handle Container#123 format ID args passed by the LLM
				expectedType := strings.TrimSuffix(idType.TypeName(), "ID")
				envVal, err := m.GetObject(ctx, idStr, expectedType)
				if err != nil {
					return nil, fmt.Errorf("arg %q: %w", arg.Name, err)
				}
				obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](envVal)
				if !ok {
					return nil, fmt.Errorf("arg %q: expected object, got %T", arg.Name, envVal)
				}
				val = obj.ID()
			}
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return nil, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	if len(remainingArgs) > 0 {
		return nil, fmt.Errorf("unknown args: %v", remainingArgs)
	}

	sels = append(sels, sel)

	if retObjType, ok := srv.ObjectType(fieldDef.Type.NamedType); ok {
		if sync, ok := retObjType.FieldSpec("sync", srv.View); ok {
			// If the Object supports "sync", auto-select it.
			//
			syncSel := dagql.Selector{
				View:  srv.View,
				Field: sync.Name,
			}
			sels = append(sels, syncSel)
		}
	}

	return sels, nil
}

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

// LookupTool looks for a tool identified by a name.
func (m *MCP) LookupTool(name string, tools []LLMTool) (*LLMTool, error) {
	var tool *LLMTool
	for _, t := range tools {
		if t.Name == name {
			tool = &t
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("tool %q is not available", name)
	}
	return tool, nil
}

func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall LLMToolCall) (res string, failed bool) {
	tool, err := m.LookupTool(toolCall.Function.Name, tools)
	if err != nil {
		return err.Error(), true
	}

	args := toolCall.Function.Arguments

	var toolArgNames []string
	var toolArgValues []string
	if requiredArgs, ok := tool.Schema["required"].([]string); ok {
		for _, arg := range requiredArgs {
			val, ok := args[arg]
			if !ok {
				continue
			}
			if str, ok := val.(string); ok {
				toolArgNames = append(toolArgNames, arg)
				toolArgValues = append(toolArgValues, str)
			}
		}
	}
	toolName := tool.Name
	if tool.Server != "" {
		toolName = strings.TrimPrefix(toolName, tool.Server+"_")
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.LLMToolAttr, toolName),
		attribute.StringSlice(telemetry.LLMToolArgNamesAttr, toolArgNames),
		attribute.StringSlice(telemetry.LLMToolArgValuesAttr, toolArgValues),
	}
	if tool.HideSelf {
		// Hide spans which are better represented by the child spans that they
		// spawn, i.e. CallMethod, ChainMethods, or direct object-method tools.
		attrs = append(attrs, attribute.Bool(telemetry.UIPassthroughAttr, true))
	}
	if tool.Server != "" {
		attrs = append(attrs, attribute.String(telemetry.LLMToolServerAttr, tool.Server))
	}
	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s%s", tool.Name, displayArgs(args)),
		telemetry.ActorEmoji("🤖"),
		telemetry.Reveal(),
		trace.WithAttributes(attrs...),
	)

	var telemetryErr error
	defer telemetry.EndWithCause(span, &telemetryErr)
	defer func() {
		if failed {
			telemetryErr = fmt.Errorf("tool call %q failed", tool.Name)
		}
	}()

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()

	defer func() {
		// write final result to telemetry so we see exactly what the LLM sees
		fmt.Fprintln(stdio.Stdout, res)
	}()

	result, err := tool.Call(EnvIDToContext(ctx, m.env.ID()), toolCall.Function.Arguments)
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

// CallBatch executes a batch of tool calls, handling MCP server syncing efficiently by
// grouping calls by destructiveness and server to avoid workspace conflicts
func (m *MCP) CallBatch(ctx context.Context, tools []LLMTool, toolCalls []LLMToolCall) []*ModelMessage {
	// Group tool calls by their characteristics
	readOnlyMCPCalls := make(map[string][]LLMToolCall)    // server -> read-only calls
	destructiveMCPCalls := make(map[string][]LLMToolCall) // server -> destructive calls
	regularCalls := make([]LLMToolCall, 0)
	destructiveCalls := make([]LLMToolCall, 0)

	for _, toolCall := range toolCalls {
		tool, err := m.LookupTool(toolCall.Function.Name, tools)
		if err != nil {
			// Couldn't find the tool, just call it regularly and let it fail with the
			// tool not found (or ambiguous) error
			regularCalls = append(regularCalls, toolCall)
			continue
		}

		if tool.Server == "" {
			// Regular tool call (not MCP)
			// Check if it modifies state (returns Env or Changeset)
			if tool.ReadOnly {
				regularCalls = append(regularCalls, toolCall)
			} else {
				destructiveCalls = append(destructiveCalls, toolCall)
			}
			continue
		}

		// This is an MCP tool call - check if it's read-only using the stored field
		if tool.ReadOnly {
			readOnlyMCPCalls[tool.Server] = append(readOnlyMCPCalls[tool.Server], toolCall)
		} else {
			destructiveMCPCalls[tool.Server] = append(destructiveMCPCalls[tool.Server], toolCall)
		}
	}

	var allResults []*ModelMessage

	// 1. Execute destructive non-MCP calls sequentially (they modify Env/Changeset state)
	for _, call := range destructiveCalls {
		result, isError := m.Call(ctx, tools, call)
		allResults = append(allResults, &ModelMessage{
			Role:        "user",
			Content:     result,
			ToolCallID:  call.ID,
			ToolErrored: isError,
		})
	}

	// 2. Execute destructive MCP calls one server at a time to avoid workspace conflicts
	for serverName, calls := range destructiveMCPCalls {
		serverResults := m.callBatchMCPServer(ctx, tools, calls, serverName)
		allResults = append(allResults, serverResults...)
	}

	// 3. Execute all regular read-only (non-MCP) calls in parallel
	if len(regularCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, regularCalls)...)
	}

	// 4. Execute all read-only MCP calls in parallel (safe across servers)
	var readOnlyToolCalls []LLMToolCall
	for _, calls := range readOnlyMCPCalls {
		readOnlyToolCalls = append(readOnlyToolCalls, calls...)
	}
	if len(readOnlyToolCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, readOnlyToolCalls)...)
	}

	return allResults
}

// callBatchMCPServer executes a batch of calls for a single MCP server with proper workspace syncing
func (m *MCP) callBatchMCPServer(ctx context.Context, tools []LLMTool, toolCalls []LLMToolCall, serverName string) []*ModelMessage {
	mcpSrv, ok := m.mcpServers[serverName]
	if !ok {
		// Fall back to individual calls if server not found
		return m.callBatchRegular(ctx, tools, toolCalls)
	}

	sess, ok := m.mcpSessions[serverName]
	if !ok {
		// Fall back to individual calls if session not found
		return m.callBatchRegular(ctx, tools, toolCalls)
	}

	ctr := mcpSrv.Service.Self().Container
	if ctr.Config.WorkingDir == "" || ctr.Config.WorkingDir == "/" {
		// No workspace syncing needed - execute normally
		return m.callBatchRegular(ctx, tools, toolCalls)
	}

	// Use runAndSnapshotChanges to sync workspace and execute all tool calls atomically
	var results []*ModelMessage
	snapshot, hasChanges, err := mcpSrv.Service.Self().runAndSnapshotChanges(
		ctx,
		sess.ID(),
		ctr.Config.WorkingDir,
		m.env.Self().Workspace.Self(),
		func() error {
			// Execute all tool calls for this server in parallel within the synced context
			results = m.callBatchRegular(ctx, tools, toolCalls)
			return nil
		})

	if err != nil {
		// Fall back to individual calls if sync fails
		return m.callBatchRegular(ctx, tools, toolCalls)
	}

	// Apply workspace changes if any were made
	if hasChanges {
		if err := m.updateEnvWorkspace(ctx, snapshot); err != nil {
			slog.Error("failed to update workspace after MCP server batch", "server", serverName, "error", err)
		}
	}

	return results
}

// callBatchRegular is the original parallel execution logic without MCP-specific syncing
func (m *MCP) callBatchRegular(ctx context.Context, tools []LLMTool, toolCalls []LLMToolCall) []*ModelMessage {
	// Run tool calls in parallel using the existing pool logic
	toolCallsPool := pool.NewWithResults[*ModelMessage]()
	for _, toolCall := range toolCalls {
		toolCallsPool.Go(func() *ModelMessage {
			content, isError := m.Call(ctx, tools, toolCall)
			return &ModelMessage{
				Role:        "user", // Anthropic only allows tool call results in user messages
				Content:     content,
				ToolCallID:  toolCall.ID,
				ToolErrored: isError,
			}
		})
	}
	return toolCallsPool.Wait()
}

// sync this with idtui.llmLogsLastLines to ensure user and LLM sees the same
// thing
const llmLogsLastLines = 8
const llmLogsMaxLineLen = 2000
const llmLogsBatchSize = 1000

// captureLogs returns nicely Heroku-formatted lines of all logs seen since the
// last capture.
func (m *MCP) captureLogs(ctx context.Context, spanID string) ([]string, error) {
	root, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, err
	}
	defer q.Close()

	buf := new(strings.Builder)

	var lastLogID int64

	for {
		logs, err := q.SelectLogsBeneathSpan(ctx, clientdb.SelectLogsBeneathSpanParams{
			ID:     lastLogID,
			SpanID: sql.NullString{Valid: true, String: spanID},
			Limit:  llmLogsBatchSize,
		})
		if err != nil {
			return nil, err
		}
		if len(logs) == 0 {
			break
		}

		for _, log := range logs {
			lastLogID = log.ID

			var logAttrs []*otlpcommonv1.KeyValue
			if err := clientdb.UnmarshalProtoJSONs(log.Attributes, &otlpcommonv1.KeyValue{}, &logAttrs); err != nil {
				slog.Warn("failed to unmarshal log attributes", "error", err)
				continue
			}
			var skip bool
		dance:
			for _, attr := range logAttrs {
				switch attr.Key {
				case telemetry.StdioEOFAttr, telemetry.LogsVerboseAttr, telemetry.LogsGlobalAttr:
					if attr.Value.GetBoolValue() {
						skip = true
						break dance
					}
				}
			}
			if skip {
				// don't generate a line for EOF events
				continue
			}

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
			}

			var bodyPb otlpcommonv1.AnyValue
			if err := proto.Unmarshal(log.Body, &bodyPb); err != nil {
				slog.Warn("failed to unmarshal log body", "error", err, "client", mainMeta.ClientID, "log", log.ID)
				continue
			}
			switch x := bodyPb.GetValue().(type) {
			case *otlpcommonv1.AnyValue_StringValue:
				fmt.Fprint(buf, x.StringValue)
			case *otlpcommonv1.AnyValue_BytesValue:
				buf.Write(x.BytesValue)
			default:
				// default to something troubleshootable
				fmt.Fprintf(buf, "UNHANDLED: %+v", x)
			}
		}
	}
	if buf.Len() == 0 {
		return nil, nil
	}
	return strings.Split(
		// ensure trailing linebreaks don't contribute to line limits
		strings.TrimRight(buf.String(), "\n"),
		"\n",
	), nil
}

func toolErrorMessage(err error) string {
	errResponse := err.Error()
	// propagate error values to the model
	var extErr dagql.ExtendedError
	if errors.As(err, &extErr) {
		// TODO: return a structured error object instead?
		var exts []string
		for k, v := range extErr.Extensions() {
			if k == "traceparent" || k == "baggage" {
				// silence this one
				continue
			}
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

func (m *MCP) saveTool(srv *dagql.Server) LLMTool {
	desc := "Save an output that has been requested by the user."

	checklist := func() string {
		var list []string
		for name, b := range m.env.Self().outputsByName {
			checked := " "
			if b.Value != nil {
				checked = "x"
			}
			list = append(list,
				fmt.Sprintf("- [%s] %s (%s): %s", checked, name, b.ExpectedType, b.Description))
		}

		sort.Strings(list)

		return strings.Join(list, "\n")
	}

	desc += "\n\nThe following checklist describes the desired outputs:"
	desc += "\n\n" + checklist()

	return LLMTool{
		Name:        "Save",
		Description: desc,
		ReadOnly:    false, // Modifies output state
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the output, following shell naming conventions ([a-z][a-z0-9_]*).",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "The value to save for the output.",
				},
			},
			"required":             []string{"name", "value"},
			"additionalProperties": false,
		},
		Strict: true,
		Call: ToolFunc(srv, func(ctx context.Context, args struct {
			Name  string
			Value string
		}) (any, error) {
			output, ok := m.env.Self().outputsByName[args.Name]
			if !ok {
				return nil, fmt.Errorf("unknown output: %q - please declare it first", args.Name)
			}
			if output.ExpectedType == "String" {
				output.Value = dagql.String(args.Value)
			} else {
				bnd, ok, err := m.Input(ctx, args.Value)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("object not found for argument %s: %s", args.Name, args.Value)
				}

				obj := bnd.Value
				actualType := obj.Type().Name()
				if output.ExpectedType != actualType {
					return nil, fmt.Errorf("incompatible types: %s must be %s, got %s", args.Name, output.ExpectedType, actualType)
				}

				// Propagate description from output to binding so that outputs are
				// described under `Available objects:`
				bnd.Description = output.Description
				output.Value = obj
			}

			// If all outputs have been saved, we can flag the MCP as having completed
			// its task.
			var anyNotSaved bool
			for _, output := range m.env.Self().outputsByName {
				if output.Value == nil {
					anyNotSaved = true
					break
				}
			}
			m.returned = !anyNotSaved

			return checklist(), nil
		}),
	}
}

func (m *MCP) loadBuiltins(srv *dagql.Server, allTools, objectMethods *LLMToolSet) {
	schema := srv.Schema()

	if m.env.Self().writable {
		allTypes := map[string]dagql.Type{
			"String": dagql.String(""),
		}
		for name := range schema.Types {
			if strings.HasPrefix(name, "_") {
				continue
			}
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
		allTools.Add(LLMTool{
			Name:        "DeclareOutput",
			Description: "Declare a new output that can have a value saved to it",
			ReadOnly:    false, // Modifies Env state
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
						"type":        []string{"string", "null"},
						"description": "An optional description of the output.",
					},
				},
				"required":             []string{"name", "type", "description"},
				"additionalProperties": false,
			},
			Strict: true,
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Name        string
				Type        string
				Description string `default:""`
			}) (any, error) {
				if _, ok := allTypes[args.Type]; !ok {
					return nil, fmt.Errorf("unknown type: %q", args.Type)
				}
				var dest dagql.ObjectResult[*Env]
				err := srv.Select(ctx, m.env, &dest, dagql.Selector{
					View:  srv.View,
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
				m.env = dest
				return toolStructuredResponse(map[string]any{
					"output": args.Name,
					"hint":   "To save a value to the output, use the Save tool.",
				})
			}),
		})
	}

	if len(m.TypeCounts()) > 0 {
		allTools.Add(LLMTool{
			Name:        "ListObjects",
			Description: "List available objects.",
			ReadOnly:    true, // Read-only operation
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
				counts := m.TypeCounts()
				for _, typeName := range slices.Sorted(maps.Keys(counts)) {
					count := counts[typeName]
					for i := 1; i <= count; i++ {
						bnd, found, err := m.Input(ctx, fmt.Sprintf("%s#%d", typeName, i))
						if err != nil {
							continue
						}
						if !found {
							// impossible?
							continue
						}
						objects = append(objects, objDesc{
							ID:          bnd.ID(),
							Description: bnd.Description,
						})
					}
				}
				return toolStructuredResponse(objects)
			}),
		})
	}

	if m.staticTools {
		m.loadStaticMethodCallingTools(srv, allTools, objectMethods)
	}

	allTools.Add(LLMTool{
		Name:        "ReadLogs",
		Description: "Read logs from the most recent execution. Can filter with grep pattern or read the last N lines.",
		ReadOnly:    true, // Read-only operation
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"span": map[string]any{
					"type":        "string",
					"description": "Span ID to query logs beneath, recursively",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Number of lines to read from the end.",
					"minimum":     1,
					"default":     100,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Number of lines to skip from the end. If not specified, starts from the end.",
					"minimum":     0,
				},
				"grep": map[string]any{
					"type":        "string",
					"description": "Grep pattern to filter logs. If specified, only lines matching this pattern will be returned.",
				},
			},
			"required":             []string{"span"},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.readLogsTool(srv),
	})

	if len(m.env.Self().outputsByName) > 0 {
		allTools.Add(m.saveTool(srv))
	}

	if len(m.env.Self().inputsByName) > 0 {
		allTools.Add(LLMTool{
			Name:        "UserProvidedValues",
			Description: "Read the inputs supplied by the user.",
			ReadOnly:    true, // Read-only operation
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"required":             []string{},
				"additionalProperties": false,
			},
			Strict: true,
			Call: func(ctx context.Context, args any) (any, error) {
				values, err := m.userProvidedValues()
				if err != nil {
					return nil, err
				}
				if values == "" {
					return "No user-provided values.", nil
				}
				return values, nil
			},
		})
	}
}

func (m *MCP) readLogsTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span   string
		Offset int    `default:"0"`
		Limit  int    `default:"100"`
		Grep   string `default:""`
	}) (any, error) {
		logs, err := m.captureLogs(ctx, args.Span)
		if err != nil {
			return nil, fmt.Errorf("failed to capture logs: %w", err)
		}

		// Trim the last Offset lines
		if args.Offset >= len(logs) {
			return nil, fmt.Errorf("offset %d is beyond log length %d", args.Offset, len(logs))
		}
		logs = logs[:len(logs)-args.Offset]

		// Apply grep filter if specified
		if args.Grep != "" {
			re, err := regexp.Compile(args.Grep)
			if err != nil {
				return nil, fmt.Errorf("invalid grep pattern %q: %w", args.Grep, err)
			}
			var filteredLogs []string
			for i, line := range logs {
				if re.MatchString(line) {
					filteredLogs = append(filteredLogs, fmt.Sprintf("%6d→%s", i+1, line))
				}
			}
			logs = filteredLogs
		} else {
			for i, line := range logs {
				logs[i] = fmt.Sprintf("%6d→%s", i+1, line)
			}
		}

		// Apply line limit if specified
		logs = limitLines(args.Span, logs, args.Limit, llmLogsMaxLineLen)

		return strings.Join(logs, "\n"), nil
	})
}

func (m *MCP) loadStaticMethodCallingTools(srv *dagql.Server, allTools *LLMToolSet, objectMethods *LLMToolSet) {
	allTools.Add(LLMTool{
		Name:        "ListMethods",
		Description: "List the methods that can be selected.",
		ReadOnly:    true, // Read-only operation
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
		Call:   m.listMethodsTool(srv, objectMethods),
	})

	allTools.Add(LLMTool{
		Name:        "SelectMethods",
		Description: "Select methods for interacting with the available objects. Never guess - only select methods previously returned by ListMethods.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"methods": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":        "string",
						"description": "The name of the method to select, as seen in ListMethods.",
					},
					"description": "The methods to select.",
				},
			},
			"required":             []string{"methods"},
			"additionalProperties": false,
		},
		Strict: true,
		Call:   m.selectMethodsTool(srv, objectMethods),
	})

	allTools.Add(LLMTool{
		Name:        "CallMethod",
		Description: "Call a method on an object. Methods must be selected with SelectMethods before calling them. Self represents the object to call the method on, and args specify any additional parameters to pass.",
		HideSelf:    true,
		ReadOnly:    false, // Can call methods that return Env or Changeset
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
		Call:   m.callMethodTool(objectMethods),
	})

	allTools.Add(LLMTool{
		Name: "ChainMethods",
		Description: `Invoke multiple methods sequentially, passing the result of one method as the receiver of the next

NOTE: you must select methods before chaining them`,
		HideSelf: true,
		ReadOnly: false, // Can call methods that return Env or Changeset
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
		Call:   m.chainMethodsTool(srv, objectMethods),
	})
}

func (m *MCP) listMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct{}) (any, error) {
		type toolDesc struct {
			Name         string            `json:"name"`
			Returns      string            `json:"returns"`
			RequiredArgs map[string]string `json:"required_args,omitempty"`
		}
		var methods []toolDesc
		for _, method := range objectMethods.Order {
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
	})
}

func (m *MCP) selectMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
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
			method, found := objectMethods.Map[methodName]
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
			return nil, fmt.Errorf("unknown methods: %v; use ListMethods first", unknownMethods)
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
	})
}

func (m *MCP) callMethodTool(objectMethods *LLMToolSet) LLMToolFunc {
	return func(ctx context.Context, argsAny any) (_ any, rerr error) {
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
			if !strings.Contains(call.Method, "_") {
				// allow omitting the TypeName_ prefix, which models are more prone
				// to guessing
				call.Method = fmt.Sprintf("%s_%s", typeName, call.Method)
			}
		}
		var method LLMTool
		method, found := objectMethods.Map[call.Method]
		if !found {
			return nil, fmt.Errorf("method not defined: %q; use ListMethods first", call.Method)
		}
		if !m.selectedMethods[call.Method] {
			return nil, fmt.Errorf("method not selected: %q; use SelectMethods first", call.Method)
		}
		return method.Call(ctx, call.Args)
	}
}

func (m *MCP) chainMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	schema := srv.Schema()
	return func(ctx context.Context, argsAny any) (_ any, rerr error) {
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
		if err := m.validateAndNormalizeChain(ctx, toolArgs.Self, toolArgs.Chain, objectMethods, schema); err != nil {
			return nil, err
		}
		var res any
		for i, call := range toolArgs.Chain {
			var tool LLMTool
			tool, found := objectMethods.Map[call.Method]
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
	}
}

type ChainedCall struct {
	Method string         `json:"method"`
	Args   map[string]any `json:"args"`
}

func (m *MCP) validateAndNormalizeChain(ctx context.Context, self string, calls []ChainedCall, objectMethods *LLMToolSet, schema *ast.Schema) error {
	if len(calls) == 0 {
		return errors.New("no methods called")
	}
	var currentType *ast.Type
	if self != "" {
		obj, err := m.GetObject(ctx, self, "")
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
		if !strings.Contains(call.Method, "_") && currentType != nil {
			// add type prefix to method name
			call.Method = currentType.Name() + "_" + call.Method
			calls[i] = call
		}
		method, found := objectMethods.Map[call.Method]
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

func (m *MCP) userProvidedValues() (string, error) {
	type valueDesc struct {
		Description string `json:"description"`
		Value       any    `json:"value"`
	}
	var values []valueDesc
	for _, input := range m.env.Self().Inputs() {
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
	if len(values) == 0 {
		return "", nil
	}
	return toolStructuredResponse(values)
}

func (m *MCP) IsDone() bool {
	return len(m.env.Self().outputsByName) == 0 || m.returned
}

var idRegex = regexp.MustCompile(`^(?P<type>[A-Z]\w*)#(?P<nth>\d+)$`)

func (m *MCP) fieldArgsToJSONSchema(schema *ast.Schema, typeDef *ast.Definition, field *ast.FieldDefinition, autoConstruct *ObjectTypeDef) (map[string]any, error) {
	properties := map[string]any{}
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
		if desc != "" {
			argSchema["description"] = desc
		}

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
	if typeDef.Name != schema.Query.Name && autoConstruct == nil {
		properties["self"] = map[string]any{
			"type":        []string{"string", "null"},
			"description": "The ID of the object to call this method on",
		}
		required = append(required, "self")
	}
	jsonSchema := map[string]any{
		"type":       "object",
		"properties": properties,
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

func (m *MCP) TypeCounts() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.typeCounts)
}

func (m *MCP) toolObjectResponse(ctx context.Context, srv *dagql.Server, target dagql.AnyObjectResult, objID string) (string, error) {
	schema := srv.Schema()
	typeName := target.Type().Name()
	m.mu.Lock()
	_, known := m.typeCounts[typeName]
	m.mu.Unlock()
	res := map[string]any{
		"result": objID,
	}
	data := map[string]any{}
	for _, field := range schema.Types[typeName].Fields {
		trivial := field.Directives.ForName(trivialFieldDirectiveName) != nil
		if !trivial {
			continue
		}
		val, err := target.Select(ctx, srv, dagql.Selector{
			View:  srv.View,
			Field: field.Name,
		})
		if err != nil {
			return "", err
		}
		if _, isObj := srv.ObjectType(val.Type().Name()); isObj {
			// skip any fields that reference objects, to avoid dumping entire
			// ModuleObjects
			continue
		}
		datum, err := m.sanitizeResult(val)
		if err != nil {
			return "", err
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

func (m *MCP) Ingest(obj dagql.AnyObjectResult, desc string) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	return m.IngestBy(obj, desc, hash)
}

func (m *MCP) IngestBy(obj dagql.AnyObjectResult, desc string, hash digest.Digest) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	typeName := id.Type().NamedType()
	llmID, ok := m.idByHash[hash]
	if !ok {
		m.typeCounts[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, m.typeCounts[typeName])
		if desc == "" {
			desc = m.describeLocked(id)
		}
		m.idByHash[hash] = llmID
		m.objsByID[llmID] = func(context.Context, dagql.ObjectResult[*Env]) (*Binding, error) {
			return &Binding{
				Key:          llmID,
				Value:        obj,
				Description:  desc,
				ExpectedType: obj.ObjectType().TypeName(),
			}, nil
		}
	}
	return llmID
}

func (m *MCP) IngestContextual(
	hash digest.Digest,
	desc string,
	typeName string,
	create func(ctx context.Context, env dagql.ObjectResult[*Env]) (dagql.AnyResult, error),
) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	llmID, ok := m.idByHash[hash]
	if !ok {
		m.typeCounts[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, m.typeCounts[typeName])
		m.idByHash[hash] = llmID
		m.objsByID[llmID] = func(ctx context.Context, env dagql.ObjectResult[*Env]) (*Binding, error) {
			obj, err := create(ctx, env)
			if err != nil {
				return nil, err
			}
			return &Binding{
				Key:          llmID,
				Value:        obj,
				Description:  desc,
				ExpectedType: typeName,
			}, nil
		}
	}
	return llmID
}

func (m *MCP) describeLocked(id *call.ID) string {
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
			str.WriteString(m.displayLitLocked(arg.Value()))
		}
		str.WriteString(")")
	}
	return str.String()
}

func (m *MCP) displayLitLocked(lit call.Literal) string {
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
		for i, value := range x.Values() {
			if i > 0 {
				list += ","
			}
			list += m.displayLitLocked(value)
		}
		list += "]"
		return list
	case *call.LiteralObject:
		obj := "{"
		for i, arg := range x.Args() {
			if i > 0 {
				obj += ","
			}
			obj += arg.Name() + ": " + m.displayLitLocked(arg.Value())
		}
		obj += "}"
		return obj
	default:
		return lit.Display()
	}
}

func (m *MCP) Types() []string {
	// Make sure we count env inputs
	for _, input := range m.env.Self().Inputs() {
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](input.Value); ok {
			m.Ingest(obj, input.Description)
		}
	}
	return slices.Collect(maps.Keys(m.TypeCounts()))
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

func limitLines(spanID string, logs []string, limit, maxLineLen int) []string {
	if limit > 0 && len(logs) > limit {
		snipped := fmt.Sprintf("... %d lines omitted (use ReadLogs(span: %s) to read more) ...", len(logs)-limit, spanID)
		logs = append([]string{snipped}, logs[len(logs)-limit:]...)
	}
	for i, line := range logs {
		if len(line) > maxLineLen {
			logs[i] = line[:maxLineLen] + fmt.Sprintf("[... %d chars truncated]", len(line)-maxLineLen)
		}
	}
	return logs
}

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

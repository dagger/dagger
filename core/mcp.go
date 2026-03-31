package core

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/core/prompts"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	env  dagql.ObjectResult[*Env]
	objs *LLMObjects

	// Expose a static toolset for calling methods, rather than directly exposing
	// a dynamic set of methods as tools
	staticTools bool
	// Only show these functions, if non-empty
	selectedMethods map[string]bool
	// Never show these functions, grouped by type
	blockedMethods map[string][]string
	// The last value returned by a function.
	lastResult dagql.Typed
	// Configured MCP servers.
	mcpServers map[string]*MCPServerConfig
}

// newMCPEmpty creates an MCP with no env.
func newMCPEmpty() *MCP {
	blocked := maps.Clone(defaultBlockedMethods)
	for typeName, methods := range blocked {
		blocked[typeName] = slices.Clone(methods)
	}
	return &MCP{
		objs:            NewLLMObjects(),
		selectedMethods: map[string]bool{},
		blockedMethods:  blocked,
		mcpServers:      make(map[string]*MCPServerConfig),
	}
}

func newMCP(env dagql.ObjectResult[*Env]) *MCP {
	m := newMCPEmpty()
	m.setEnv(env)
	return m
}

func (m *MCP) Clone() *MCP {
	cp := *m
	cp.objs = cp.objs.Clone()
	cp.selectedMethods = maps.Clone(cp.selectedMethods)
	cp.blockedMethods = maps.Clone(cp.blockedMethods)
	for typeName, methods := range cp.blockedMethods {
		cp.blockedMethods[typeName] = slices.Clone(methods)
	}
	cp.mcpServers = maps.Clone(cp.mcpServers)
	return &cp
}

func (m *MCP) setEnv(env dagql.ObjectResult[*Env]) {
	m.env = env
	for _, input := range m.env.Self().Inputs() {
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](input.Value); ok {
			m.objs.Track(obj, input.Description)
		}
	}
}

func (m *MCP) WithObject(llmID string, id dagql.AnyID) *MCP {
	m = m.Clone()
	m.objs = m.objs.WithObject(llmID, id)
	return m
}

func (m *MCP) WithMCPServer(srv *MCPServerConfig) *MCP {
	m = m.Clone()
	m.mcpServers[srv.Name] = srv
	return m
}

func (m *MCP) Server(ctx context.Context) (*dagql.Server, error) {
	if m.env.ID() != nil {
		return m.env.Self().deps.Server(ctx)
	}
	return CurrentDagqlServer(ctx)
}

func (m *MCP) LastResult() dagql.Typed {
	return m.lastResult
}

// Input looks up an input binding by key from tracked objects.
func (m *MCP) Input(ctx context.Context, key string) (*Binding, bool, error) {
	srv, err := m.Server(ctx)
	if err != nil {
		return nil, false, err
	}
	return m.objs.LookupBinding(ctx, srv, key)
}

// GetObject resolves an LLM-friendly key to a dagql object.
func (m *MCP) GetObject(ctx context.Context, key, expectedType string) (dagql.AnyObjectResult, error) {
	key = NormalizeObjectRef(key, expectedType)
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

// Tools loads all tools from all sources.
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
		for _, t := range objectMethods.Order {
			allTools.Add(t)
		}
	}
	m.loadBuiltins(srv, allTools, objectMethods)
	return allTools.Order, nil
}

// LookupTool looks for a tool identified by a name.
func (m *MCP) LookupTool(name string, tools []LLMTool) (*LLMTool, error) {
	for _, t := range tools {
		if t.Name == name {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("tool %q is not available", name)
}

// BlockFunction blocks a function from being exposed as a tool.
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

// DefaultSystemPrompt assembles the system prompt from env state.
func (m *MCP) DefaultSystemPrompt() string {
	if m.env.ID() == nil {
		return ""
	}
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

// call is the core tool execution function.
func (m *MCP) call(ctx context.Context,
	srv *dagql.Server,
	schema *ast.Schema,
	selfType string,
	fieldDef *ast.FieldDefinition,
	args map[string]any,
	autoConstruct *ObjectTypeDef,
) (res string, rerr error) {
	defer func() {
		prependLogs(ctx, &res)
	}()

	// Propagate the workspace into context so module function calls
	// resolve workspace arguments against the caller's workspace.
	if m.env.ID() != nil {
		ws := m.env.Self().Workspace
		if ws.ID() != nil {
			ctx = WorkspaceIDToContext(ctx, ws.ID())
		}
	}

	// 1. Resolve the target object
	var target dagql.AnyObjectResult
	var err error
	var skipAutoConstruct bool
	if self, ok := args["self"]; ok && self != nil {
		recv, ok := self.(string)
		if !ok {
			return "", fmt.Errorf("expected 'self' to be a string - got %#v", self)
		}
		target, err = m.GetObject(ctx, recv, selfType)
		if err != nil {
			return "", err
		}
	} else if autoConstruct != nil {
		if latest := m.objs.TypeCounts()[autoConstruct.Name]; latest > 0 {
			target, err = m.GetObject(ctx, fmt.Sprintf("%s#%d", autoConstruct.Name, latest), autoConstruct.Name)
			if err != nil {
				return "", err
			}
			skipAutoConstruct = true
		} else {
			target = srv.Root()
		}
	} else if selfType == srv.Root().ObjectType().TypeName() {
		target = srv.Root()
	} else if latest := m.objs.TypeCounts()[selfType]; latest > 0 {
		target, err = m.GetObject(ctx, fmt.Sprintf("%s#%d", selfType, latest), selfType)
		if err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("no object of type %s found", selfType)
	}

	// 2. Build selectors
	var sels []dagql.Selector

	if autoConstruct != nil && !skipAutoConstruct {
		consSel, err := buildConstructorSelector(srv, schema, autoConstruct)
		if err != nil {
			return "", fmt.Errorf("build constructor: %w", err)
		}
		sels = append(sels, consSel)

		t, ok := srv.ObjectType(autoConstruct.Name)
		if !ok {
			return "", fmt.Errorf("object type %q not found", autoConstruct.Name)
		}
		sel, err := buildSelector(ctx, srv, schema, t, fieldDef, args, m.objs)
		if err != nil {
			return "", fmt.Errorf("build selector: %w", err)
		}
		sels = append(sels, sel)
	} else {
		sel, err := buildSelector(ctx, srv, schema, target.ObjectType(), fieldDef, args, m.objs)
		if err != nil {
			return "", fmt.Errorf("build selector: %w", err)
		}
		sels = append(sels, sel)
	}

	sels = appendSyncSelector(srv, fieldDef, sels)

	// 3. Execute
	val, err := execute(ctx, srv, target, sels...)
	if err != nil {
		return "", err
	}

	// 4. Format result
	moduleName := ""
	if autoConstruct != nil {
		moduleName = autoConstruct.Name
	}
	return formatResult(ctx, srv, m.objs, val, moduleName)
}

// TypeCounts delegates to the object tracker.
func (m *MCP) TypeCounts() map[string]int {
	return m.objs.TypeCounts()
}

// Ingest adds an object to the object tracker.
func (m *MCP) Ingest(obj dagql.AnyObjectResult, desc string) string {
	return m.objs.Track(obj, desc)
}

// IngestBy adds an object to the object tracker by a specific digest.
func (m *MCP) IngestBy(id *call.ID, desc string, hash digest.Digest) string {
	return m.objs.TrackByDigest(id, desc, hash)
}

// Types delegates to the object tracker.
func (m *MCP) Types() []string {
	return m.objs.Types()
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
		"asModule",
		"asModuleSource",
		"export",
		"name",
		"terminal",
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

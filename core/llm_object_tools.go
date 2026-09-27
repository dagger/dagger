package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
)

// This file implements the object-tools scheme (hack/designs/workspace-agents.md).
// The LLM binds one or more objects via LLM.withTools, and every eligible method
// of a bound object becomes a tool. A tool that returns the bound object's own
// type replaces the binding — so the object IS the agent's state, and a state
// update is just a method that returns a new self. This supersedes the Dang
// scripting harness (dang_eval + inspect) as the core agent interface; the Dang
// machinery stays in the tree for module authoring.

// llmToolLogsMaxLines caps the print output surfaced from a single tool call
// (object/Void returns), matching ReadLogs' default page size.
const llmToolLogsMaxLines = 8

// workspaceTypeName is the object type the engine auto-injects into a module
// function's arguments from the bound Workspace, so such arguments are hidden
// from the generated tool schema.
const workspaceTypeName = "Workspace"

// llmTypeName identifies an object-tool argument that MCP fills directly from
// the conversation making the call. It is hidden only from the generated tool
// schema; the module's GraphQL schema remains unchanged. A method returning LLM
// is a continuation: the loop resumes from the returned conversation.
const llmTypeName = "LLM"

// agentTypeName is the object type the engine auto-injects into a module
// function's arguments from the calling agent, when the tool call is
// dispatched from a running agent loop (see core/agent_context.go). Like
// Workspace and LLM, such arguments are hidden from the generated tool
// schema — the model never sees or supplies them.
const agentTypeName = "Agent"

// boundTool is one object bound into the LLM's toolset via withTools. It carries
// enough to build the toolset (the object's type, via objType) and to dispatch a
// tool call (the object itself, as the receiver). The object may be held lazily
// as an unevaluated ID: a binding restored from a persisted session references
// an object whose construction might have side effects or might no longer be
// reproducible, so it is only loaded when a tool is actually invoked on it. See
// the LazyRef argument on LLM.withTools.
type boundTool struct {
	// object is the loaded receiver. It is nil for a lazy binding until the
	// first dispatch loads it (see MCP.loadBoundTool).
	object dagql.AnyObjectResult
	// id is the recipe ID of the bound object, used to load it lazily. Always
	// set (both for eager and lazy bindings) so the binding can be re-emitted.
	id *call.ID
	// objType is the bound object's GraphQL type, known without loading, so the
	// toolset can be built from a lazy binding.
	objType dagql.ObjectType
	// definingSchema is the schema that defined objType when this binding was
	// composed. It is authoritative for the lifetime of the binding: workspace
	// edits only affect tools after explicit recomposition creates a new LLM.
	definingSchema *ast.Schema
	Except         []string
	// Owner identifies the composition that installed this binding. A
	// same-type tool return retains it; an explicit withTools replaces it.
	// Empty means caller-owned/unowned.
	Owner string
	// Version is the explicit withTools state contract. Same-type returns keep
	// it; recomposition resets the receiver when the new binding differs.
	Version int
}

// typeName returns the bound object's type name without forcing a load.
func (b boundTool) typeName() string {
	if b.object != nil {
		return b.object.Type().Name()
	}
	if b.objType != nil {
		return b.objType.TypeName()
	}
	return ""
}

// impure reports whether the bound type's field is marked as having side
// effects or live reads: a module function with cache policy Never, or a core
// field marked DoNotCache. Looked up on the bound object's own type, since the
// defining schema's AST carries neither.
func (b boundTool) impure(fieldName string, srv *dagql.Server) bool {
	objType := b.objType
	if b.object != nil {
		objType = b.object.ObjectType()
	}
	if objType == nil || srv == nil {
		return false
	}
	spec, ok := objType.FieldSpec(fieldName, srv.View)
	return ok && (cachePolicyNever(spec) || spec.DoNotCache != "")
}

// WithTools binds obj's methods as tools, carrying the schema that defined the
// receiver at composition time and except. At most one binding per object type
// is kept: binding an object whose type is already bound replaces it in place.
// That is the state-update shape — a method returning the bound type rebinds
// through here — so the binding list stays bounded and a recorded withTools
// selector reconstructs the same state deterministically.
func (m *MCP) WithTools(obj dagql.AnyObjectResult, definingSchema *ast.Schema, except []string) *MCP {
	return m.withToolsOwner(obj, definingSchema, except, "", 0)
}

func (m *MCP) withToolsOwner(obj dagql.AnyObjectResult, definingSchema *ast.Schema, except []string, owner string, version int) *MCP {
	m = m.Clone()
	typeName := obj.Type().Name()
	id, _ := obj.ID()
	binding := boundTool{
		object:         obj,
		id:             id,
		objType:        obj.ObjectType(),
		definingSchema: definingSchema,
		Except:         except,
		Owner:          owner,
		Version:        version,
	}
	for i, b := range m.boundTools {
		if b.typeName() == typeName {
			m.boundTools[i] = binding
			return m
		}
	}
	m.boundTools = append(m.boundTools, binding)
	return m
}

// WithLazyTools binds an object's methods as tools from its unevaluated ID,
// without loading it. Used when restoring a persisted session: the referenced
// object is only loaded if and when a tool is actually invoked on it (see
// MCP.boundToolObject), so restoring the conversation never re-runs the call
// that produced the object. objType and definingSchema describe the object's
// GraphQL type without loading it. The defining schema stays authoritative even
// if the bound Workspace later contains another definition of the same type.
func (m *MCP) WithLazyTools(id *call.ID, objType dagql.ObjectType, definingSchema *ast.Schema, except []string) *MCP {
	return m.withLazyToolsOwner(id, objType, definingSchema, except, "", 0)
}

func (m *MCP) withLazyToolsOwner(id *call.ID, objType dagql.ObjectType, definingSchema *ast.Schema, except []string, owner string, version int) *MCP {
	m = m.Clone()
	typeName := objType.TypeName()
	binding := boundTool{id: id, objType: objType, definingSchema: definingSchema, Except: except, Owner: owner, Version: version}
	for i, b := range m.boundTools {
		if b.typeName() == typeName {
			m.boundTools[i] = binding
			return m
		}
	}
	m.boundTools = append(m.boundTools, binding)
	return m
}

// boundToolObject returns the current bound object for a type, loading it lazily
// if the binding was restored from an unevaluated ID. Loading (and any side
// effects or failures it entails) is deferred to here, the first time a tool is
// actually invoked on the binding.
func (m *MCP) boundToolObject(ctx context.Context, srv *dagql.Server, typeName string) (dagql.AnyObjectResult, bool, error) {
	// Look up the binding under the lock so a state update from an earlier call
	// in the same batch is visible. If it needs loading, release the lock first:
	// srv.Load can be slow and may re-enter MCP, so it must not run under m.mu.
	m.mu.Lock()
	var toLoad *call.ID
	for _, b := range m.boundTools {
		if b.typeName() != typeName {
			continue
		}
		if b.object != nil {
			obj := b.object
			m.mu.Unlock()
			return obj, true, nil
		}
		if b.id == nil {
			m.mu.Unlock()
			return nil, false, fmt.Errorf("bound object of type %q has neither a loaded value nor an ID", typeName)
		}
		toLoad = b.id
		break
	}
	m.mu.Unlock()
	if toLoad == nil {
		return nil, false, nil
	}

	obj, err := srv.Load(ctx, toLoad)
	if err != nil {
		return nil, true, fmt.Errorf("load bound object of type %q: %w", typeName, err)
	}

	// Store the loaded object back, unless another caller already did (or the
	// binding was rebound in the meantime — then keep the newer value).
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, b := range m.boundTools {
		if b.typeName() != typeName {
			continue
		}
		if b.object != nil {
			return b.object, true, nil
		}
		// Loading through the caller's server can wrap the value in an older
		// class with the same name. The binding's composition-time class is
		// authoritative, just like its definingSchema.
		obj, err = b.objType.New(obj)
		if err != nil {
			return nil, true, fmt.Errorf("load bound object of type %q: %w", typeName, err)
		}
		m.boundTools[i].object = obj
		return obj, true, nil
	}
	return obj, true, nil
}

// rebindBoundTool replaces the object for typeName's binding with newObj — the
// same-type-return state transition. It mutates in place under the lock; step()
// then persists the transition as a withTools selector on the LLM's ID.
func (m *MCP) rebindBoundTool(typeName string, newObj dagql.AnyObjectResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.continuation.Self() != nil {
		// See errContinuationAdopted: the rebind would be dropped.
		return errContinuationAdopted
	}
	for i, b := range m.boundTools {
		if b.typeName() == typeName {
			// A state transition changes the value, not its composition owner or
			// the module revision the binding was composed from. Select may have
			// wrapped the returned value using the caller's older same-named class.
			newObj, err := b.objType.New(newObj)
			if err != nil {
				return fmt.Errorf("rebind object of type %q: %w", typeName, err)
			}
			id, _ := newObj.ID()
			m.boundTools[i].object = newObj
			m.boundTools[i].id = id
			m.stateChanged = true
			return nil
		}
	}
	return nil
}

// boundToolBinding is a flattened snapshot of a binding: the object's ID plus its
// except list and owner, enough for step() to rebuild a withTools selector.
type boundToolBinding struct {
	ID      *call.ID
	Except  []string
	Owner   string
	Version int
}

// BoundToolBindings snapshots the current bindings' IDs and except lists so
// step() can detect a state transition (an object rebind) and persist it, the
// same way it persists a workspace overlay via withWorkspace. Uses each
// binding's recorded ID directly (without loading a lazy binding), so snapshotting
// a restored session never forces evaluation.
func (m *MCP) BoundToolBindings() ([]boundToolBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]boundToolBinding, 0, len(m.boundTools))
	for _, b := range m.boundTools {
		id := b.id
		if id == nil && b.object != nil {
			var err error
			id, err = b.object.ID()
			if err != nil {
				return nil, err
			}
		}
		out = append(out, boundToolBinding{ID: id, Except: slices.Clone(b.Except), Owner: b.Owner, Version: b.Version})
	}
	return out, nil
}

// bindWorkspaceModuleTools binds each served workspace module's main object as
// a toolset, constructing it through the canonical server (which serves module
// constructors even for entrypoint modules, whose sugared schema only carries
// proxies). `dagger mcp` uses this so a workspace module's methods are served
// as MCP tools without an explicit withTools: the module entrypoint is "the
// way in" (hack/designs/workspace-agents.md). Modules whose constructors
// require arguments (beyond the auto-injected Workspace) are skipped — there
// is no one to prompt for them.
func (m *MCP) bindWorkspaceModuleTools(ctx context.Context) (*MCP, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	served, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}
	srv, err := served.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("served schema: %w", err)
	}
	canonical := srv.Canonical()
	for _, primary := range served.PrimaryMods() {
		mod := primary.ModuleResult().Self()
		if mod == nil || mod.Name() == ModuleName {
			continue
		}
		ctorName := gqlFieldName(mod.Name())
		spec, ok := canonical.Root().ObjectType().FieldSpec(ctorName, canonical.View)
		if !ok {
			continue
		}
		constructible := true
		for _, arg := range spec.Args.Inputs(canonical.View) {
			if arg.Internal || arg.Default != nil {
				continue
			}
			if !arg.Type.Type().NonNull {
				continue
			}
			if inputSpecIsWorkspace(arg) {
				// Supplied from the workspace in scope, not by the caller.
				continue
			}
			constructible = false
			break
		}
		if !constructible {
			continue
		}
		var obj dagql.AnyObjectResult
		if err := canonical.Select(ctx, canonical.Root(), &obj, dagql.Selector{
			View:  canonical.View,
			Field: ctorName,
		}); err != nil {
			return nil, fmt.Errorf("construct workspace module %q: %w", mod.Name(), err)
		}
		m = m.WithTools(obj, canonical.Schema(), nil)
	}
	return m, nil
}

// loadObjectTools registers one tool per eligible method of each bound object.
// It is called before the MCP/skill/builtin tools so a bound method overrides a
// builtin of the same name. When a tool name is contributed by more than one
// bound object, ALL tools of every object involved are served under namespaced
// names instead (see namespacedTypes) — nothing is silently shadowed.
func (m *MCP) loadObjectTools(_ context.Context, srv *dagql.Server, allTools *LLMToolSet) error {
	toolsets, err := m.boundToolsets(srv)
	if err != nil {
		return err
	}
	namespaced := namespacedTypes(toolsets)
	for _, ts := range toolsets {
		for _, t := range ts.tools {
			if namespaced[ts.typeName] {
				t.Name = namespacedToolName(ts.typeName, t.Name)
			}
			allTools.Add(t)
		}
	}
	return nil
}

// bindingToolset is one binding's generated tools plus the bound object's type
// name — the unit of collision-driven namespacing.
type bindingToolset struct {
	typeName string
	tools    []LLMTool
}

// boundToolsets generates each binding's tools, in binding order.
func (m *MCP) boundToolsets(srv *dagql.Server) ([]bindingToolset, error) {
	m.mu.Lock()
	bindings := slices.Clone(m.boundTools)
	m.mu.Unlock()
	if len(bindings) == 0 {
		return nil, nil
	}
	toolsets := make([]bindingToolset, 0, len(bindings))
	for _, b := range bindings {
		tools, err := m.toolsForBoundObject(srv, b)
		if err != nil {
			return nil, err
		}
		toolsets = append(toolsets, bindingToolset{typeName: b.typeName(), tools: tools})
	}
	return toolsets, nil
}

// namespacedToolName qualifies a tool name with its bound object's type, e.g.
// TuiQa's `start` becomes `tuiQa_start` — the type rendered as its GraphQL
// field name, matching how the module is spelled elsewhere in the API.
func namespacedToolName(typeName, toolName string) string {
	return gqlFieldName(typeName) + "_" + toolName
}

// namespacedTypes decides which bound-object types must serve their tools under
// namespaced names: a tool name contributed by more than one binding namespaces
// ALL tools of every binding involved, so each conflicting toolset stays
// uniform (either every tool bare, or every tool prefixed) and no tool is
// silently shadowed. Bindings with no collisions keep bare names — the common
// case stays terse. Runs to a fixpoint, since a namespaced name can itself
// collide with another binding's bare tool name; the namespaced set only
// grows, so this terminates within len(toolsets) rounds.
func namespacedTypes(toolsets []bindingToolset) map[string]bool {
	namespaced := map[string]bool{}
	for {
		// served name -> the set of bound types contributing a tool under it
		contributors := map[string]map[string]bool{}
		for _, ts := range toolsets {
			for _, t := range ts.tools {
				name := t.Name
				if namespaced[ts.typeName] {
					name = namespacedToolName(ts.typeName, name)
				}
				if contributors[name] == nil {
					contributors[name] = map[string]bool{}
				}
				contributors[name][ts.typeName] = true
			}
		}
		changed := false
		for _, types := range contributors {
			if len(types) < 2 {
				continue
			}
			for typeName := range types {
				if !namespaced[typeName] {
					namespaced[typeName] = true
					changed = true
				}
			}
		}
		if !changed {
			return namespaced
		}
	}
}

// ToolNameCollisions reports, per bare tool name, the bound-object type names
// that each contribute a tool of that name — but only for names contributed by
// more than one bound object. Such a collision makes loadObjectTools serve ALL
// tools of every object involved under namespaced names (namespacedToolName);
// the report lets callers surface the renaming when composing several agents'
// toolsets onto one LLM (hack/designs/workspace-agents.md §3).
func (m *MCP) ToolNameCollisions(ctx context.Context) (map[string][]string, error) {
	srv, err := m.baseServer(ctx)
	if err != nil {
		return nil, err
	}
	toolsets, err := m.boundToolsets(srv)
	if err != nil {
		return nil, err
	}

	contributors := map[string][]string{}
	for _, ts := range toolsets {
		for _, t := range ts.tools {
			contributors[t.Name] = append(contributors[t.Name], ts.typeName)
		}
	}

	collisions := map[string][]string{}
	for name, types := range contributors {
		if len(types) > 1 {
			collisions[name] = types
		}
	}
	return collisions, nil
}

// toolsForBoundObject generates the tools for a single bound object: one per
// eligible field of its schema type.
func (m *MCP) toolsForBoundObject(srv *dagql.Server, b boundTool) ([]LLMTool, error) {
	typeName := b.typeName()
	toolSchema := b.definingSchema
	if toolSchema == nil {
		return nil, fmt.Errorf("bound object type %q has no defining schema", typeName)
	}
	def := toolSchema.Types[typeName]
	if def == nil || (def.Kind != ast.Object && def.Kind != ast.Interface) {
		return nil, fmt.Errorf("bound object type %q is not an object in its defining schema", typeName)
	}
	implicit := m.implicitArgs()
	var tools []LLMTool
	for _, field := range def.Fields {
		if !objectToolEligible(field, b.Except, implicit) {
			continue
		}
		methodSchema, err := objectMethodSchema(toolSchema, field, implicit)
		if err != nil {
			return nil, fmt.Errorf("build schema for %s.%s: %w", typeName, field.Name, err)
		}
		retType := field.Type.Name()
		tools = append(tools, LLMTool{
			Name:        field.Name,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      methodSchema,
			// A pure method may run concurrently with its pure neighbors in a
			// batch; any other is a sequential step, run alone in the position
			// it was written (see MCP.CallBatch). A method changes the agent's
			// state when it returns the bound object's own type, a Workspace, a
			// Changeset, or an LLM — the conversation itself, which the calls
			// written after it in the batch run on (see toolDispatch). A module
			// function cached with policy Never, or a core field marked
			// DoNotCache, is impure too: side effects and live reads are
			// exactly what those are for.
			ReadOnly: retType != typeName &&
				retType != "Changeset" &&
				retType != workspaceTypeName &&
				retType != llmTypeName &&
				!b.impure(field.Name, srv),
			ReturnsLLM: retType == llmTypeName,
			Call:       m.callObjectMethod(srv, typeName, field),
			Server:     typeName,
		})
	}
	return tools, nil
}

// objectToolEligible reports whether a field becomes a tool: it must not be in
// except, must not be an internal/reserved field, and every REQUIRED argument
// must be expressible without an object handle — a required object-typed arg
// (other than those implicit supplies) disqualifies it, since the model has no
// handle to pass. Exception: a required arg of a LIFTABLE type (see
// liftableObjectArg) does not disqualify — the model can supply an address
// string, lifted into the object at dispatch time via the core Address API.
func objectToolEligible(field *ast.FieldDefinition, except []string, implicit implicitToolArgs) bool {
	if slices.Contains(except, field.Name) {
		return false
	}
	if strings.HasPrefix(field.Name, "_") {
		return false
	}
	if field.Name == "id" || field.Name == "sync" {
		return false
	}
	if field.Directives.ForName("deprecated") != nil {
		return false
	}
	// An @agent method is the composition entrypoint (base: LLM!): LLM!; it is
	// never itself a tool, so hide it without requiring authors to add it to
	// `except` by hand.
	if field.Directives.ForName("agent") != nil {
		return false
	}
	for _, arg := range field.Arguments {
		if implicit.supplies(arg) {
			// MCP supplies this argument; the model does not need an object handle.
			continue
		}
		required := arg.Type.NonNull && arg.DefaultValue == nil
		if required && isObjectArg(arg) {
			if _, ok := liftableObjectArg(arg); !ok {
				return false
			}
		}
	}
	return true
}

// isObjectArg reports whether an argument is a Dagger object, which crosses the
// wire as an `ID` scalar carrying an @expectedType directive.
func isObjectArg(arg *ast.ArgumentDefinition) bool {
	return arg.Directives.ForName("expectedType") != nil
}

// addressableType describes an object type the core Address API loads: how a
// plain address string supplied for a tool arg of the type resolves into the
// object, which forms the model may supply, and how to document them.
type addressableType struct {
	// addressField is the Address field that loads the type:
	// Workspace.resolve(value: <addr>).<addressField> — the same lifting the CLI
	// performs for object-typed flags (internal/cmd/dagger/flags.go), see
	// core/schema/address.go.
	addressField string
	// external vets a model-supplied address that is not a dag:// address
	// before it reaches the Address decoder: it returns an error for any form
	// that would reach the calling client's host. nil refuses every external
	// form. dag:// addresses are always accepted: they name workspace or
	// bound tool artifacts, never the host.
	external func(addr string) error
	// hint documents the accepted address syntaxes; the tool schema renders
	// it as "(<Type> address: <hint>)" prefixed to the arg's own docstring,
	// so each type carries its own syntax examples.
	hint string
}

// dagAddressHint documents DAG addresses in every hint. A dag:// address
// names a value by its path from a module's main object, with a key for each
// collection on the way. It resolves in the conversation's scope
// (LLM.artifacts): a module bound as one of this conversation's tools is
// addressed with its live state; any other module is constructed fresh from
// the workspace.
const dagAddressHint = `a dag:// address to a value of one of your tool modules (with its current state) or of a workspace module, like "dag://<module>/<field>/<field>?<item>=<key>" (` + findArtifactsPointer + `)`

// findArtifactsPointer points the model at the FindArtifacts builtin, which
// lists the scope that DAG addresses resolve in.
const findArtifactsPointer = "FindArtifacts lists what exists"

// selectionAddressHint documents the DAG address vocabulary for the
// artifact selection types, which take a selection rather than a path to one
// value.
const selectionAddressHint = `a DAG address selecting artifacts of your tool modules (with their current state) and of workspace modules (` + findArtifactsPointer + `). The "dag://" scheme is optional. The path may be a glob: "go/**" selects everything under go, "{app,docs}/*" the children of both; "?<dimension>=<key>" pins collection items, like "go/test?go-test=TestX"; "dag+<type>://" keeps artifacts of that type, like "dag+check://go/**"`

// selectionTypes are the artifact selection types a tool arg lifts from a
// DAG address, besides the addressable types, with their hints: the address
// filters the conversation's scope (LLM.artifacts), and the arg's type
// decides the arity — "arity from the sink" (hack/designs/module-wiring.md).
// An Artifacts arg takes the whole selection; an Artifact arg takes the one
// artifact it must select. A selection only names artifacts, so no form of it
// reaches the calling client's host.
var selectionTypes = map[string]string{
	"Artifacts": selectionAddressHint + `. Or an Artifacts ID from a prior tool result`,
	"Artifact":  selectionAddressHint + `. It must select exactly one artifact. Or an Artifact ID from a prior tool result`,
}

// remoteGitSchemes are the git URL schemes a model may supply. The Address
// git decoders read anything else from the calling client's host: a path is a
// local directory or repository, and an ssh:// or scp-style URL borrows the
// client's SSH agent socket.
var remoteGitSchemes = []string{gitutil.HTTPSProtocol, gitutil.HTTPProtocol, gitutil.GitProtocol}

// remoteGitURL accepts only a git URL with one of the remoteGitSchemes.
func remoteGitURL(addr string) error {
	u, err := gitutil.ParseURL(addr)
	if err == nil && slices.Contains(remoteGitSchemes, u.Scheme) {
		return nil
	}
	return errors.New("only an https://, http:// or git:// git URL or a dag:// address is accepted; local paths and ssh URLs would reach the calling client's host")
}

// addressableTypes lists every object type with an Address.<field> loader in
// core/schema/address.go, except Workspace: a Workspace arg is always filled
// by MCP from the bound workspace (implicitToolArgs.supplies), so the model
// never supplies one. Which of these a model may lift is decided by
// unliftableTypes, and which forms by each type's external check.
var addressableTypes = map[string]addressableType{
	"Container": {
		addressField: "container",
		// Image refs pull from registries on the engine's network; the
		// decoder has no host fallback.
		external: func(string) error { return nil },
		hint:     `an image ref like "golang:1.26", ` + dagAddressHint + `, or a Container ID from a prior tool result`,
	},
	"Directory": {
		addressField: "directory",
		external:     remoteGitURL,
		hint:         `an https:// or git:// git URL with an optional #<ref>:<subdir> like "https://github.com/org/repo#main:docs", ` + dagAddressHint + `, or a Directory ID from a prior tool result`,
	},
	"File": {
		addressField: "file",
		external:     remoteGitURL,
		hint:         `an https:// or git:// git URL with #<ref>:<path> like "https://github.com/org/repo#main:README.md", ` + dagAddressHint + `, or a File ID from a prior tool result`,
	},
	"GitRef": {
		addressField: "gitRef",
		external:     remoteGitURL,
		hint:         `an https:// or git:// git URL with an optional #<branch, tag or commit> like "https://github.com/org/repo#main" (the default branch without one), ` + dagAddressHint + `, or a GitRef ID from a prior tool result`,
	},
	"GitRepository": {
		addressField: "gitRepository",
		external:     remoteGitURL,
		hint:         `an https:// or git:// git URL without a #ref like "https://github.com/org/repo", ` + dagAddressHint + `, or a GitRepository ID from a prior tool result`,
	},
	"Service": {
		addressField: "service",
		// No external form: tcp:// and udp:// addresses tunnel to a port on
		// the calling client's host (Host.service).
		hint: dagAddressHint + `, or a Service ID from a prior tool result`,
	},
	"Secret": {addressField: "secret"},
	"Socket": {addressField: "socket"},
	"Volume": {addressField: "volume"},
}

// unliftableTypes is the blocklist of addressable types whose tool args do
// NOT accept a plain address string: an ID from a prior tool result is the
// only way to pass one. Lifting is a CAPABILITY decision, not a convenience
// one: a CLI flag is human-typed, but a tool arg is MODEL-typed, so an
// address the model can guess must not mint a capability it was not handed.
// These three decoders do exactly that — Address.secret reads env://,
// file:// and op:// URIs into secrets, Address.socket forwards a host
// socket, and Address.volume mounts sshfs:// endpoints or engine volumes.
//
// The other types lift, but a model-supplied string never reaches the
// calling client's host: each type's external check admits only forms whose
// decoding stays off the host (image refs, remote git URLs), besides dag://
// addresses. The CLI's host fallbacks — local paths for Directory/File,
// local repositories and SSH agents for the git types, host tunnels for
// Service — are refused before the Address decoder runs.
var unliftableTypes = map[string]bool{
	"Secret": true,
	"Socket": true,
	"Volume": true,
}

// checkLiftableAddress refuses a model-supplied address for a liftable type
// unless it is a dag:// address or an external form the type admits (see
// addressableType.external).
func checkLiftableAddress(typeName, addr string) error {
	if dagaddress.IsAddress(addr) {
		return nil
	}
	check := addressableTypes[typeName].external
	if check == nil {
		return errors.New("only a dag:// address is accepted; other addresses would reach the calling client's host")
	}
	return check(addr)
}

// liftableObjectArg returns the @expectedType name of an object-typed
// argument when that type is liftable: either resolvable from an address
// string via the core Address API (addressableTypes) AND not in the
// unliftableTypes capability blocklist, or an artifact selection type
// (selectionTypes). Lists of IDs ([ID!]! with @expectedType) qualify too:
// each element is lifted on its own (see MCP.liftArgValue).
func liftableObjectArg(arg *ast.ArgumentDefinition) (string, bool) {
	if arg.Type == nil || namedElemType(arg.Type).NamedType != "ID" {
		return "", false
	}
	d := arg.Directives.ForName("expectedType")
	if d == nil {
		return "", false
	}
	name := d.Arguments.ForName("name")
	if name == nil || name.Value == nil {
		return "", false
	}
	if _, ok := selectionTypes[name.Value.Raw]; ok {
		return name.Value.Raw, true
	}
	if _, ok := addressableTypes[name.Value.Raw]; !ok || unliftableTypes[name.Value.Raw] {
		return "", false
	}
	return name.Value.Raw, true
}

// namedElemType unwraps list types down to their innermost named type.
func namedElemType(t *ast.Type) *ast.Type {
	for t.Elem != nil {
		t = t.Elem
	}
	return t
}

// liftHint documents the address syntaxes a liftable type accepts.
func liftHint(typeName string) string {
	if hint, ok := selectionTypes[typeName]; ok {
		return hint
	}
	return addressableTypes[typeName].hint
}

// implicitToolArgs decides which object-tool arguments MCP supplies itself,
// hidden from the tool schema, rather than asking the model for them. This
// classification affects only the generated MCP tool and never rewrites the
// module's GraphQL schema.
//
// Workspace is always contextual: it is filled from the bound workspace. LLM is
// filled from the conversation dispatching the call, which only exists while
// an LLM drives the tools (LLM.step); a standalone server (dagger mcp) has
// none. There an LLM argument is an ordinary object argument the caller cannot
// satisfy: a required one disqualifies its method (objectToolEligible), and an
// optional one is exposed by ID like any other object, so it can at least be
// left unset. Agent is filled from the agent loop dispatching the call (see
// AgentFromContext), which likewise only exists under a conversation, so it
// follows the same policy as LLM.
type implicitToolArgs struct {
	// llm reports whether a conversation is available to fill LLM and Agent
	// arguments.
	llm bool
}

var (
	// conversationToolArgs is the policy of tools driven by an LLM.
	conversationToolArgs = implicitToolArgs{llm: true}
	// standaloneToolArgs is the policy of tools served without a conversation.
	standaloneToolArgs = implicitToolArgs{}
)

// supplies reports whether MCP fills arg itself under this policy.
func (p implicitToolArgs) supplies(arg *ast.ArgumentDefinition) bool {
	return isExpectedTypeArg(arg, workspaceTypeName) ||
		(p.llm && (isExpectedTypeArg(arg, llmTypeName) ||
			isExpectedTypeArg(arg, agentTypeName)))
}

// implicitArgs returns the policy this MCP serves tools under: standalone when
// no conversation will ever drive it (see MCP.Standalone), else conversation.
func (m *MCP) implicitArgs() implicitToolArgs {
	if m == nil || m.standalone {
		return standaloneToolArgs
	}
	return conversationToolArgs
}

func isExpectedTypeArg(arg *ast.ArgumentDefinition, typeName string) bool {
	d := arg.Directives.ForName("expectedType")
	if d == nil {
		return false
	}
	name := d.Arguments.ForName("name")
	return name != nil && name.Value != nil && name.Value.Raw == typeName
}

// objectMethodSchema builds a tool's JSON-schema parameters from a field's
// visible arguments — its scalars, enums, lists, and input objects — omitting
// the arguments MCP supplies itself (implicit). Object arguments render as ID
// strings annotated with their expected type; liftable object arguments
// (required or optional) render as address strings instead, with the type's
// syntax hint.
func objectMethodSchema(schema *ast.Schema, field *ast.FieldDefinition, implicit implicitToolArgs) (map[string]any, error) {
	properties := map[string]any{}
	var required []string
	for _, arg := range field.Arguments {
		if implicit.supplies(arg) {
			continue
		}
		argSchema, err := argTypeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}
		desc := arg.Description
		if d := arg.Directives.ForName("expectedType"); d != nil {
			if name := d.Arguments.ForName("name"); name != nil && name.Value != nil {
				// A liftable arg accepts an address string (per the type's
				// hint — see addressableTypes and selectionTypes) as well as
				// an ID from a previous tool result; anything else only
				// accepts an ID.
				prefix := fmt.Sprintf("(%s ID)", name.Value.Raw)
				if typeName, ok := liftableObjectArg(arg); ok {
					if arg.Type.Elem != nil {
						prefix = fmt.Sprintf("(list of %s addresses, each %s)", typeName, liftHint(typeName))
					} else {
						prefix = fmt.Sprintf("(%s address: %s)", typeName, liftHint(typeName))
					}
				}
				if desc == "" {
					desc = prefix
				} else {
					desc = prefix + " " + desc
				}
			}
		}
		if desc != "" {
			argSchema["description"] = desc
		}
		if arg.DefaultValue != nil {
			val, err := arg.DefaultValue.Value(nil)
			if err != nil {
				return nil, fmt.Errorf("default value for %q: %w", arg.Name, err)
			}
			argSchema["default"] = val
		}
		properties[arg.Name] = argSchema
		if arg.Type.NonNull && arg.DefaultValue == nil {
			required = append(required, arg.Name)
		}
	}
	jsonSchema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		jsonSchema["required"] = required
	}
	return jsonSchema, nil
}

// argTypeToJSONSchema converts a GraphQL argument type to a JSON-schema fragment.
// It resurrects the pre-Dang arg→schema conversion, scoped to a single argument.
func argTypeToJSONSchema(schema *ast.Schema, t *ast.Type) (map[string]any, error) {
	jsonSchema := map[string]any{}
	if t.Elem != nil {
		jsonSchema["type"] = "array"
		items, err := argTypeToJSONSchema(schema, t.Elem)
		if err != nil {
			return nil, fmt.Errorf("elem type: %w", err)
		}
		jsonSchema["items"] = items
	} else {
		switch t.NamedType {
		case "Int":
			jsonSchema["type"] = "integer"
		case "Float":
			jsonSchema["type"] = "number"
		case "String", "ID":
			jsonSchema["type"] = "string"
		case "Boolean":
			jsonSchema["type"] = "boolean"
		default:
			typeDef, found := schema.Types[t.NamedType]
			if !found {
				return nil, fmt.Errorf("unknown type: %q", t.NamedType)
			}
			switch typeDef.Kind {
			case ast.InputObject:
				jsonSchema["type"] = "object"
				properties := map[string]any{}
				for _, f := range typeDef.Fields {
					fieldSpec, err := argTypeToJSONSchema(schema, f.Type)
					if err != nil {
						return nil, fmt.Errorf("field %q type: %w", f.Name, err)
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
				jsonSchema["type"] = "string"
			default:
				return nil, fmt.Errorf("unhandled type: %s (%s)", t, typeDef.Kind)
			}
		}
	}
	// GraphQL nullability applies at every type boundary, including list
	// elements. Keep the concrete schema intact so enums and nested objects still
	// constrain non-null values, and add null as a separate valid alternative.
	if !t.NonNull {
		return map[string]any{
			"anyOf": []any{
				jsonSchema,
				map[string]any{"type": "null"},
			},
		}, nil
	}
	return jsonSchema, nil
}

// callObjectMethod returns the tool implementation for one method of a bound
// object. It selects the method on the CURRENT bound object (so an earlier
// same-batch state update is visible), relying on the bound Workspace already
// threaded into ctx by MCP.Call so Workspace-typed args auto-inject, then routes
// the result by type.
func (m *MCP) callObjectMethod(srv *dagql.Server, typeName string, field *ast.FieldDefinition) LLMToolFunc {
	return func(ctx context.Context, rawArgs any) (any, error) {
		args, ok := rawArgs.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments type: %T", rawArgs)
		}
		recv, ok, err := m.boundToolObject(ctx, srv, typeName)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("no object of type %q is bound", typeName)
		}
		sel, err := m.buildObjectMethodSelector(ctx, srv, recv.ObjectType(), field, args)
		if err != nil {
			return nil, err
		}
		var val dagql.AnyResult
		// The method call is the real user-facing work of the tool call, not
		// engine bookkeeping: don't let Select mark it internal, so its spans
		// (and the logs beneath them) surface in the UI and in toolLogs.
		if err := srv.Select(dagql.WithNonInternalTelemetry(ctx), recv, &val, sel); err != nil {
			return nil, err
		}
		return m.routeObjectMethodResult(ctx, srv, typeName, val)
	}
}

// buildObjectMethodSelector converts the model's tool arguments into a complete
// selector for the module method. Workspace retains its contextual handling;
// MCP directly adds the current LLM and calling Agent for hidden arguments.
// These are explicit selector arguments, not GraphQL defaults or dynamic inputs.
// An object-typed argument of a liftable type (see liftableObjectArg)
// additionally accepts an address string: when the value fails to decode as an
// ID, it is lifted into the object (see MCP.liftObjectArg) and the resulting
// object's ID is used instead — the same lifting the CLI performs for object
// flags (internal/cmd/dagger/flags.go), except that a relative dag:// address
// resolves in the conversation's scope (LLM.artifacts), bound tools' live
// state included, and an Artifacts or Artifact arg takes a selection of it.
// ctx and srv are the session's, so other addresses resolve against the
// workspace client schema with all installed modules visible.
func (m *MCP) buildObjectMethodSelector(ctx context.Context, srv *dagql.Server, recvType dagql.ObjectType, astField *ast.FieldDefinition, args map[string]any) (dagql.Selector, error) {
	fieldName := astField.Name
	sel := dagql.Selector{View: srv.View, Field: fieldName}
	field, ok := recvType.FieldSpec(fieldName, srv.View)
	if !ok {
		return sel, fmt.Errorf("field %q not found on %q", fieldName, recvType.TypeName())
	}
	provided := maps.Clone(args)
	for _, arg := range field.Args.Inputs(srv.View) {
		if arg.Internal {
			continue
		}
		val, ok := args[arg.Name]
		if !ok {
			implicit, found, err := m.implicitToolInput(ctx, astField, arg)
			if err != nil {
				return sel, fmt.Errorf("arg %q: %w", arg.Name, err)
			}
			if found {
				sel.Args = append(sel.Args, dagql.NamedInput{Name: arg.Name, Value: implicit})
			} else if wsInput, ok := boundWorkspaceInput(ctx, srv, arg); ok {
				sel.Args = append(sel.Args, dagql.NamedInput{Name: arg.Name, Value: wsInput})
			}
			continue
		}
		delete(provided, arg.Name)
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			// Not a valid ID: for a liftable object arg, fall back to
			// interpreting the string as an address.
			lifted, ok, liftErr := m.liftObjectArg(ctx, srv, astField, arg, val, err)
			if liftErr != nil {
				return sel, fmt.Errorf("arg %q: %w", arg.Name, liftErr)
			}
			if !ok {
				return sel, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
			}
			input = lifted
		}
		sel.Args = append(sel.Args, dagql.NamedInput{Name: arg.Name, Value: input})
	}
	if len(provided) > 0 {
		unknown := make([]string, 0, len(provided))
		for k := range provided {
			unknown = append(unknown, k)
		}
		slices.Sort(unknown)
		return sel, fmt.Errorf("unknown arguments: %s", strings.Join(unknown, ", "))
	}
	return sel, nil
}

// implicitToolInput fills the two runtime-local arguments supported by the MCP
// object-tool adapter. These values are ordinary explicit selector arguments:
// module function schemas and general DAGQL input resolution know nothing about
// this convention. An argument the policy does not supply (see
// implicitToolArgs) is reported not found, so it is left to the caller like any
// other object argument.
func (m *MCP) implicitToolInput(ctx context.Context, astField *ast.FieldDefinition, spec dagql.InputSpec) (dagql.Input, bool, error) {
	astArg := astField.Arguments.ForName(spec.Name)
	if astArg == nil {
		return nil, false, nil
	}

	var obj dagql.IDable
	switch {
	case isExpectedTypeArg(astArg, llmTypeName):
		if !m.implicitArgs().llm {
			return nil, false, nil
		}
		llm := m.currentLLM()
		if llm.Self() == nil {
			return nil, true, errors.New("function requires the current conversation; invoke it as an LLM tool")
		}
		obj = llm
	case isExpectedTypeArg(astArg, agentTypeName):
		if !m.implicitArgs().llm {
			return nil, false, nil
		}
		agent, ok := AgentFromContext(ctx)
		if !ok {
			return nil, true, errors.New("function requires the calling agent; invoke it from an agent loop (LLM.spawn) — synchronous loop support is planned")
		}
		obj = agent
	default:
		return nil, false, nil
	}

	id, err := obj.ID()
	if err != nil {
		return nil, true, fmt.Errorf("get %s ID: %w", astArg.Directives.ForName("expectedType").Arguments.ForName("name").Value.Raw, err)
	}
	encoded, err := id.Encode()
	if err != nil {
		return nil, true, fmt.Errorf("encode ID: %w", err)
	}
	input, err := spec.Type.Decoder().DecodeInput(encoded)
	if err != nil {
		return nil, true, fmt.Errorf("decode ID: %w", err)
	}
	return input, true, nil
}

// liftObjectArg resolves an address string supplied for a liftable
// object-typed argument into that object's ID:
//
//   - For an artifact selection type (selectionTypes), the string is a DAG
//     address, with or without the dag:// scheme, filtering the
//     conversation's scope: an Artifacts arg takes the selection, an Artifact
//     arg the one artifact it must select (see MCP.liftScopeSelection).
//   - A relative dag:// address names one value in the conversation's scope
//     — its bound tools with their live state, and its workspace (see
//     MCP.resolveScopeObject).
//   - An absolute dag:// address resolves in the conversation's bound
//     workspace only (see MCP.resolveBoundWorkspaceAddress); with none bound
//     it is refused.
//   - Any other address — an image ref, a remote git URL — selects
//     Workspace.resolve(value: <addr>).<addressField> on the session server,
//     once checkLiftableAddress has vetted it.
//
// A list arg lifts element-wise (see MCP.liftArgValue): each element may be
// an address or an ID from a prior tool result.
//
// The resulting objects' IDs are re-encoded through the argument's own decoder
// so the input matches whatever ID type the field expects (including
// optional-wrapped IDs and lists). Returns ok=false — without an error — when
// the argument is not a liftable object arg or the value does not have its
// shape, so the caller surfaces the original ID decode error instead. idErr
// is that original error, folded into the message when address resolution
// also fails (unless the value is plainly an address rather than an ID).
func (m *MCP) liftObjectArg(ctx context.Context, srv *dagql.Server, astField *ast.FieldDefinition, spec dagql.InputSpec, val any, idErr error) (dagql.Input, bool, error) {
	astArg := astField.Arguments.ForName(spec.Name)
	if astArg == nil {
		return nil, false, nil
	}
	typeName, ok := liftableObjectArg(astArg)
	if !ok {
		return nil, false, nil
	}
	lifted, ok, err := m.liftArgValue(ctx, srv, typeName, astArg.Type, val, idErr)
	if err != nil || !ok {
		return nil, false, err
	}
	input, err := spec.Type.Decoder().DecodeInput(lifted)
	if err != nil {
		return nil, false, fmt.Errorf("decode lifted %s: %w", typeName, err)
	}
	return input, true, nil
}

// liftArgValue lifts a model-supplied value of a liftable arg of type t into
// its wire form: an address string becomes the encoded ID of the object it
// resolves to (see MCP.liftAddress). For a list type, every element is lifted
// on its own — an element that already decodes as an ID is kept as is, so
// IDs from prior tool results and addresses mix freely. Returns ok=false when
// the value does not have the shape of t (a non-string, or a non-list for a
// list type), so the caller surfaces the original decode error.
func (m *MCP) liftArgValue(ctx context.Context, srv *dagql.Server, typeName string, t *ast.Type, val any, idErr error) (any, bool, error) {
	if t.Elem == nil {
		addr, ok := val.(string)
		if !ok {
			return nil, false, nil
		}
		encoded, err := m.liftAddress(ctx, srv, typeName, addr, idErr)
		if err != nil {
			return nil, false, err
		}
		return encoded, true, nil
	}
	elems, ok := val.([]any)
	if !ok {
		return nil, false, nil
	}
	lifted := make([]any, len(elems))
	for i, elem := range elems {
		var elemErr error
		switch x := elem.(type) {
		case nil:
			// Left to the decoder: a null element is valid only when the
			// element type is nullable.
			continue
		case string:
			var id call.ID
			if elemErr = id.Decode(x); elemErr == nil {
				lifted[i] = x
				continue
			}
		}
		v, ok, err := m.liftArgValue(ctx, srv, typeName, t.Elem, elem, elemErr)
		if err != nil {
			return nil, false, fmt.Errorf("element %d: %w", i, err)
		}
		if !ok {
			return nil, false, nil
		}
		lifted[i] = v
	}
	return lifted, true, nil
}

// liftAddress resolves one address string for a liftable type (see
// MCP.liftObjectArg) and returns the encoded ID of the resulting object.
func (m *MCP) liftAddress(ctx context.Context, srv *dagql.Server, typeName, addr string, idErr error) (string, error) {
	var obj dagql.AnyObjectResult
	var err error
	if _, selection := selectionTypes[typeName]; selection {
		obj, err = m.liftScopeSelection(ctx, srv, addr, typeName == "Artifact")
		if err != nil {
			return "", fmt.Errorf("%q is not a resolvable %s address: %w; %s", addr, typeName, err, findArtifactsPointer)
		}
	} else {
		parsed, isDAG, parseErr := parseDAGAddress(addr)
		switch {
		case isDAG && parseErr != nil:
			return "", fmt.Errorf("%q is not a resolvable %s address: %w", addr, typeName, parseErr)
		case isDAG && !parsed.Absolute:
			obj, err = m.resolveScopeObject(ctx, srv, parsed, addr, typeName)
			if err != nil {
				return "", fmt.Errorf("%q is not a resolvable %s address: %w; %s", addr, typeName, err, findArtifactsPointer)
			}
		case isDAG:
			obj, err = m.resolveBoundWorkspaceAddress(ctx, srv, addr, addressableTypes[typeName].addressField)
			if err != nil {
				return "", fmt.Errorf("%q is not a resolvable %s address: %w", addr, typeName, err)
			}
		default:
			// A model-typed string must never reach the calling client's
			// host, whatever the Address decoder would fall back to for a
			// human.
			if err := checkLiftableAddress(typeName, addr); err != nil {
				return "", fmt.Errorf("%q is not a %s ID or an accepted %s address: %w", addr, typeName, typeName, err)
			}
			obj, err = resolveObjectAddress(WithAgentAddressResolution(ctx), srv, addr, addressableTypes[typeName].addressField)
			if err != nil {
				if dagaddress.IsAddress(addr) {
					// Plainly not an ID: the decode error would only be noise.
					return "", fmt.Errorf("%q is not a resolvable %s address: %w", addr, typeName, err)
				}
				return "", fmt.Errorf("%q is neither a %s ID (%w) nor a resolvable %s address: %w",
					addr, typeName, idErr, typeName, err)
			}
		}
	}
	objID, err := obj.ID()
	if err != nil {
		return "", fmt.Errorf("get %s ID for address %q: %w", typeName, addr, err)
	}
	encoded, err := objID.Encode()
	if err != nil {
		return "", fmt.Errorf("encode %s ID for address %q: %w", typeName, addr, err)
	}
	return encoded, nil
}

// parseDAGAddress parses a dag:// address. isDAG reports whether the value
// has the scheme at all; err, whether such a value is malformed. A relative
// address names a value in the conversation's scope; an absolute one names a
// workspace, which must be the conversation's own.
func parseDAGAddress(addr string) (parsed *dagaddress.Address, isDAG bool, err error) {
	if !dagaddress.IsAddress(addr) {
		return nil, false, nil
	}
	parsed, err = dagaddress.Parse(addr)
	return parsed, true, err
}

// resolveBoundWorkspaceAddress resolves an absolute dag:// address in the
// conversation's bound workspace, and only there:
// Workspace.resolve(value: <addr>).<addressField>, which refuses an address
// naming another workspace. With no bound workspace it is refused: the
// calling client's current workspace is not part of the conversation.
func (m *MCP) resolveBoundWorkspaceAddress(ctx context.Context, srv *dagql.Server, addr, addressField string) (dagql.AnyObjectResult, error) {
	ws := m.workspace
	if ws.Self() == nil {
		return nil, errors.New("an absolute dag:// address names a workspace, and none is bound to this conversation")
	}
	srv = srv.Canonical()
	workspaceType, ok := srv.ObjectType("Workspace")
	if ok {
		_, ok = workspaceType.FieldSpec("resolve", srv.View)
	}
	if !ok {
		return nil, errors.New("this schema view cannot resolve dag:// addresses")
	}
	// User-facing work of the tool call, as in resolveObjectAddress.
	var obj dagql.AnyObjectResult
	err := srv.Select(dagql.WithNonInternalTelemetry(ctx), ws, &obj,
		dagql.Selector{View: srv.View, Field: "resolve", Args: []dagql.NamedInput{{Name: "value", Value: dagql.String(addr)}}},
		addressLoaderSelector(srv, addressField))
	return obj, err
}

// addressLoaderSelector selects the Address loader for addressField with
// noLock: true when the loader takes it (an image tag or git ref lookup), so
// a model-supplied address resolves live: it neither reads the workspace
// lockfile's pin nor records a new one.
func addressLoaderSelector(srv *dagql.Server, addressField string) dagql.Selector {
	sel := dagql.Selector{View: srv.View, Field: addressField}
	addressType, ok := srv.ObjectType("Address")
	if !ok {
		return sel
	}
	spec, ok := addressType.FieldSpec(addressField, srv.View)
	if !ok {
		return sel
	}
	if _, ok := spec.Args.Input("noLock", srv.View); ok {
		sel.Args = []dagql.NamedInput{{Name: "noLock", Value: dagql.Boolean(true)}}
	}
	return sel
}

// resolveObjectAddress loads an object from an external address (an image
// ref, a remote git URL) through the core Address API:
// Workspace.resolve(value: <addr>).<addressField>, or Query.address when no
// workspace is in scope (see resolveUserAddress). DAG addresses resolve in
// the conversation's scope or bound workspace instead (see liftObjectArg).
func resolveObjectAddress(ctx context.Context, srv *dagql.Server, addr, addressField string) (dagql.AnyObjectResult, error) {
	var obj dagql.AnyObjectResult
	resolved, err := resolveUserAddress(ctx, srv, addr)
	if err != nil {
		return nil, err
	}
	// A backstop: a dag:// address is accepted only because a workspace
	// resolves it. Without one, some decoders would read the value as a
	// path on the calling client's host instead.
	if dagaddress.IsAddress(addr) && resolved.Self().BoundWorkspace.Self() == nil {
		return nil, errors.New("a dag:// address needs a workspace, and none is in scope")
	}
	// Address resolution is user-facing work of the tool call — possibly an
	// image pull — not engine bookkeeping: run it non-internal (matching the
	// method call's Select in callObjectMethod) so it renders in the trace as
	// part of the tool call instead of hiding as internal spans.
	err = srv.Select(dagql.WithNonInternalTelemetry(ctx), resolved, &obj,
		addressLoaderSelector(srv, addressField))
	return obj, err
}

// routeObjectMethodResult renders a method's result for the model, per the
// return-type table in hack/designs/workspace-agents.md:
//   - Changeset: overlay onto the workspace, return the patch summary.
//   - Workspace: replace the current workspace, return the diff summary.
//   - LLM: replace the conversation — the loop resumes from it (a continuation).
//   - LLMContent: the tool result's text and media, in order.
//   - the bound object's own type: rebind it as the new state, return its print.
//   - any other object: sync it, return its print (else a type description).
//   - Void/null: return its print, else "(done)".
//   - scalar/list/record: return the value.
func (m *MCP) routeObjectMethodResult(ctx context.Context, srv *dagql.Server, typeName string, val dagql.AnyResult) (any, error) {
	// A Changeset overlays onto the workspace (a Workspace replaces it, an LLM
	// replaces the whole conversation), returning a summary. step() persists the
	// resulting workspace via a withWorkspace selector, or resumes from the
	// returned conversation.
	if handled, out, err := m.applyStateReturn(ctx, srv, val); handled {
		if logs := m.toolLogs(ctx); logs != "" {
			if out == "" {
				out = logs
			} else {
				out = logs + "\n---\n" + out
			}
		}
		return out, err
	}

	// Content is the tool's result content, in order, after anything the
	// method printed. This is how a tool shows the model media (e.g. a
	// screenshot): as its own result, not as a user message appended to the
	// conversation it would have to return.
	if content, ok := dagql.UnwrapAs[*LLMContent](val); ok {
		if content == nil || len(content.Blocks) == 0 {
			return m.logsOrDone(ctx), nil
		}
		// Clone so the tool result never aliases a cached value.
		return &LLMContentBlock{Kind: LLMContentToolResult, Text: m.toolLogs(ctx), Content: cloneLLMContent(content.Blocks)}, nil
	}

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		if obj.Type().Name() == typeName {
			// Same-type return: the result is the agent's new state. Rebind it
			// (step() persists this as a withTools selector); the method's own print
			// output is the response.
			if err := m.rebindBoundTool(typeName, obj); err != nil {
				return nil, err
			}
			return m.logsOrDone(ctx), nil
		}
		// Any other object: force it so its side effects run, and surface whatever
		// it printed; fall back to describing it by type.
		if err := m.syncObject(ctx, srv, obj); err != nil {
			return nil, err
		}
		if logs := m.toolLogs(ctx); logs != "" {
			return logs, nil
		}
		return m.describeObject(ctx, srv, obj)
	}

	if val == nil || val.Type().Name() == "Void" {
		return m.logsOrDone(ctx), nil
	}

	// Scalar, list, enum, or record: return the value directly.
	return m.outputToLLM(ctx, srv, val)
}

// syncObject forces an object result (running its side effects) when it has a
// sync field, so a tool that returns e.g. a Container executes before we read
// its logs.
func (m *MCP) syncObject(ctx context.Context, srv *dagql.Server, obj dagql.AnyObjectResult) error {
	if _, ok := obj.ObjectType().FieldSpec("sync", srv.View); !ok {
		return nil
	}
	var synced dagql.AnyResult
	// Non-internal for the same reason as the tool's method call itself: the
	// sync runs the object's side effects whose print output we surface.
	return srv.Select(dagql.WithNonInternalTelemetry(ctx), obj, &synced, dagql.Selector{View: srv.View, Field: "sync"})
}

// logsOrDone returns whatever the just-executed method printed, or "(done)" when
// it printed nothing.
func (m *MCP) logsOrDone(ctx context.Context) string {
	if logs := m.toolLogs(ctx); logs != "" {
		return logs
	}
	return "(done)"
}

// toolLogs captures the output emitted beneath the current tool-call span
// (created by MCP.Call). Empty when nothing was captured.
//
// A tool call that ran nested work is rendered as TWO sections (see
// spanResult): the tool's own printed output, verbatim, and the pretty
// report of what ran beneath it. A tool call that ran no nested work has no
// tree worth drawing, so it falls back to the flat captured-log text -- which
// is also what spanResult falls back to when the subtree renders to nothing
// (dagui filters internal/passthrough/encapsulated spans, so a report can
// legitimately come out empty).
func (m *MCP) toolLogs(ctx context.Context) string {
	spanID := trace.SpanContextFromContext(ctx).SpanID()
	if !spanID.IsValid() {
		return ""
	}
	if !toolSpanHasDescendants(ctx, spanID.String()) {
		// Nothing ran beneath the call: there is no tree to draw, only
		// whatever the tool printed itself.
		return m.toolFlatLogs(ctx, spanID.String())
	}
	return m.spanResult(ctx, spanID.String(), toolCallReportOpts())
}

// Section heading for a combined result. It matches the report's own agent
// vocabulary -- "== CHECKS ==", "== TESTS ==", "== SERVICES ==" and friends,
// see idtui's reportHeadingLine -- so the tool's own output reads as one more
// section of the same document rather than as a new kind of wrapper.
//
// The report body itself carries no heading: its sections speak for
// themselves, and a "TRACE REPORT" banner over them was pure redundancy (the
// tool that asks for one is literally called ReadTrace).
const spanResultOutputHeading = "== OUTPUT =="

// spanResult renders what happened beneath spanID for an LLM reader.
//
// It carries BOTH halves, because either alone loses something:
//
//   - OUTPUT: the lines captureLogLines classified as `direct` -- what the
//     tool (or test, or check) printed itself -- verbatim and unabridged. A
//     deliberate report is the point of the call, and letting a rendered
//     summary stand in for it is exactly the regression the provenance-based
//     abridging already fixed once for the flat path. When the report hides
//     its span tree (a tool call's own result), OUTPUT also carries a tail of
//     the nested logs, as the flat path does; see spanResultOutput.
//   - the report: the structure of the nested work, with its logs clamped
//     per row, plus the CHECKS/TESTS roll-ups. It carries no heading of its
//     own -- its sections are already labelled.
//
// Bounded failure links come first, when present, followed by OUTPUT and the
// report. The byte guard applies to the COMBINED text; navigation stays at the
// head so neither that guard nor the outer tool-result guard can discard it.
//
// The direct lines are not duplicated: the report is told to suppress the
// inline logs of exactly the spans that printed them (HideLogSpans).
// Suppressing rather than de-duplicating after the fact keeps the report's
// own clamping honest -- a hidden row's nested children are still clamped and
// still rendered. The nested tail OUTPUT carries when the tree is hidden is
// deliberately NOT suppressed, so a check's or test's logs stay under its own
// row; the overlap is bounded by that tail.
//
// With no report to show -- nothing nested, a render failure, or a subtree
// that renders to nothing -- the result is the flat capture, byte for byte as
// before: no headings, no separators, no empty sections.
func (m *MCP) spanResult(ctx context.Context, spanID string, opts traceReportOpts) string {
	result, err := m.inspectSpanResult(ctx, spanID, opts)
	if err != nil {
		// Automatic decoration must not replace the actual tool result with a
		// telemetry failure. Explicit inspection returns this error instead.
		slog.Warn("incomplete tool trace decoration", "span", spanID, "error", err)
	}
	return result
}

// inspectSpanResult preserves component errors even when the other half of a
// report is available. ReadTrace must never present partial telemetry as a
// successful inspection; spanResult deliberately keeps the best-effort text.
func (m *MCP) inspectSpanResult(ctx context.Context, spanID string, opts traceReportOpts) (string, error) {
	captured, captureErr := m.captureLogLines(ctx, spanID, true, opts.OwnOutputOnly)
	opts.HideLogSpans = captured.directSpans
	report, reportErr := renderTraceReport(ctx, spanID, opts)
	if captureErr != nil {
		captureErr = fmt.Errorf("capture logs for span %s: %w", spanID, captureErr)
	}
	if reportErr != nil {
		reportErr = fmt.Errorf("render report for span %s: %w", spanID, reportErr)
	}
	err := errors.Join(captureErr, reportErr)
	if strings.TrimSpace(report.body) == "" && report.failures == "" {
		return flatLogs(spanID, captured.lines), err
	}
	own := spanResultOutput(spanID, captured.lines, opts.HideSpanTree)
	if opts.FocusFailures && report.failures != "" {
		own = guardText(own, textGuard{
			maxBytes: 4096, maxLineLen: llmLogsMaxLineLen, headBytes: 2048,
			marker: func(lines, bytes int) string {
				return fmt.Sprintf("... %d own log lines omitted; ReadLogs(span: %q, scope: \"own\", fromLine: 1) ...", lines, spanID)
			},
		})
	}
	return combineSpanResult(spanID, own, report.body, report.failures), err
}

// combineSpanResult assembles and bounds the sections. A root ReadLogs
// breadcrumb is a fallback only when there are no narrower failure links.
// own may be empty; the report's sections already carry their own headings.
func combineSpanResult(spanID, own, report, failures string) string {
	report = strings.TrimLeft(report, "\n")
	if strings.TrimSpace(report) == "" && failures == "" {
		return ""
	}
	var sections []string
	if failures != "" {
		// Put actionable failure links ahead of potentially huge OUTPUT. This
		// bounded section survives both report and outer CallContent guards.
		sections = append(sections, failures)
	}
	if own != "" {
		sections = append(sections, spanResultOutputHeading+"\n"+own)
	}
	sections = append(sections, report)
	result := guardTraceReport(strings.Join(sections, "\n\n"))
	if failures == "" {
		result += "\n" + fmt.Sprintf("... use ReadLogs(span: %s) to read the full logs ...", spanID)
	}
	return result
}

// toolCallReportOpts are the render options for the report embedded in a tool
// call's own result.
func toolCallReportOpts() traceReportOpts {
	return traceReportOpts{
		// The tool call's own span is a roll-up/boundary span and every module
		// function beneath it may be too; without forcing rows open, the work
		// a tool did would render as a bare status line. See expandedSpans:
		// this unwrap is tuned for a tool-call scope and stops at the first
		// real work span.
		ExpandWrappers: true,
		// Same reason captureLogLines excludes them: a long-lived service's
		// exec span joins the subtree via cause links and streams noise that
		// drowns out deliberate output, and the LLM's own message spans are
		// conversation rather than work. ReadLogs remains the discovery path.
		HideNoise: true,
		// The report is about this tool call, not about the run that made it:
		// drop the run-wide TRACE verdict header and skip the live-tree
		// promotions that would reshape the report around the whole run. The
		// surfaced sections (CHECKS, SERVICES, CONVERSATION, ...) are rolled
		// up relative to the tool call regardless; see traceReportOpts.Scoped.
		Scoped: true,
		// Nested work is abridged to a tail, exactly as in the flat path; the
		// OUTPUT section carries the tool's own lines unabridged.
		NestedLogLines: llmToolLogsMaxLines,
		// The reader is an LLM, which has tools rather than a shell: suggest
		// FindSpans then ReadTrace for failed checks instead of `dagger
		// check "<name>"` commands it cannot run.
		SuggestReadTrace: true,
		// A tool result is about the RESULT, not about the machinery: keep
		// what the call surfaced (CHECKS, TESTS, SERVICES, conversation) and
		// the OUTPUT section, and drop the span tree. With no tree to carry
		// nested logs, OUTPUT takes them instead, abridged to a tail as in
		// the flat path (see spanResultOutput). An agent that wants the tree
		// asks for it with ReadTrace, which keeps rendering it.
		HideSpanTree: true,
	}
}

// directLogs joins the lines the captured span printed itself, verbatim save
// for the per-line byte clamp every LLM-facing path applies. Empty when the
// span printed nothing -- so no empty OUTPUT section is ever emitted.
func directLogs(lines []capturedLine) string {
	var out []string
	for _, line := range lines {
		if line.direct {
			out = append(out, line.text)
		}
	}
	if len(out) == 0 {
		return ""
	}
	for i, line := range out {
		if len(line) > llmLogsMaxLineLen {
			out[i] = line[:llmLogsMaxLineLen] + fmt.Sprintf("[... %d chars truncated]", len(line)-llmLogsMaxLineLen)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// spanResultOutput builds a combined result's OUTPUT section.
//
// With the span tree shown (ReadTrace), OUTPUT is only what the span printed
// itself: nested logs render in the tree, under the rows that produced them.
// With the tree hidden (a tool call's own result), OUTPUT is the only place
// most nested output can appear at all, so it takes the flat path's shape:
// direct lines in full plus a tail of nested lines. Otherwise a tool whose
// result is its nested exec's stdout would lose it the moment anything else
// -- a started service, a check -- made the report non-empty.
//
// Either way, only the DIRECT spans are hidden from the report
// (HideLogSpans). A nested span whose lines land in OUTPUT's tail keeps its
// inline logs under its own row -- a check's or test's logs stay attributed
// to it in CHECKS/TESTS -- at the cost of a bounded (llmToolLogsMaxLines)
// duplication.
func spanResultOutput(spanID string, lines []capturedLine, withNested bool) string {
	if !withNested {
		return directLogs(lines)
	}
	return flatLogs(spanID, lines)
}

// toolSpanHasDescendants reports whether anything ran beneath the tool-call
// span. It is a pure in-memory index lookup on the client's telemetry store --
// no queries, no subtree walk -- so it is cheap enough to run for every tool result.
func toolSpanHasDescendants(ctx context.Context, spanID string) bool {
	root, err := CurrentQuery(ctx)
	if err != nil {
		return false
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return false
	}
	// NB: this flushes the session's clients, so spans that just ended are
	// visible in the index.
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return false
	}
	defer q.Close()
	return q.HasDescendants(spanID)
}

// toolFlatLogs is the flat capture: the print output emitted beneath the
// tool-call span, joined into lines. Empty when nothing was printed.
func (m *MCP) toolFlatLogs(ctx context.Context, spanID string) string {
	// Exclude service exec span logs: long-lived services stream noise into
	// the tool-call subtree via cause links, drowning out deliberate prints.
	// ReadLogs remains the discovery path for service logs.
	captured, err := m.captureLogLines(ctx, spanID, true, false)
	if err != nil {
		return ""
	}
	return flatLogs(spanID, captured.lines)
}

// flatLogs is the pre-report tool-result shape, unchanged: whatever the tool
// printed itself survives in full — a sub-agent's report or a tool's summary
// is the point of the call. Only logs from nested work beneath it are
// abridged to a tail.
func flatLogs(spanID string, lines []capturedLine) string {
	if len(lines) == 0 {
		return ""
	}
	logs := limitIndirectLines(spanID, lines, llmToolLogsMaxLines, llmLogsMaxLineLen)
	return strings.TrimRight(strings.Join(logs, "\n"), "\n")
}

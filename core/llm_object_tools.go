package core

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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
	Except  []string
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

// WithTools binds obj's methods as tools, carrying except. At most one binding
// per object type is kept: binding an object whose type is already bound replaces
// it in place. That is the state-update shape — a method returning the bound type
// rebinds through here — so the binding list stays bounded and a recorded
// withTools selector replays to the same state deterministically.
func (m *MCP) WithTools(obj dagql.AnyObjectResult, except []string) *MCP {
	m = m.Clone()
	typeName := obj.Type().Name()
	id, _ := obj.ID()
	binding := boundTool{object: obj, id: id, objType: obj.ObjectType(), Except: except}
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
// that produced the object. objType is the object's GraphQL type, resolved from
// the ID's return type, so the toolset can still be built.
func (m *MCP) WithLazyTools(id *call.ID, objType dagql.ObjectType, except []string) *MCP {
	m = m.Clone()
	typeName := objType.TypeName()
	binding := boundTool{id: id, objType: objType, Except: except}
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
		m.boundTools[i].object = obj
		return obj, true, nil
	}
	return obj, true, nil
}

// rebindBoundTool replaces the object for typeName's binding with newObj — the
// same-type-return state transition. It mutates in place under the lock; step()
// then persists the transition as a withTools selector on the LLM's ID.
func (m *MCP) rebindBoundTool(typeName string, newObj dagql.AnyObjectResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, b := range m.boundTools {
		if b.typeName() == typeName {
			id, _ := newObj.ID()
			m.boundTools[i].object = newObj
			m.boundTools[i].id = id
			m.boundTools[i].objType = newObj.ObjectType()
			return
		}
	}
}

// boundToolBinding is a flattened snapshot of a binding: the object's ID plus its
// except list, enough for step() to rebuild a withTools selector.
type boundToolBinding struct {
	ID     *call.ID
	Except []string
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
		out = append(out, boundToolBinding{ID: id, Except: slices.Clone(b.Except)})
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
			if arg.Type.Type().Name() == workspaceTypeName {
				// Auto-injected from the current workspace.
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
		m = m.WithTools(obj, nil)
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
	schema := srv.Schema()
	toolsets := make([]bindingToolset, 0, len(bindings))
	for _, b := range bindings {
		tools, err := m.toolsForBoundObject(srv, schema, b)
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
	srv, err := m.Server(ctx)
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
func (m *MCP) toolsForBoundObject(srv *dagql.Server, schema *ast.Schema, b boundTool) ([]LLMTool, error) {
	typeName := b.typeName()
	def := schema.Types[typeName]
	if def == nil || (def.Kind != ast.Object && def.Kind != ast.Interface) {
		return nil, fmt.Errorf("bound object type %q is not an object in the workspace schema", typeName)
	}
	var tools []LLMTool
	for _, field := range def.Fields {
		if !objectToolEligible(field, b.Except) {
			continue
		}
		toolSchema, err := objectMethodSchema(schema, field)
		if err != nil {
			return nil, fmt.Errorf("build schema for %s.%s: %w", typeName, field.Name, err)
		}
		retType := field.Type.Name()
		tools = append(tools, LLMTool{
			Name:        field.Name,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      toolSchema,
			// A method that returns the bound object's own type or a Workspace
			// mutates shared state and must run sequentially. Changeset-returning
			// methods run in parallel; CallBatch merges their results before applying
			// them to the workspace.
			ReadOnly:         retType != typeName && retType != "Changeset" && retType != "Workspace",
			ReturnsChangeset: retType == "Changeset",
			Call:             m.callObjectMethod(srv, typeName, field),
			Server:           typeName,
		})
	}
	return tools, nil
}

// objectToolEligible reports whether a field becomes a tool: it must not be in
// except, must not be an internal/reserved field, and every REQUIRED argument
// must be expressible without an object handle — a required object-typed arg
// (other than the auto-injected Workspace) disqualifies it, since the model has
// no handle to pass.
func objectToolEligible(field *ast.FieldDefinition, except []string) bool {
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
		if isWorkspaceArg(arg) {
			// Auto-injected from the bound Workspace; treated as optional.
			continue
		}
		required := arg.Type.NonNull && arg.DefaultValue == nil
		if required && isObjectArg(arg) {
			return false
		}
	}
	return true
}

// isObjectArg reports whether an argument is a Dagger object, which crosses the
// wire as an `ID` scalar carrying an @expectedType directive.
func isObjectArg(arg *ast.ArgumentDefinition) bool {
	return arg.Directives.ForName("expectedType") != nil
}

// isWorkspaceArg reports whether an argument is the auto-injected Workspace,
// identified by @expectedType(name: "Workspace"). Such args are filled from the
// bound Workspace and never shown to the model.
func isWorkspaceArg(arg *ast.ArgumentDefinition) bool {
	d := arg.Directives.ForName("expectedType")
	if d == nil {
		return false
	}
	name := d.Arguments.ForName("name")
	return name != nil && name.Value != nil && name.Value.Raw == workspaceTypeName
}

// objectMethodSchema builds a tool's JSON-schema parameters from a field's
// visible arguments — its scalars, enums, lists, and input objects — omitting the
// auto-injected Workspace argument. Object args (when optional) render as ID
// strings, annotated with their expected type.
func objectMethodSchema(schema *ast.Schema, field *ast.FieldDefinition) (map[string]any, error) {
	properties := map[string]any{}
	var required []string
	for _, arg := range field.Arguments {
		if isWorkspaceArg(arg) {
			continue
		}
		argSchema, err := argTypeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}
		desc := arg.Description
		if d := arg.Directives.ForName("expectedType"); d != nil {
			if name := d.Arguments.ForName("name"); name != nil && name.Value != nil {
				if desc == "" {
					desc = fmt.Sprintf("(%s ID)", name.Value.Raw)
				} else {
					desc = fmt.Sprintf("(%s ID) %s", name.Value.Raw, desc)
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
		return jsonSchema, nil
	}
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
	return jsonSchema, nil
}

// callObjectMethod returns the tool implementation for one method of a bound
// object. It selects the method on the CURRENT bound object (so an earlier
// same-batch state update is visible), relying on the bound Workspace already
// threaded into ctx by MCP.Call so Workspace-typed args auto-inject, then routes
// the result by type.
func (m *MCP) callObjectMethod(srv *dagql.Server, typeName string, field *ast.FieldDefinition) LLMToolFunc {
	fieldName := field.Name
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
		sel, err := buildObjectMethodSelector(srv, recv.ObjectType(), fieldName, args)
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

// buildObjectMethodSelector converts the model's tool arguments into a selector
// for the method. It decodes each provided argument through the field's input
// spec; the Workspace argument is omitted here and auto-injected downstream.
func buildObjectMethodSelector(srv *dagql.Server, recvType dagql.ObjectType, fieldName string, args map[string]any) (dagql.Selector, error) {
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
			continue
		}
		delete(provided, arg.Name)
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return sel, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
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

// routeObjectMethodResult renders a method's result for the model, per the
// return-type table in hack/designs/workspace-agents.md:
//   - Changeset: overlay onto the workspace, return the patch summary.
//   - Workspace: replace the current workspace, return the diff summary.
//   - the bound object's own type: rebind it as the new state, return its print.
//   - any other object: sync it, return its print (else a type description).
//   - Void/null: return its print, else "(done)".
//   - scalar/list/record: return the value.
func (m *MCP) routeObjectMethodResult(ctx context.Context, srv *dagql.Server, typeName string, val dagql.AnyResult) (any, error) {
	// A Changeset overlays onto the workspace (and a Workspace replaces it),
	// returning a patch summary. step() persists the resulting workspace via a
	// withWorkspace selector.
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

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		if obj.Type().Name() == typeName {
			// Same-type return: the result is the agent's new state. Rebind it
			// (step() persists this as a withTools selector); the method's own print
			// output is the response.
			m.rebindBoundTool(typeName, obj)
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
// spanResult): the tool's own printed output, verbatim, and the pretty TRACE
// REPORT of what ran beneath it. A tool call that ran no nested work has no
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

// Section headings for a combined result. Uppercase single-word headings are
// the report's own vocabulary (CHECKS, TESTS, CONVERSATION, SERVICES), so the
// two halves read as more sections of the same document rather than as a new
// kind of wrapper.
const (
	spanResultOutputHeading = "OUTPUT"
	spanResultReportHeading = "TRACE REPORT"
)

// spanResult renders what happened beneath spanID for an LLM reader.
//
// It carries BOTH halves, because either alone loses something:
//
//   - OUTPUT: the lines captureLogLines classified as `direct` -- what the
//     tool (or test, or check) printed itself -- verbatim and unabridged. A
//     deliberate report is the point of the call, and letting a rendered
//     summary stand in for it is exactly the regression the provenance-based
//     abridging already fixed once for the flat path.
//   - TRACE REPORT: the structure of the nested work, with its logs clamped
//     per row, plus the CHECKS/TESTS roll-ups.
//
// OUTPUT comes first for two reasons: it is the answer, while the report is
// the supporting evidence; and guardTraceReport drops the MIDDLE of an
// over-budget result, so the head is the one place a section is guaranteed to
// survive in full. The byte guard is applied to the COMBINED text -- the
// budget is what reaches the reader, not what one half of it renders to.
//
// There is no duplication between the two: the report is told to suppress the
// inline logs of exactly the spans OUTPUT was built from (HideLogSpans).
// Suppressing rather than de-duplicating after the fact keeps the report's
// own clamping honest -- a hidden row's nested children are still clamped and
// still rendered.
//
// With no report to show -- nothing nested, a render failure, or a subtree
// that renders to nothing -- the result is the flat capture, byte for byte as
// before: no headings, no separators, no empty sections.
func (m *MCP) spanResult(ctx context.Context, spanID string, opts traceReportOpts) string {
	// Exclude service exec span logs: long-lived services stream noise into
	// the subtree via cause links, drowning out deliberate prints. ReadLogs
	// remains the discovery path for service logs.
	captured, err := m.captureLogLines(ctx, spanID, true, opts.OwnOutputOnly)
	if err != nil {
		slog.Warn("failed to capture tool logs", "span", spanID, "error", err)
	}

	report := m.traceReport(ctx, spanID, captured.directSpans, opts)
	if report == "" {
		return flatLogs(spanID, captured.lines)
	}
	return combineSpanResult(spanID, directLogs(captured.lines), report)
}

// combineSpanResult assembles the two sections, bounds the COMBINED text, and
// closes with the ReadLogs breadcrumb. own may be empty -- a target that
// printed nothing gets no OUTPUT section, not an empty one.
func combineSpanResult(spanID, own, report string) string {
	if strings.TrimSpace(report) == "" {
		return ""
	}
	var sections []string
	if own != "" {
		sections = append(sections, spanResultOutputHeading+"\n"+own)
	}
	sections = append(sections, spanResultReportHeading+"\n"+report)
	// The report clamps nested log tails (and the byte guard may drop its
	// middle), so tell the reader where the unabridged logs live.
	return guardTraceReport(strings.Join(sections, "\n\n")) + "\n" +
		fmt.Sprintf("... use ReadLogs(span: %s) to read the full logs ...", spanID)
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
		ExpandAll: true,
		// Same reason captureLogLines excludes them: a long-lived service's
		// exec span joins the subtree via cause links and streams noise that
		// drowns out deliberate output, and the LLM's own message spans are
		// conversation rather than work. ReadLogs remains the discovery path.
		HideNoise: true,
		// The report is about this tool call, not about the agent that made
		// it: drop the whole-trace CONVERSATION/SERVICES sections, which would
		// otherwise render the caller's own transcript back at it.
		Scoped: true,
		// Nested work is abridged to a tail, exactly as in the flat path; the
		// OUTPUT section carries the tool's own lines unabridged.
		NestedLogLines: llmToolLogsMaxLines,
		// The reader is an LLM, which has tools rather than a shell: suggest
		// the ReadTrace builtin for the failed checks instead of `dagger
		// check "<name>"` commands it cannot run.
		SuggestReadTrace: true,
		// A tool result is about the RESULT, not about the machinery: keep
		// what the call surfaced (CHECKS, TESTS, SERVICES, conversation) and
		// the tool's own OUTPUT, and drop the span tree. An agent that wants
		// the tree asks for it with ReadTrace, which keeps rendering it.
		HideSpanTree: true,
	}
}

// traceReport renders spanID's subtree as the pretty report, with the spans
// whose output the caller prints itself suppressed. It returns "" when there
// is no report to show and the flat capture should be used instead.
func (m *MCP) traceReport(ctx context.Context, spanID string, hideLogSpans map[string]bool, opts traceReportOpts) string {
	opts.HideLogSpans = hideLogSpans
	report, err := renderTraceReport(ctx, spanID, opts)
	if err != nil {
		slog.Warn("failed to render trace report", "span", spanID, "error", err)
		return ""
	}
	report = strings.TrimRight(report, "\n")
	if strings.TrimSpace(report) == "" {
		return ""
	}
	return report
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

// toolSpanHasDescendants reports whether anything ran beneath the tool-call
// span. It is a pure in-memory index lookup on the client's telemetry store --
// no queries, no subtree walk -- so it is cheap enough to gate every tool
// result on.
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

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const workspaceInputsNamespace = "workspace-inputs/v2"

// Map native Host operations to the public source query that contains them.
type workspaceInputSourceContextKey struct{}

// Facts describe prepared inputs and the queries used to construct them.
type workspaceInputSource struct {
	Workspace string                `json:"workspace"`
	Selectors []provenance.Selector `json:"selectors"`
	Issues    []string              `json:"issues,omitempty"`
}

func workspaceInputKey(ws *Workspace) string {
	if ws.Address == "" {
		return ""
	}
	return ws.ClientID + "\x00" + ws.Address
}

type workspaceInputSummary struct {
	Sources []workspaceInputSource `json:"sources,omitempty"`
	Issues  []string               `json:"issues,omitempty"`
}

func (s *workspaceInputSummary) merge(other workspaceInputSummary) {
	s.Sources = append(s.Sources, other.Sources...)
	s.Issues = append(s.Issues, other.Issues...)
}

func (s workspaceInputSummary) normalized() workspaceInputSummary {
	byWorkspace := map[string]workspaceInputSource{}
	for _, source := range s.Sources {
		if source.Workspace == "" {
			s.Issues = append(s.Issues, "source input has no workspace address")
			continue
		}
		group := byWorkspace[source.Workspace]
		group.Workspace = source.Workspace
		group.Selectors = append(group.Selectors, source.Selectors...)
		group.Issues = append(group.Issues, source.Issues...)
		byWorkspace[source.Workspace] = group
	}
	s.Sources = nil
	for _, group := range byWorkspace {
		group.Selectors = provenance.Normalize(group.Selectors)
		slices.Sort(group.Issues)
		group.Issues = slices.Compact(group.Issues)
		s.Sources = append(s.Sources, group)
	}
	slices.SortFunc(s.Sources, func(a, b workspaceInputSource) int { return strings.Compare(a.Workspace, b.Workspace) })
	s.Issues = slices.Clone(s.Issues)
	slices.Sort(s.Issues)
	s.Issues = slices.Compact(s.Issues)
	return s
}

func (s workspaceInputSummary) forWorkspace(workspace string) ([]provenance.Selector, []string) {
	var selectors []provenance.Selector
	issues := slices.Clone(s.Issues)
	if workspace == "" {
		issues = append(issues, "current workspace has no address")
	}
	for _, source := range s.Sources {
		if source.Workspace == "" {
			issues = append(issues, "source input has no workspace address")
			continue
		}
		if source.Workspace != workspace {
			continue
		}
		selectors = append(selectors, source.Selectors...)
		issues = append(issues, source.Issues...)
	}
	slices.Sort(issues)
	return provenance.Normalize(selectors), slices.Compact(issues)
}

type workspaceInputCollector struct {
	ctx        context.Context
	cache      *dagql.Cache
	server     *dagql.Server
	session    string
	seen       map[*dagql.ResultCall]bool
	result     workspaceInputSummary
	origin     string
	scope      string
	evaluating bool
	retained   map[uint64]bool
}

func newWorkspaceInputCollector(ctx context.Context) (*workspaceInputCollector, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	server, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return &workspaceInputCollector{ctx: ctx, cache: cache, server: server, session: md.SessionID, seen: map[*dagql.ResultCall]bool{}, retained: map[uint64]bool{}}, nil
}

// workspaceInputsForCall visits the current call's inputs without executing the
// call. Module code paths are added separately from the module's source.
func workspaceInputsForCall(ctx context.Context, frame *dagql.ResultCall) (workspaceInputSummary, error) {
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return workspaceInputSummary{}, err
	}
	collector.evaluating = true
	collector.inputs(frame)
	return collector.result.normalized(), nil
}

func (c *workspaceInputCollector) issue(format string, args ...any) {
	issue := fmt.Sprintf(format, args...)
	if c.scope != "" {
		c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: c.scope, Issues: []string{issue}})
		return
	}
	c.result.Issues = append(c.result.Issues, issue)
}

func (c *workspaceInputCollector) workspace(ref *dagql.ResultCallRef) (*Workspace, error) {
	if ref == nil || ref.ResultID == 0 {
		return nil, fmt.Errorf("Workspace input has no attached value")
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
	if err != nil {
		return nil, err
	}
	ws, ok := dagql.UnwrapAs[*Workspace](value)
	if !ok || ws == nil {
		return nil, fmt.Errorf("Workspace input has type %T", value.Unwrap())
	}
	return ws, nil
}

func (c *workspaceInputCollector) source(ws *Workspace, selector provenance.Selector) {
	if ws.Address == "" {
		c.issue("source input has no workspace address")
		return
	}
	c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: workspaceInputKey(ws), Selectors: []provenance.Selector{selector}})
}

func (c *workspaceInputCollector) inputs(frame *dagql.ResultCall) {
	if frame == nil {
		return
	}
	if c.evaluating && frame.Module != nil {
		c.moduleInputs(frame.Module.ResultRef)
	}
	previous := c.origin
	c.origin = "receiver"
	if frame.Field != "" {
		c.origin += " of " + frame.Field
	}
	c.ref(frame.Receiver)
	for _, arg := range frame.Args {
		if arg != nil {
			c.origin = "argument " + arg.Name
			if frame.Field != "" {
				c.origin += " of " + frame.Field
			}
			c.literal(arg.Value)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input != nil {
			c.origin = "engine input " + input.Name
			c.literal(input.Value)
		}
	}
	c.origin = previous
}

func (c *workspaceInputCollector) moduleInputs(ref *dagql.ResultCallRef) {
	if ref == nil {
		c.issue("called module has no attached source")
		return
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
	if err != nil {
		c.issue("cannot inspect module runtime inputs: %v", err)
		return
	}
	mod, ok := dagql.UnwrapAs[*Module](value)
	if !ok {
		c.issue("called module source has type %T", value.Unwrap())
		return
	}
	evaluating := c.evaluating
	c.evaluating = false
	c.ref(ref)
	if mod.Runtime.Valid {
		c.retainedField(mod.Runtime.Value, "module runtime")
	}
	if mod.Definition.Valid {
		c.retainedField(mod.Definition.Value, "module definition")
		if definition := mod.Definition.Value.Self(); definition != nil && definition.Runtime.Valid {
			c.retainedField(definition.Runtime.Value, "module definition runtime")
		}
	}
	c.evaluating = evaluating
	if src := mod.GetSource(); !mod.Runtime.Valid && src != nil && src.SDKImpl != nil {
		_, moduleBacked := src.SDKImpl.AsModule()
		if sdk, ok := src.SDKImpl.(*persistedModuleSourceLazySDK); ok {
			moduleBacked = sdk.capabilities.ModuleBacked == nil || *sdk.capabilities.ModuleBacked
		}
		if _, runtime := src.SDKImpl.AsRuntime(); moduleBacked && runtime {
			c.issue("SDK runtime inputs have not been prepared")
		}
	}
	c.entrypointModuleInputs(mod)
}

func (c *workspaceInputCollector) entrypointModuleInputs(mod *Module) {
	if mod == nil {
		return
	}
	src := mod.GetSource()
	if src == nil || src.Entrypoint == nil {
		return
	}
	if src.EntrypointSources {
		id, err := src.ContextDirectory.ID()
		if err != nil || id == nil {
			c.issue("entrypoint source directory has no attached value")
			return
		}
		c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		return
	}
	var ws *Workspace
	if bound, ok := WorkspaceFromContext(c.ctx); ok {
		ws = bound.Self()
	} else {
		query, err := CurrentQuery(c.ctx)
		if err != nil {
			c.issue("cannot inspect entrypoint workspace: %v", err)
			return
		}
		ws, err = query.Server.CurrentWorkspace(c.ctx)
		if err != nil {
			c.issue("cannot inspect entrypoint workspace: %v", err)
			return
		}
	}
	if ws == nil {
		c.issue("entrypoint has no workspace")
		return
	}
	c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "entrypoint workspace"})
}

func (c *workspaceInputCollector) literal(value *dagql.ResultCallLiteral) {
	if value == nil {
		return
	}
	c.ref(value.ResultRef)
	for _, item := range value.ListItems {
		c.literal(item)
	}
	for _, field := range value.ObjectFields {
		if field != nil {
			c.literal(field.Value)
		}
	}
}

func (c *workspaceInputCollector) ref(ref *dagql.ResultCallRef) {
	if ref == nil {
		return
	}
	frame, err := c.cache.ResultCallForRef(ref)
	if err != nil {
		c.issue("cannot inspect input call: %v", err)
		return
	}
	if frame == nil {
		c.issue("input has no call frame")
		return
	}
	if c.seen[frame] {
		return
	}
	c.seen[frame] = true
	if frame.Module == nil && workspaceInputTransport(frame.Type) {
		return
	}
	typeName := ""
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	nativeModule := typeName == "Module" && frame.Module == nil && (frame.Field == "module" || frame.Field == "_implementationScoped" || strings.HasPrefix(frame.Field, "with") || frame.Kind == dagql.ResultCallKindSynthetic && frame.SyntheticOp == "sdk_scope_module")
	if nativeModule && ref.ResultID != 0 {
		if value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID); err == nil {
			if mod, ok := dagql.UnwrapAs[*Module](value); ok {
				if ws := workspaceInputSourceWorkspace(c.ctx); ws != nil {
					c.moduleSources(ws, mod, "module source")
				}
			}
		}
	}
	if typeName == "Workspace" {
		if c.evaluating {
			ws, err := c.workspace(ref)
			if err != nil {
				c.issue("cannot inspect Workspace input: %v", err)
				return
			}
			c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin})
		}
	}
	if typeName == "Query" {
		return
	}
	if typeName == "Host" {
		c.issue("Host input has no workspace source mapping")
		return
	}
	if c.evaluating && ref.ResultID != 0 {
		value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
		if err != nil {
			c.issue("cannot inspect retained %s inputs: %v", typeName, err)
		} else {
			c.retainedValue(value)
		}
	}
	operation := workspaceInputOperation(c.ctx, frame)
	captured := false
	mergePayload := func(payload json.RawMessage) {
		var fact workspaceInputFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			c.issue("cannot decode %s input origin: %v", typeName, err)
			return
		}
		c.result.merge(fact.workspaceInputSummary)
		captured = captured || fact.Complete && fact.Operation == operation
	}
	facts, complete, err := c.cache.ResultFacts(c.ctx, c.session, ref, workspaceInputsNamespace)
	if err != nil {
		c.issue("cannot inspect %s input origins: %v", typeName, err)
	} else if !complete {
		c.issue("%s input has incomplete source origins", typeName)
	}
	for _, fact := range facts {
		mergePayload(fact.Payload)
	}
	c.legacySources(ref)
	if nativeModule {
		c.inputs(frame)
		return
	}
	if complete && captured {
		return
	}
	if !captured {
		if typeName == "Module" {
			c.issue("%s input from %s has no complete source query capture", typeName, frame.Field)
		} else {
			c.issue("%s input has no complete source query capture", typeName)
		}
	}
	c.inputs(frame)
}

func (c *workspaceInputCollector) legacySources(ref *dagql.ResultCallRef) {
	facts, _, err := c.cache.ResultFacts(c.ctx, c.session, ref, "workspace-inputs/v1")
	if err != nil {
		c.issue("cannot inspect earlier source origins: %v", err)
		return
	}
	for _, fact := range facts {
		var summary workspaceInputSummary
		if err := json.Unmarshal(fact.Payload, &summary); err != nil {
			c.issue("cannot decode earlier source origins: %v", err)
			continue
		}
		c.result.merge(summary)
	}
}

func (c *workspaceInputCollector) retainedValue(value dagql.AnyResult) {
	if value == nil {
		return
	}
	if id, err := value.ID(); err == nil && id != nil && id.EngineResultID() != 0 {
		if c.retained[id.EngineResultID()] {
			return
		}
		c.retained[id.EngineResultID()] = true
	}
	if ws, ok := dagql.UnwrapAs[*Workspace](value); ok && ws != nil {
		c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin})
		return
	}
	if git, ok := dagql.UnwrapAs[*WorkspaceGit](value); ok && git != nil {
		c.retainedValue(git.Workspace)
		return
	}
	if address, ok := dagql.UnwrapAs[*Address](value); ok && address != nil {
		c.retainedValue(address.BoundWorkspace)
		return
	}
	if agent, ok := dagql.UnwrapAs[*Agent](value); ok && agent != nil {
		c.issue("Agent input has live conversation state")
		return
	}
	if llm, ok := dagql.UnwrapAs[*LLM](value); ok && llm != nil {
		c.retainedValue(llm.Workspace())
		for _, expertise := range llm.Expertise {
			c.retainedValue(expertise)
		}
		if llm.mcp != nil {
			for _, tool := range llm.mcp.boundTools {
				if tool.object != nil {
					c.retainedValue(tool.object)
				} else if tool.id != nil && tool.id.EngineResultID() != 0 {
					c.ref(&dagql.ResultCallRef{ResultID: tool.id.EngineResultID()})
				} else {
					c.issue("lazy tool %s has no materialized input scope", tool.typeName())
				}
			}
		}
		return
	}
	if artifact, ok := dagql.UnwrapAs[*Artifact](value); ok && artifact != nil {
		if artifact.Workspace.Self() != nil {
			c.retainedValue(artifact.Workspace)
		} else {
			c.retainedArtifactCall(artifact, JSON("{}"))
		}
		return
	}
	if artifacts, ok := dagql.UnwrapAs[*Artifacts](value); ok && artifacts != nil {
		for _, artifact := range append(slices.Clone(artifacts.Entries), artifacts.CollectionRoots...) {
			if artifact.Workspace.Self() != nil {
				c.retainedValue(artifact.Workspace)
			} else {
				c.retainedArtifactCall(artifact, JSON("{}"))
			}
		}
		return
	}
	if obj, ok := dagql.UnwrapAs[*ModuleObject](value); ok && obj != nil {
		if obj.Module.Self() != nil {
			id, err := obj.Module.ID()
			if err != nil || id == nil {
				c.issue("retained object module has no attached source")
			} else {
				c.moduleInputs(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
		for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
			c.retainedField(obj.Fields[name], "field "+name)
		}
		return
	}
	if check, ok := dagql.UnwrapAs[*Check](value); ok && check != nil {
		previous := c.ctx
		if check.Workspace.Self() != nil {
			c.ctx = WorkspaceToContext(c.ctx, check.Workspace)
		}
		defer func() { c.ctx = previous }()
		if receiver := check.Receiver.Self(); receiver != nil {
			c.entrypointModuleInputs(receiver.Module.Self())
		}
		c.retainedValue(check.Receiver)
		c.retainedValue(check.Unaddressed)
		c.retainedArtifactCall(check.RemoteArtifact.Self(), check.RemoteArguments)
		c.retainedValue(check.Generator)
		for _, input := range check.Inputs {
			c.retainedField(input.Value, "argument "+input.Name+" of "+check.Function)
		}
		return
	}
	if generator, ok := dagql.UnwrapAs[*Generator](value); ok && generator != nil {
		c.retainedArtifactCall(generator.Artifact, generator.Arguments)
		return
	}
	if expertise, ok := dagql.UnwrapAs[*Expertise](value); ok && expertise != nil {
		c.retainedArtifactCall(expertise.Artifact, JSON("{}"))
		return
	}
	if array, ok := value.Unwrap().(dagql.Enumerable); ok {
		frame, err := value.ResultCall()
		if err != nil {
			c.issue("cannot inspect retained list inputs: %v", err)
			return
		}
		for i := 1; i <= array.Len(); i++ {
			item, err := array.NthValue(i, frame)
			if err != nil {
				c.issue("cannot inspect retained list item: %v", err)
				continue
			}
			c.retainedValue(item)
		}
	}
}

func (c *workspaceInputCollector) retainedField(value any, origin string) {
	previous := c.origin
	c.origin += ": " + origin
	defer func() { c.origin = previous }()
	if input, ok := value.(dagql.Input); ok {
		request := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}}
		if err := request.SetImplicitInput(c.ctx, "input", input); err != nil {
			c.issue("cannot inspect retained input: %v", err)
			return
		}
		c.literal(request.ImplicitInputs[0].Value)
		return
	}
	_, _, err := attachModuleObjectValue(c.ctx, func(value dagql.AnyResult) (dagql.AnyResult, error) {
		id, err := value.ID()
		if err != nil {
			return nil, err
		}
		if id != nil && id.EngineResultID() != 0 {
			c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		} else {
			c.retainedValue(value)
		}
		return value, nil
	}, value)
	if err != nil {
		c.issue("cannot inspect retained input: %v", err)
	}
}

func (c *workspaceInputCollector) retainedArtifactCall(artifact *Artifact, arguments JSON) {
	if artifact == nil {
		return
	}
	inputs, err := artifact.CallInputs(arguments)
	if err != nil {
		c.issue("cannot inspect deferred artifact inputs: %v", err)
		return
	}
	ctx, frame, err := artifact.plannedProvenanceCall(c.ctx, inputs...)
	if err != nil {
		c.issue("cannot prepare deferred artifact inputs: %v", err)
		return
	}
	previous := c.ctx
	c.ctx = ctx
	c.inputs(frame)
	c.ctx = previous
}

// ObserveWorkspaceInputs preserves source queries and input origins across cache
// reuse. Module bodies add their queries before the result is published.
func ObserveWorkspaceInputs(ctx context.Context, req *dagql.CallRequest) (context.Context, func(), func(dagql.AnyResult, error)) {
	if req == nil || req.ResultCall == nil {
		return ctx, nil, nil
	}
	parent := currentWorkspaceInputReads(ctx)
	transport := req.Module == nil && (req.ReceiverTypeName == "FunctionCall" || req.ReceiverTypeName == "FunctionCallArgValue" || workspaceInputTransport(req.Type))
	if req.Field == "id" || (req.ReceiverTypeName == "Query" && (req.Field == "node" || req.Field == "currentFunctionCall")) || transport {
		return ctx, nil, nil
	}
	source := req.Module == nil && req.ReceiverTypeName == "Workspace" && workspaceSourceQuery(req.Field)
	gitSource := req.Module == nil && req.ReceiverTypeName == "WorkspaceGit"
	hostSource := req.Module == nil && req.ReceiverTypeName == "Host" && (req.Field == "file" || req.Field == "directory")
	parentSource, inSource := ctx.Value(workspaceInputSourceContextKey{}).(workspaceInputSummary)
	view := workspaceInputView(req)
	metadata := req.Module == nil && req.ReceiverTypeName == "Query" && req.Field == "currentModule"
	boundary, _ := ctx.Value(workspaceInputBoundaryKey{}).(bool)
	nativeSourceValue := req.Receiver != nil && (req.ReceiverTypeName == "File" || req.ReceiverTypeName == "Directory" || req.Type != nil && (req.Type.NamedType == "File" || req.Type.NamedType == "Directory"))
	sourceInternal := inSource && req.Module == nil && (req.ReceiverTypeName == "Host" && !hostSource || boundary && nativeSourceValue)
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		req.ResultFactsIncomplete = true
		return ctx, nil, nil
	}
	switch {
	case source:
		collector.sourceCall(req)
	case gitSource:
		collector.gitSourceCall(req)
	case sourceInternal:
		collector.result.merge(parentSource)
	case hostSource:
		if inSource {
			collector.result.merge(parentSource)
		} else {
			collector.issue("Host source input has no workspace source mapping")
		}
	default:
		collector.inputs(req.ResultCall)
	}
	if view && req.ReceiverTypeName == "Query" {
		if ws, ok := WorkspaceFromContext(ctx); ok {
			if id, err := ws.ID(); err == nil && id != nil {
				collector.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
	}
	if req.Module != nil {
		collector.calledModuleSource(req)
	}
	summary := collector.result.normalized()
	if source || gitSource || view {
		ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, summary)
		ctx = context.WithValue(ctx, workspaceInputBoundaryKey{}, true)
	}
	appendWorkspaceInputFact(ctx, req, summary, source || gitSource || sourceInternal || hostSource || view || metadata)
	observation := &workspaceInputCall{
		request:  req,
		reads:    &workspaceInputReads{summary: summary, workspace: workspaceInputSourceWorkspace(ctx)},
		prepared: source || gitSource || sourceInternal || hostSource || view || metadata,
	}
	if req.Module == nil && req.ReceiverTypeName == "Workspace" {
		if ws, err := collector.workspace(req.Receiver); err == nil {
			observation.reads.workspace = ws
		}
	}
	if source || gitSource || view || sourceInternal || hostSource && inSource {
		observation.view = &summary
	}
	ctx = context.WithValue(ctx, workspaceInputCallKey{}, observation)
	ctx = context.WithValue(ctx, workspaceInputReadsKey{}, observation.reads)
	return ctx, func() { publishWorkspaceInputReads(ctx) }, workspaceInputReadCompletion(ctx, parent, observation)
}

func (c *workspaceInputCollector) calledModuleSource(req *dagql.CallRequest) {
	ws := workspaceInputSourceWorkspace(c.ctx)
	if ws == nil || req.Module.ResultRef == nil {
		return
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, req.Module.ResultRef.ResultID)
	if err != nil {
		c.issue("cannot inspect called module source: %v", err)
		return
	}
	mod, ok := dagql.UnwrapAs[*Module](value)
	if !ok {
		c.issue("called module source has type %T", value.Unwrap())
		return
	}
	c.moduleSources(ws, mod, "module source of "+req.Field)
}

func (c *workspaceInputCollector) moduleSources(ws *Workspace, mod *Module, origin string) {
	for _, selector := range moduleSourceSelectors(ws, mod) {
		selector.Origin = origin
		c.source(ws, selector)
	}
	seen := map[*ModuleSource]bool{}
	var source func(*ModuleSource)
	source = func(src *ModuleSource) {
		if src == nil || seen[src] {
			return
		}
		seen[src] = true
		if _, mapped := moduleSourceWorkspaceRoot(ws, src); !mapped && src.Kind == ModuleSourceKindDir {
			dir := src.ContextDirectory
			if src.DirSrc != nil && src.DirSrc.OriginalContextDir.Self() != nil {
				dir = src.DirSrc.OriginalContextDir
			}
			if id, err := dir.ID(); err == nil && id != nil {
				c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
		for _, dep := range src.Dependencies {
			source(dep.Self())
		}
		if src.SDKImpl != nil {
			if sdk, ok := src.SDKImpl.AsModule(); ok && sdk.Self() != nil {
				source(sdk.Self().GetSource())
			}
		}
	}
	source(mod.GetSource())
}

func (c *workspaceInputCollector) gitSourceCall(req *dagql.CallRequest) {
	c.ref(req.Receiver)
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, req.Receiver.ResultID)
	if err != nil {
		c.issue("cannot inspect Git workspace: %v", err)
		return
	}
	git, ok := dagql.UnwrapAs[*WorkspaceGit](value)
	if !ok || git == nil || git.Workspace.Self() == nil {
		c.issue("Git input has no workspace")
		return
	}
	c.source(git.Workspace.Self(), provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "Workspace.git." + req.Field})
}

func workspaceSourceQuery(field string) bool {
	switch field {
	case "address", "cwd", "path", "git", "snapshot", "resolve", "withWorkdir", "withConfigPaths", "withConfigEnvironment",
		"withNewFile", "withFile", "withNewDirectory", "withDirectory", "withoutFile", "withoutFiles", "withoutDirectory",
		"withChanges", "withPatchFile", "withMountedDirectory", "withMountedFile", "withoutMount":
		return false
	default:
		return true
	}
}

func (c *workspaceInputCollector) sourceCall(req *dagql.CallRequest) {
	c.ref(req.Receiver)
	ws, err := c.workspace(req.Receiver)
	if err != nil {
		c.issue("cannot inspect source workspace: %v", err)
		return
	}
	c.scope = workspaceInputKey(ws)
	if req.Field == "glob" {
		arg := req.Arg("pattern")
		if arg == nil || arg.Value == nil {
			c.issue("source glob has no pattern")
			return
		}
		c.source(ws, provenance.Selector{Path: "/", Include: []string{strings.TrimPrefix(arg.Value.StringValue, "/")}, Kind: provenance.WorkspaceRead, Origin: "Workspace.glob"})
		return
	}
	if req.Field != "file" && req.Field != "directory" {
		c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "Workspace." + req.Field})
		return
	}
	arg := req.Arg("path")
	if arg == nil || arg.Value == nil || arg.Value.Kind != dagql.ResultCallLiteralKindString {
		c.issue("source input has no path")
		return
	}
	inputPath := path.Clean(strings.ReplaceAll(arg.Value.StringValue, `\`, "/"))
	resolved := inputPath
	if !path.IsAbs(inputPath) {
		resolved = path.Join(ws.Cwd, inputPath)
	}
	resolved, err = provenance.Path(resolved)
	if err != nil {
		c.issue("cannot resolve source input path: %v", err)
		return
	}
	selector := provenance.Selector{Path: resolved, Kind: provenance.WorkspaceRead, Origin: "Workspace." + req.Field}
	if req.Field == "directory" {
		patterns := func(name string) []string {
			var patterns []string
			if arg := req.Arg(name); arg != nil && arg.Value != nil {
				for _, item := range arg.Value.ListItems {
					if item != nil && item.Kind == dagql.ResultCallLiteralKindString {
						patterns = append(patterns, item.StringValue)
					}
				}
			}
			return patterns
		}
		selector.Include = patterns("include")
		selector.Exclude = patterns("exclude")
		gitignore := false
		if arg := req.Arg("gitignore"); arg != nil && arg.Value != nil {
			gitignore = arg.Value.BoolValue
		}
		if gitignore {
			selector = provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Conservative: true, Origin: "Workspace.directory"}
		}
	}
	if selector.Path != "/" {
		if err := c.unaliasedSourcePath(ws, selector.Path); err != nil {
			c.issue("cannot determine source path %q: %v", selector.Path, err)
		}
	}
	c.source(ws, selector)
}

// A narrow selector cannot represent a symlink's target chain. Check every
// source prefix before treating its spelling as its complete dependency.
func (c *workspaceInputCollector) unaliasedSourcePath(ws *Workspace, sourcePath string) error {
	if ws.MountedPath(strings.TrimPrefix(sourcePath, "/")) || ws.HasMountsUnder(strings.TrimPrefix(sourcePath, "/")) {
		return fmt.Errorf("mounted input has no source path mapping")
	}
	var stat func(string) (*Stat, error)
	if root, ok := ws.SourceDirectory(); ok && root.Self() != nil {
		stat = func(prefix string) (*Stat, error) {
			return root.Self().Stat(c.ctx, root, c.server, prefix, true)
		}
	} else if ws.HostPath() != "" {
		ctx, err := WorkspaceClientContext(c.ctx, ws)
		if err != nil {
			return err
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return err
		}
		caller := NewCallerStatFS(bk)
		stat = func(prefix string) (*Stat, error) {
			if delta, ok := ws.OverlayDeltaRoot(); ok {
				entry, err := delta.Self().Stat(c.ctx, delta, c.server, prefix, true)
				if err == nil || !errors.Is(err, fs.ErrNotExist) || ws.OverlayPathTouched(prefix) {
					return entry, err
				}
			}
			_, entry, err := caller.Stat(ctx, filepath.Join(ws.HostPath(), filepath.FromSlash(prefix)))
			return entry, err
		}
	} else {
		return fmt.Errorf("workspace has no inspectable source")
	}
	prefix := ""
	for _, part := range strings.Split(strings.Trim(sourcePath, "/"), "/") {
		prefix = path.Join(prefix, part)
		entry, err := stat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry == nil || entry.FileType == FileTypeSymlink {
			return fmt.Errorf("source prefix %q is a symlink or has no type", prefix)
		}
	}
	return nil
}

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const workspaceInputsNamespace = "workspace-inputs/v1"

// This marker maps host sync operations inside one public source call to that
// call's declared source region. It carries no observations from the body.
type workspaceInputSourceContextKey struct{}

// These facts describe source values and planned inputs. They do not describe
// reads made by a function body or depend on an earlier function execution.
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
	ctx     context.Context
	cache   *dagql.Cache
	server  *dagql.Server
	session string
	seen    map[*dagql.ResultCall]bool
	result  workspaceInputSummary
	origin  string
	scope   string
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
	return &workspaceInputCollector{ctx: ctx, cache: cache, server: server, session: md.SessionID, seen: map[*dagql.ResultCall]bool{}}, nil
}

// workspaceInputsForCall visits the current call's inputs without executing the
// call. Module code paths are added separately from the module's source.
func workspaceInputsForCall(ctx context.Context, frame *dagql.ResultCall) (workspaceInputSummary, error) {
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return workspaceInputSummary{}, err
	}
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
	typeName := ""
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	if typeName == "Workspace" {
		ws, err := c.workspace(ref)
		if err != nil {
			c.issue("cannot inspect Workspace input: %v", err)
			return
		}
		c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin})
		return
	}
	if typeName == "Query" {
		return
	}
	if typeName == "Host" {
		c.issue("Host input has no workspace source mapping")
		return
	}
	mergePayload := func(payload json.RawMessage) bool {
		var summary workspaceInputSummary
		if err := json.Unmarshal(payload, &summary); err != nil {
			c.issue("cannot decode %s input origin: %v", typeName, err)
			return false
		}
		c.result.merge(summary)
		return true
	}
	facts, complete, err := c.cache.ResultFacts(c.ctx, c.session, ref, workspaceInputsNamespace)
	if err != nil {
		c.issue("cannot inspect %s input origins: %v", typeName, err)
	} else if !complete {
		c.issue("%s input has incomplete source origins", typeName)
	}
	found := false
	for _, fact := range facts {
		found = mergePayload(fact.Payload) || found
	}
	if complete && found {
		return
	}
	c.inputs(frame)
}

// ObserveWorkspaceInputs preserves construction-time origins when a source or
// module call returns an existing or content-equivalent result. Hits receive the
// same facts as fresh calls. Function bodies never contribute facts here.
func ObserveWorkspaceInputs(ctx context.Context, req *dagql.CallRequest) context.Context {
	if req == nil || req.ResultCall == nil {
		return ctx
	}
	sourceField := req.Module == nil && (req.Field == "file" || req.Field == "directory")
	source := sourceField && req.ReceiverTypeName == "Workspace"
	hostSource := sourceField && req.ReceiverTypeName == "Host"
	parentSource, inSource := ctx.Value(workspaceInputSourceContextKey{}).(workspaceInputSummary)
	sourceInternal := inSource && req.Module == nil && !source && !hostSource
	if req.Field == "id" || (req.ReceiverTypeName == "Query" && (req.Field == "node" || req.Field == "currentFunctionCall" || req.Field == "currentModule")) || req.ReceiverTypeName == "FunctionCall" {
		return ctx
	}
	if req.Module == nil && !source && !hostSource {
		typ := req.Type
		for typ != nil && typ.Elem != nil {
			typ = typ.Elem
		}
		server, err := CurrentDagqlServer(ctx)
		if err != nil || typ == nil {
			return ctx
		}
		if sourceInternal && (req.Receiver == nil || (typ.NamedType != "File" && typ.NamedType != "Directory")) {
			return ctx
		}
		_, object := server.ObjectType(typ.NamedType)
		_, iface := server.InterfaceType(typ.NamedType)
		if !object && !iface {
			return ctx
		}
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		req.ResultFactsIncomplete = true
		return ctx
	}
	if source {
		collector.sourceCall(req)
	} else if sourceInternal {
		collector.result.merge(parentSource)
	} else if hostSource {
		if inSource {
			collector.result.merge(parentSource)
		} else {
			collector.issue("Host source input has no workspace source mapping")
		}
	} else {
		collector.inputs(req.ResultCall)
	}
	summary := collector.result.normalized()
	if source {
		ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, summary)
	}
	payload, _ := json.Marshal(summary)
	req.ResultFacts = append(req.ResultFacts, dagql.ResultFact{Namespace: workspaceInputsNamespace, Payload: payload})
	return ctx
}

func (c *workspaceInputCollector) sourceCall(req *dagql.CallRequest) {
	ws, err := c.workspace(req.Receiver)
	if err != nil {
		c.issue("cannot inspect source workspace: %v", err)
		return
	}
	c.scope = workspaceInputKey(ws)
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
		if gitignore || (len(selector.Include) == 0 && len(selector.Exclude) == 0) {
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

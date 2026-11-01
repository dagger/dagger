package core

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceSelector describes a region that contributes to an artifact.
type WorkspaceSelector struct {
	Path         WorkspacePath `field:"true" doc:"The path relative to the workspace boundary."`
	Include      []string      `field:"true" doc:"Include patterns relative to path."`
	Exclude      []string      `field:"true" doc:"Exclude patterns relative to path."`
	Conservative bool          `field:"true" doc:"Whether this region is an upper bound on possible inputs."`
	Kind         string        `field:"true" doc:"WORKSPACE_READ or MODULE_SOURCE."`
}

func (*WorkspaceSelector) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceSelector", NonNull: true}
}

type WorkspaceProvenance struct {
	Selectors []*WorkspaceSelector `field:"true" doc:"The normalized input regions. Unknown inputs have a conservative root selector."`
}

func (*WorkspaceProvenance) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceProvenance", NonNull: true}
}
func (p *WorkspaceProvenance) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectPayload(p)
}
func (*WorkspaceProvenance) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var p WorkspaceProvenance
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
func (s *WorkspaceSelector) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectPayload(s)
}
func (*WorkspaceSelector) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var s WorkspaceSelector
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
func (p *WorkspaceProvenance) regions() []provenance.Selector {
	out := make([]provenance.Selector, 0, len(p.Selectors))
	for _, s := range p.Selectors {
		out = append(out, provenance.Selector{Path: string(s.Path), Include: s.Include, Exclude: s.Exclude, Conservative: s.Conservative, Kind: s.Kind})
	}
	return out
}
func newWorkspaceProvenance(regions []provenance.Selector) *WorkspaceProvenance {
	p := &WorkspaceProvenance{Selectors: []*WorkspaceSelector{}}
	for _, s := range provenance.Normalize(regions) {
		p.Selectors = append(p.Selectors, &WorkspaceSelector{Path: WorkspacePath(s.Path), Include: s.Include, Exclude: s.Exclude, Conservative: s.Conservative, Kind: s.Kind})
	}
	return p
}
func (p *WorkspaceProvenance) AffectedByPath(path string, kinds []string) (bool, error) {
	return provenance.AffectedByPath(p.regions(), path, kinds)
}
func (p *WorkspaceProvenance) AffectedByDiff(ctx context.Context, changes *Changeset, kinds []string) (bool, error) {
	if err := provenance.ValidateKinds(kinds); err != nil {
		return false, err
	}
	for _, selector := range p.Selectors {
		if selector.Conservative && (len(kinds) == 0 || slices.Contains(kinds, selector.Kind)) {
			return true, nil
		}
	}
	paths, err := changes.ComputePaths(ctx)
	if err != nil {
		return false, err
	}
	// Added/AllRemoved also contain directories. Treat them as regions so a
	// removed subtree still matches a selector for an input inside it.
	for _, path := range paths.Modified {
		match, err := provenance.AffectedByFile(p.regions(), path, kinds)
		if err != nil || match {
			return match, err
		}
	}
	for _, path := range slices.Concat(paths.Added, paths.AllRemoved) {
		var match bool
		if strings.HasSuffix(path, "/") {
			match, err = p.AffectedByPath(path, kinds)
		} else {
			match, err = provenance.AffectedByFile(p.regions(), path, kinds)
		}
		if err != nil || match {
			return match, err
		}
	}
	return false, nil
}

func conservativeProvenance(kind string) provenance.Selector {
	return provenance.Selector{Path: "/", Conservative: true, Kind: kind}
}

// Provenance plans a check without running its body. A saved read set is used
// only after validation against the current immutable workspace.
func (a *Artifact) Provenance(ctx context.Context) (*WorkspaceProvenance, error) {
	selectors := a.moduleSourceSelectors()
	unknown := func() (*WorkspaceProvenance, error) {
		return newWorkspaceProvenance(append(selectors, conservativeProvenance(provenance.WorkspaceRead))), nil
	}
	if a.LoadFailure != nil || a.Node == nil || a.Workspace.Self() == nil || a.TypeName != "Check" {
		return unknown()
	}
	ctx, err := a.WorkspaceContext(ctx)
	if err != nil {
		return nil, err
	}
	var value dagql.AnyObjectResult
	if err := a.Evaluate(ctx, &value); err != nil {
		return unknown()
	}
	ctx = suppressWorkspaceReads(ctx)
	check, ok := dagql.UnwrapAs[*Check](value)
	if !ok || check.Receiver.Self() == nil || check.CachePolicy != FunctionCachePolicyDefault {
		return unknown()
	}
	srv, err := dagqlServerForModule(ctx, check.Receiver.Self().Module)
	if err != nil {
		return nil, err
	}
	args := make([]dagql.NamedInput, 0, len(check.Inputs))
	for _, input := range check.Inputs {
		if v, ok := input.Value.(dagql.Input); ok {
			args = append(args, dagql.NamedInput{Name: input.Name, Value: v})
		}
	}
	req, err := check.Receiver.Plan(ctx, srv, dagql.Selector{Field: checkRunFieldName(check.Function), Args: args})
	if err != nil {
		return unknown()
	}
	key, err := workspaceCallKey(ctx, req)
	if err != nil || key == "" {
		return unknown()
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// A stripped Workspace argument must be the workspace being filtered. Other
	// workspaces need separate baselines; keep those checks selected for now.
	currentScope, err := workspaceScopeForValue(ctx, a.Workspace)
	if err != nil || currentScope == nil {
		return unknown()
	}
	refs, err := workspaceRefs(ctx, req.ResultCall)
	if err != nil {
		return unknown()
	}
	for _, ref := range refs {
		value, err := workspaceValueForRef(ctx, srv, cache, ref)
		if err != nil {
			return unknown()
		}
		scope, err := workspaceScopeForValue(ctx, value)
		if err != nil || scope == nil || scope.Identity != currentScope.Identity {
			return unknown()
		}
	}
	candidates, err := cache.CallMetadataCandidates(ctx, md.SessionID, workspaceReadsNamespace, key)
	if err != nil {
		return unknown()
	}
	for _, candidate := range candidates {
		var summary workspaceReadSummary
		if json.Unmarshal(candidate.Payload, &summary) != nil {
			continue
		}
		summary.Selectors = append(slices.Clone(summary.Selectors), selectors...)
		if validateWorkspaceReads(ctx, srv, a.Workspace, summary) {
			return newWorkspaceProvenance(summary.Selectors), nil
		}
	}
	return unknown()
}

func (a *Artifact) moduleSourceSelectors() []provenance.Selector {
	if a.Workspace.Self() == nil {
		return []provenance.Selector{conservativeProvenance(provenance.ModuleSource)}
	}
	var mod *Module
	if a.Node != nil {
		mod = a.Node.Module.Self()
	}
	return moduleSourceSelectors(a.Workspace.Self(), mod)
}

func moduleSourceSelectors(ws *Workspace, mod *Module) []provenance.Selector {
	var selectors []provenance.Selector
	for _, file := range []string{ws.ConfigFile, ws.LockFile} {
		if file != "" {
			selectors = append(selectors, provenance.Selector{Path: path.Clean("/" + file), Kind: provenance.ModuleSource})
		}
	}
	seen := map[*ModuleSource]bool{}
	var source func(*ModuleSource)
	source = func(src *ModuleSource) {
		if src == nil || seen[src] {
			return
		}
		seen[src] = true
		if src.contextIsWorkspace(ws) {
			selectors = append(selectors, provenance.Selector{Path: path.Clean("/" + src.SourceRootSubpath), Kind: provenance.ModuleSource})
			if len(src.RebasedIncludePaths) > 0 {
				selectors = append(selectors, provenance.Selector{Path: "/", Include: slices.Clone(src.RebasedIncludePaths), Kind: provenance.ModuleSource})
			}
		}
		for _, dep := range src.Dependencies {
			source(dep.Self())
		}
		if src.SDKImpl != nil {
			if sdkModule, ok := src.SDKImpl.AsModule(); ok && sdkModule.Self() != nil {
				source(sdkModule.Self().GetSource())
			}
		}
	}
	if mod != nil {
		source(mod.GetSource())
	}
	return selectors
}

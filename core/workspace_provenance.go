package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspaceSelector describes a region that contributes to an artifact.
type WorkspaceSelector struct {
	Path         WorkspacePath `field:"true" doc:"The path relative to the workspace boundary."`
	Include      []string `field:"true" doc:"Include patterns relative to path."`
	Exclude      []string `field:"true" doc:"Exclude patterns relative to path."`
	Conservative bool     `field:"true" doc:"Whether this region is an upper bound on possible inputs."`
	Kind         string   `field:"true" doc:"WORKSPACE_READ or MODULE_SOURCE."`
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
		match, err := p.AffectedByPath(path, kinds)
		if err != nil || match {
			return match, err
		}
	}
	return false, nil
}

func conservativeProvenance(kind string) provenance.Selector {
	return provenance.Selector{Path: "/", Conservative: true, Kind: kind}
}

// Provenance inspects the receiver, never the artifact leaf. Current module
// compiler inputs are not completely mapped to workspace paths, so module
// source invalidation remains conservative in this prototype.
func (a *Artifact) Provenance(ctx context.Context) (*WorkspaceProvenance, error) {
	regions := []provenance.Selector{conservativeProvenance(provenance.ModuleSource)}
	unknown := func() (*WorkspaceProvenance, error) {
		return newWorkspaceProvenance(append(regions, conservativeProvenance(provenance.WorkspaceRead))), nil
	}
	if a.LoadFailure != nil || a.Node == nil || a.Node.Parent == nil || a.Workspace.Self() == nil {
		return unknown()
	}
	// Late Workspace inputs cannot be discovered from receiver construction.
	if obj := a.Node.Parent.ObjectType(); obj != nil {
		if fn, ok := obj.FunctionByName(a.Node.Name); ok {
			for _, arg := range fn.Args {
				if arg.Self().IsWorkspace() {
					return unknown()
				}
			}
		}
	}
	receiver := a.Clone()
	receiver.Node = receiver.Node.Parent
	ctx, err := a.WorkspaceContext(ctx)
	if err != nil {
		return nil, err
	}
	var value dagql.AnyObjectResult
	if err := receiver.Evaluate(ctx, &value); err != nil {
		return nil, fmt.Errorf("artifact provenance receiver: %w", err)
	}
	id, err := value.RecipeID(ctx)
	if err != nil {
		return nil, err
	}
	regions = append(regions, provenanceFromRecipe(id)...)
	// Module code can obtain currentWorkspace without a Workspace argument.
	// Its recipe cannot prove the absence of those reads.
	regions = append(regions, conservativeProvenance(provenance.WorkspaceRead))
	return newWorkspaceProvenance(regions), nil
}

// Recipes are traversed for inspection only. Unrepresented runtime reads are
// covered separately by the caller's conservative selector.
func provenanceFromRecipe(root *call.ID) []provenance.Selector {
	var regions []provenance.Selector
	seen := map[*call.ID]bool{}
	var visit func(*call.ID)
	var literal func(call.Literal)
	literal = func(lit call.Literal) {
		switch v := lit.(type) {
		case *call.LiteralID:
			visit(v.Value())
		case *call.LiteralList:
			for _, item := range v.Values() {
				literal(item)
			}
		case *call.LiteralObject:
			for _, arg := range v.Args() {
				literal(arg.Value())
			}
		}
	}
	visit = func(id *call.ID) {
		if id == nil || seen[id] {
			return
		}
		seen[id] = true
		if id.IsHandle() {
			regions = append(regions, conservativeProvenance(provenance.WorkspaceRead))
			return
		}
		receiver := id.Receiver()
		if receiver != nil && receiver.Type().NamedType() == "Workspace" && (id.Field() == "directory" || id.Field() == "file") {
			s := conservativeProvenance(provenance.WorkspaceRead)
			for _, arg := range id.Args() {
				if arg.Name() == "path" {
					if text, ok := arg.Value().ToInput().(string); ok && strings.HasPrefix(text, "/") {
						if p, err := provenance.Path(text); err == nil {
							s.Path = p
							s.Conservative = false
						}
					}
				}
			}
			if !s.Conservative {
				for _, arg := range id.Args() {
					if arg.Name() != "include" && arg.Name() != "exclude" {
						continue
					}
					if list, ok := arg.Value().(*call.LiteralList); ok {
						for _, item := range list.Values() {
							if text, ok := item.ToInput().(string); ok {
								if arg.Name() == "include" {
									s.Include = append(s.Include, text)
								} else {
									s.Exclude = append(s.Exclude, text)
								}
							}
						}
					}
				}
			}
			regions = append(regions, s)
			return
		}
		if id.Type().NamedType() == "Workspace" {
			regions = append(regions, conservativeProvenance(provenance.WorkspaceRead))
			return
		}
		visit(receiver)
		for _, arg := range slices.Concat(id.Args(), id.ImplicitInputs()) {
			literal(arg.Value())
		}
	}
	visit(root)
	return provenance.Normalize(regions)
}

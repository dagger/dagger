package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
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
	Origin       string        `field:"true" doc:"The input that contributes this dependency."`
}

func (*WorkspaceSelector) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceSelector", NonNull: true}
}

type WorkspaceProvenance struct {
	Selectors []*WorkspaceSelector `field:"true" doc:"The source regions on which the artifact depends."`
	Issues    []string             `field:"true" doc:"Inputs whose source origin could not be determined. These retain a conservative root dependency."`
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
		out = append(out, provenance.Selector{Path: string(s.Path), Include: s.Include, Exclude: s.Exclude, Conservative: s.Conservative, Kind: s.Kind, Origin: s.Origin})
	}
	return out
}
func newWorkspaceProvenance(regions []provenance.Selector) *WorkspaceProvenance {
	p := &WorkspaceProvenance{Selectors: []*WorkspaceSelector{}, Issues: []string{}}
	for _, s := range provenance.Normalize(regions) {
		p.Selectors = append(p.Selectors, &WorkspaceSelector{Path: WorkspacePath(s.Path), Include: s.Include, Exclude: s.Exclude, Conservative: s.Conservative, Kind: s.Kind, Origin: s.Origin})
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
	return p.AffectedByChangedPaths(paths, nil, kinds)
}

// AffectedByChangedPaths limits the changed paths before matching dependencies.
// Intersecting two artifact selections would also match a check that reads an
// unchanged scoped file and a changed file outside that scope.
func (p *WorkspaceProvenance) AffectedByChangedPaths(changes *ChangesetPaths, scopes []WorkspacePath, kinds []string) (bool, error) {
	if err := provenance.ValidateKinds(kinds); err != nil {
		return false, err
	}
	roots := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		root, err := provenance.Path(string(scope))
		if err != nil {
			return false, err
		}
		roots = append(roots, root)
	}
	regions := p.regions()
	for _, changed := range slices.Concat(changes.Modified, changes.Added, changes.AllRemoved) {
		file, err := provenance.Path(changed)
		if err != nil {
			return false, err
		}
		if len(roots) > 0 && !slices.ContainsFunc(roots, func(root string) bool {
			return root == "/" || file == root || strings.HasPrefix(file, root+"/")
		}) {
			continue
		}
		var match bool
		if strings.HasSuffix(changed, "/") {
			match, err = provenance.AffectedByPath(regions, file, kinds)
		} else {
			match, err = provenance.AffectedByFile(regions, file, kinds)
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

// Provenance prepares the artifact's inputs without running its body.
func (a *Artifact) Provenance(ctx context.Context) (*WorkspaceProvenance, error) {
	selectors := a.moduleSourceSelectors()
	for i := range selectors {
		selectors[i].Origin = "module source"
	}
	var issues []string
	if ws := a.Workspace.Self(); ws != nil {
		env, err := workspaceHostEnvSelectors(ctx, ws)
		if err != nil {
			issues = append(issues, "cannot inspect module defaults search paths: "+err.Error())
		}
		selectors = append(selectors, env...)
	}
	if a.LoadFailure != nil {
		issues = append(issues, "module failed to load: "+a.LoadFailure.Message)
		return provenanceWithIssues(selectors, issues), nil
	}
	ctx, frame, err := a.plannedProvenanceCall(ctx)
	if err != nil {
		return nil, err
	}
	if a.Workspace.Self() == nil {
		return nil, fmt.Errorf("artifact provenance requires a workspace")
	}
	workspace := workspaceInputKey(a.Workspace.Self())
	add := func(input *dagql.ResultCall, origin string) error {
		inputs, err := workspaceInputsForCall(ctx, input)
		if err != nil {
			return err
		}
		regions, inputIssues := inputs.forWorkspace(workspace)
		for _, region := range regions {
			if region.Origin != "" && region.Origin != origin {
				region.Origin = origin + ": " + region.Origin
			} else {
				region.Origin = origin
			}
			selectors = append(selectors, region)
		}
		for _, issue := range inputIssues {
			issues = append(issues, origin+": "+issue)
		}
		return nil
	}
	if frame.Receiver != nil {
		if err := add(&dagql.ResultCall{Receiver: frame.Receiver}, "receiver"); err != nil {
			return nil, err
		}
	}
	for _, arg := range frame.Args {
		if err := add(&dagql.ResultCall{Args: []*dagql.ResultCallArg{arg}}, "argument "+arg.Name); err != nil {
			return nil, err
		}
	}
	for _, arg := range frame.ImplicitInputs {
		if err := add(&dagql.ResultCall{ImplicitInputs: []*dagql.ResultCallArg{arg}}, "engine input "+arg.Name); err != nil {
			return nil, err
		}
	}
	return provenanceWithIssues(selectors, issues), nil
}

func (a *Artifact) plannedProvenanceCall(ctx context.Context, inputs ...dagql.NamedInput) (context.Context, *dagql.ResultCall, error) {
	ctx, err := a.WorkspaceContext(ctx)
	if err != nil {
		return ctx, nil, err
	}
	artifact, err := a.withEvaluationServers(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if a.View != nil {
		ctx = dagql.WithResultScope(ctx, a.View)
	}
	node := artifact.Node
	if node.Name == "stale" && node.Parent != nil && node.Parent.ObjectType() != nil && node.Parent.ObjectType().Name == "Generator" {
		node = node.Parent
	}
	frame, err := node.plannedCall(ctx, inputs...)
	if err != nil {
		return ctx, nil, fmt.Errorf("prepare artifact provenance: %w", err)
	}
	return ctx, frame, nil
}

func provenanceWithIssues(selectors []provenance.Selector, issues []string) *WorkspaceProvenance {
	slices.Sort(issues)
	issues = slices.Compact(issues)
	for _, issue := range issues {
		region := conservativeProvenance(provenance.WorkspaceRead)
		region.Origin = issue
		selectors = append(selectors, region)
	}
	p := newWorkspaceProvenance(selectors)
	p.Issues = append(p.Issues, issues...)
	return p
}

func (a *Artifacts) Provenance(ctx context.Context) (*WorkspaceProvenance, error) {
	expanded, err := a.Expand(ctx)
	if err != nil {
		return nil, err
	}
	var selectors []provenance.Selector
	var issues []string
	for _, artifact := range expanded.Entries {
		p, err := artifact.Provenance(ctx)
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, p.regions()...)
		issues = append(issues, p.Issues...)
	}
	return provenanceWithIssues(selectors, issues), nil
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
	selectors := workspaceEnvSelectors(ws.Cwd)
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
		if root, ok := moduleSourceWorkspaceRoot(ws, src); ok {
			selectors = append(selectors, provenance.Selector{Path: path.Join(root, src.SourceRootSubpath), Kind: provenance.ModuleSource})
			if len(src.RebasedIncludePaths) > 0 {
				selectors = append(selectors, provenance.Selector{Path: root, Include: slices.Clone(src.RebasedIncludePaths), Kind: provenance.ModuleSource})
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

func workspaceEnvSelectors(cwd string) []provenance.Selector {
	var selectors []provenance.Selector
	for dir := path.Clean("/" + cwd); ; dir = path.Dir(dir) {
		selectors = append(selectors, provenance.Selector{Path: path.Join(dir, ".env"), Kind: provenance.ModuleSource})
		if dir == "/" {
			return selectors
		}
	}
}

func workspaceHostEnvSelectors(ctx context.Context, ws *Workspace) ([]provenance.Selector, error) {
	root, ok := ws.LocalSourceHostPath()
	if !ok {
		return nil, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	md, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, err
	}
	ctx = engine.ContextWithClientMetadata(ctx, md)
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, err
	}
	cwd, err := bk.AbsPath(ctx, ".")
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return nil, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil
	}
	selectors := workspaceEnvSelectors(filepath.ToSlash(rel))
	for i := range selectors {
		selectors[i].Origin = "module defaults search"
	}
	return selectors, nil
}

func moduleSourceWorkspaceRoot(ws *Workspace, src *ModuleSource) (string, bool) {
	if origin := src.Workspace.Self(); origin != nil {
		return "/", workspaceInputKey(origin) == workspaceInputKey(ws)
	}
	if src.Kind == ModuleSourceKindLocal && src.Local != nil {
		if root, ok := ws.LocalSourceHostPath(); ok {
			rel, err := filepath.Rel(root, src.Local.ContextDirectoryPath)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return path.Clean("/" + filepath.ToSlash(rel)), true
			}
		}
	}
	if src.Kind == ModuleSourceKindDir {
		if root, ok := ws.SourceDirectory(); ok {
			rootID, rootErr := root.ID()
			dir := src.ContextDirectory
			if src.DirSrc != nil && src.DirSrc.OriginalContextDir.Self() != nil {
				dir = src.DirSrc.OriginalContextDir
			}
			dirID, dirErr := dir.ID()
			if rootErr == nil && dirErr == nil && rootID != nil && dirID != nil && rootID.EngineResultID() != 0 && rootID.EngineResultID() == dirID.EngineResultID() {
				return "/", true
			}
		}
	}
	return "", false
}

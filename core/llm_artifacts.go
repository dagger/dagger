package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
)

// Artifacts returns the conversation's scope: every artifact its addresses
// can name, as one unfiltered selection (hack/designs/collections-issue.md).
// It has two parts:
//
//   - Bound tool objects (LLM.withTools), evaluated from their LIVE values,
//     so an address sees the state the tools have now, not a fresh
//     construction. Per module: if its main object is bound, only the main
//     object's tree; otherwise the tree of every bound object of that module.
//     Either way paths are qualified with the module name (see
//     BoundArtifacts). A binding that fails to load is reported as a
//     "<module>/load" check, like a workspace module that fails to load.
//   - The workspace's artifacts (Workspace.artifacts): the workspace the
//     conversation is bound to, else the one bound into ctx, else the
//     client's current workspace, else none. A workspace module named like a
//     module with bound objects is dropped: the bound one shadows it, so an
//     address naming the module never silently resolves against a fresh
//     construction instead of the live tools.
//
// include narrows the selection like Workspace.artifacts(include:): each
// pattern selects a path and its children, and only the workspace modules it
// names are loaded. nil selects everything.
func (m *MCP) Artifacts(ctx context.Context, srv *dagql.Server, include []string) (*Artifacts, error) {
	bound, shadowed, err := m.boundScopeArtifacts(ctx, srv)
	if err != nil {
		return nil, err
	}
	workspace, err := m.scopeWorkspaceArtifacts(ctx, srv, include)
	if err != nil {
		return nil, err
	}
	return mergeScopeArtifacts(bound, shadowed, workspace, include)
}

// mergeScopeArtifacts combines the bound and workspace parts of a scope. A
// workspace artifact of a shadowed module (by CLI-case module name) is
// dropped. include narrows the bound part the way Workspace.artifacts already
// narrowed the workspace part, and is recorded as the selection's paths.
func mergeScopeArtifacts(bound []*Artifact, shadowed map[string]bool, workspace []*Artifact, include []string) (*Artifacts, error) {
	result := &Artifacts{Entries: []*Artifact{}}
	if include != nil {
		result.Selector.Paths = make([]string, 0, len(include))
		for _, pattern := range include {
			result.Selector.Paths = append(result.Selector.Paths, IncludePattern(pattern))
		}
	}
	for _, entry := range bound {
		match, err := matchesInclude(entry, result.Selector.Paths)
		if err != nil {
			return nil, err
		}
		if match {
			result.Entries = append(result.Entries, entry)
		}
	}
	for _, entry := range workspace {
		if !shadowed[ArtifactTypeName(entry.ModuleName)] {
			result.Entries = append(result.Entries, entry)
		}
	}
	slices.SortStableFunc(result.Entries, func(a, b *Artifact) int { return slices.Compare(a.Path, b.Path) })
	return result, nil
}

// matchesInclude reports whether an artifact matches any include pattern (in
// DAG address form, see IncludePattern). No patterns match everything, as in
// Workspace.artifacts.
func matchesInclude(entry *Artifact, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}
	for _, pattern := range patterns {
		match, err := entry.matchesPattern(pattern)
		if err != nil || match {
			return match, err
		}
	}
	return false, nil
}

// scopeBinding is a bound tool object of a module, as a scope candidate.
type scopeBinding struct {
	typeName string
	module   dagql.ObjectResult[*Module]
	// moduleName is the module's name in CLI case, as addresses spell it.
	moduleName string
	// main reports whether the object is the module's main object.
	main bool
}

// selectScopeBindings groups bindings by module, in binding order, and picks
// the ones whose trees enter the scope: the module's main object alone when
// it is bound — the module's whole address space, of which its other objects
// are views (see boundToolRoot) — otherwise every bound object of the module.
func selectScopeBindings(bindings []scopeBinding) (modules []string, selected map[string][]scopeBinding) {
	selected = map[string][]scopeBinding{}
	for _, b := range bindings {
		if _, ok := selected[b.moduleName]; !ok {
			modules = append(modules, b.moduleName)
		}
		selected[b.moduleName] = append(selected[b.moduleName], b)
	}
	for name, group := range selected {
		if i := slices.IndexFunc(group, func(b scopeBinding) bool { return b.main }); i >= 0 {
			selected[name] = group[i : i+1]
		}
	}
	return modules, selected
}

// boundScopeArtifacts discovers the artifacts of the bound tool objects, and
// the CLI-case names of their modules, which shadow workspace modules.
func (m *MCP) boundScopeArtifacts(ctx context.Context, srv *dagql.Server) ([]*Artifact, map[string]bool, error) {
	var bindings []scopeBinding
	m.mu.Lock()
	for _, b := range m.boundTools {
		mod, ok := ModuleObjectTypeModule(b.objType)
		if !ok {
			// A core object bound as tools has no module to address it by.
			continue
		}
		binding := scopeBinding{typeName: b.typeName(), module: mod, moduleName: ArtifactTypeName(mod.Self().Name())}
		if main, ok := mod.Self().MainObject(); ok && main.Name == binding.typeName {
			binding.main = true
		}
		bindings = append(bindings, binding)
	}
	m.mu.Unlock()

	modules, selected := selectScopeBindings(bindings)
	shadowed := make(map[string]bool, len(modules))
	var entries []*Artifact
	for _, name := range modules {
		shadowed[name] = true
		for _, binding := range selected[name] {
			artifacts, err := m.bindingArtifacts(ctx, srv, binding)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, nil, ctxErr
				}
				// Like a workspace module that fails to load: report it
				// in place of the tree, which still shadows the workspace.
				modName := binding.module.Self().Name()
				entries = append(entries, &Artifact{
					ModuleName: modName, Path: []string{name, "load"}, DimensionKeys: []*ArtifactDimensionKey{},
					Directives: []string{"check"}, TypeName: "Check",
					LoadFailure: &ModuleLoadFailure{Name: modName, Message: err.Error()},
				})
				continue
			}
			entries = append(entries, artifacts.Entries...)
		}
	}
	return entries, shadowed, nil
}

// bindingArtifacts discovers one bound object's artifacts, rooted at its
// current value. A lazy binding (restored from a persisted session) is loaded
// here, as it would be for a tool call.
func (m *MCP) bindingArtifacts(ctx context.Context, srv *dagql.Server, binding scopeBinding) (*Artifacts, error) {
	root, ok, err := m.boundToolObject(ctx, srv, binding.typeName)
	if err == nil && !ok {
		err = fmt.Errorf("no object of type %q is bound", binding.typeName)
	}
	if err != nil {
		return nil, err
	}
	return BoundArtifacts(ctx, binding.module, root)
}

// scopeWorkspaceArtifacts selects Workspace.artifacts on the scope's
// workspace, so its discovery rules (load failures, SDK generators,
// entrypoint shorthand) apply unchanged. No workspace means no artifacts.
func (m *MCP) scopeWorkspaceArtifacts(ctx context.Context, srv *dagql.Server, include []string) ([]*Artifact, error) {
	srv = srv.Canonical()
	workspaceType, ok := srv.ObjectType("Workspace")
	if ok {
		_, ok = workspaceType.FieldSpec("artifacts", srv.View)
	}
	if !ok {
		return nil, nil
	}
	ws := m.workspace
	if ws.Self() == nil {
		ws, _ = WorkspaceFromContext(ctx)
	}
	if ws.Self() == nil {
		// As for address resolution (resolveUserAddress), an unbound
		// conversation works in the client's current workspace.
		if err := srv.Select(ctx, srv.Root(), &ws, dagql.Selector{View: srv.View, Field: "currentWorkspace"}); err != nil {
			if errors.Is(err, ErrNoCurrentWorkspace) {
				return nil, nil
			}
			return nil, fmt.Errorf("load current workspace: %w", err)
		}
	}
	sel := dagql.Selector{View: srv.View, Field: "artifacts"}
	if include != nil {
		sel.Args = []dagql.NamedInput{{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(include...))}}
	}
	var artifacts dagql.ObjectResult[*Artifacts]
	if err := srv.Select(ctx, ws, &artifacts, sel); err != nil {
		return nil, fmt.Errorf("workspace artifacts: %w", err)
	}
	entries := make([]*Artifact, 0, len(artifacts.Self().Entries))
	for _, entry := range artifacts.Self().Entries {
		entries = append(entries, entry.Clone())
	}
	return entries, nil
}

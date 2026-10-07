package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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
//     conversation is bound to (LLM.withWorkspace), or none for an unbound
//     conversation. A workspace module named like a module with bound
//     objects is dropped: the bound one shadows it, so an address naming the
//     module never silently resolves against a fresh construction instead of
//     the live tools. Bound objects that are themselves only a fresh
//     construction have no live state to protect, so there the workspace's
//     module is kept instead (see mergeScopeArtifacts).
//
// include narrows the selection like Workspace.artifacts(include:): each
// pattern selects a path and its children, and only the workspace modules it
// names are loaded. nil selects everything.
func (m *MCP) Artifacts(ctx context.Context, srv *dagql.Server, include []string) (*Artifacts, error) {
	bound, shadowed, fresh, err := m.boundScopeArtifacts(ctx, srv)
	if err != nil {
		return nil, err
	}
	ws := m.scopeWorkspace(srv)
	workspace := &Artifacts{}
	if ws.Self() != nil {
		workspace, err = scopeWorkspaceArtifacts(ctx, srv, ws, include)
		if err != nil {
			return nil, err
		}
	}
	// A bound artifact evaluates in the conversation's workspace, whoever
	// evaluates it: a module function receiving the selection does not
	// inherit the conversation's context.
	for _, entry := range bound {
		entry.ContextWorkspace = ws
	}
	return mergeScopeArtifacts(bound, shadowed, fresh, workspace, include)
}

// mergeScopeArtifacts combines the bound and workspace parts of a scope. A
// workspace artifact of a shadowed module (by CLI-case module name) is
// dropped, unless the module's bindings are fresh constructions (see
// isFreshConstruction) and the workspace has the module: its artifacts are
// then the same values, and the workspace's are the ones `dagger list` shows
// (with entrypoint shorthand, SDK generators and cloud checks), so they win
// over the bound ones. include narrows the bound part the way
// Workspace.artifacts already narrowed the workspace part, and is recorded as
// the selection's paths.
func mergeScopeArtifacts(bound []*Artifact, shadowed, fresh map[string]bool, workspace *Artifacts, include []string) (*Artifacts, error) {
	// Names resolve in both parts, including entries that include skips.
	result := &Artifacts{Entries: []*Artifact{}, AllDimensions: artifact.Union(workspace.AllDimensions, (&Artifacts{Entries: bound}).DimensionDefinitions())}
	if include != nil {
		result.Selector.Paths = make([]string, 0, len(include))
		for _, pattern := range include {
			result.Selector.Paths = append(result.Selector.Paths, IncludePatterns(pattern)...)
		}
	}
	// A fresh binding yields to a workspace module that loaded; not to one
	// that failed to, whose failure would hide the working binding.
	yields := map[string]bool{}
	for _, entry := range workspace.Entries {
		if name := ArtifactTypeName(entry.ModuleName); fresh[name] && entry.LoadFailure == nil {
			yields[name] = true
		}
	}
	for _, entry := range bound {
		if yields[ArtifactTypeName(entry.ModuleName)] {
			continue
		}
		match, err := matchesInclude(entry, result.Selector.Paths)
		if err != nil {
			return nil, err
		}
		if match {
			result.Entries = append(result.Entries, entry)
		}
	}
	for _, entry := range workspace.Entries {
		if name := ArtifactTypeName(entry.ModuleName); !shadowed[name] || yields[name] {
			result.Entries = append(result.Entries, entry)
		}
	}
	slices.SortStableFunc(result.Entries, func(a, b *Artifact) int { return slices.Compare(a.Path, b.Path) })
	result.NameEntries()
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
// are views — otherwise every bound object of the module.
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

// boundScopeArtifacts discovers the artifacts of the bound tool objects, the
// CLI-case names of their modules, which shadow workspace modules, and which
// of those modules are bound only as fresh constructions (see
// isFreshConstruction).
func (m *MCP) boundScopeArtifacts(ctx context.Context, srv *dagql.Server) ([]*Artifact, map[string]bool, map[string]bool, error) {
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
	fresh := map[string]bool{}
	var entries []*Artifact
	for _, name := range modules {
		shadowed[name] = true
		fresh[name] = true
		var trees []boundTree
		var failure *Artifact
		for _, binding := range selected[name] {
			root, artifacts, err := m.bindingArtifacts(ctx, srv, binding)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, nil, nil, ctxErr
				}
				fresh[name] = false
				// Like a workspace module that fails to load: report it
				// in place of the tree, which still shadows the workspace.
				// One check per module, naming each binding that failed.
				msg := err.Error()
				if len(selected[name]) > 1 {
					msg = "bound " + binding.typeName + ": " + msg
				}
				if failure != nil {
					failure.LoadFailure.Message += "\n" + msg
					continue
				}
				modName := binding.module.Self().Name()
				failure = &Artifact{
					ModuleName: modName, Path: []string{name, "load"}, DimensionKeys: []*ArtifactDimensionKey{},
					Directives: []string{"check"}, TypeName: "Check",
					LoadFailure: &ModuleLoadFailure{Name: modName, Message: msg},
				}
				entries = append(entries, failure)
				continue
			}
			if !isFreshConstruction(ctx, binding.module.Self(), root) {
				fresh[name] = false
			}
			trees = append(trees, boundTree{typeName: binding.typeName, entries: artifacts.Entries})
		}
		qualifyCollidingTrees(trees)
		for _, tree := range trees {
			entries = append(entries, tree.entries...)
		}
	}
	return entries, shadowed, fresh, nil
}

// isFreshConstruction reports whether a bound object is a plain construction
// of its module's main object: the module's constructor called from the
// query root with no arguments but a Workspace. It then carries no state of
// its own, so its artifacts are the values the workspace's module would give
// — `dagger mcp`, for instance, binds every workspace module that way — and
// there is no live state for the workspace's to hide.
func isFreshConstruction(ctx context.Context, mod *Module, root dagql.AnyObjectResult) bool {
	main, ok := mod.MainObject()
	if !ok || root.Type().Name() != main.Name {
		return false
	}
	// The recipe, not the runtime handle: a handle is opaque, with no call
	// to inspect.
	id, err := root.RecipeID(ctx)
	if err != nil || id == nil || id.IsHandle() || id.Receiver() != nil || id.Field() != mod.ConstructorName() {
		return false
	}
	for _, arg := range id.Args() {
		lit, ok := arg.Value().(*call.LiteralID)
		if !ok || lit.Value().Type().NamedType() != workspaceTypeName {
			return false
		}
	}
	return true
}

// boundTree is the artifacts of one bound object.
type boundTree struct {
	typeName string
	entries  []*Artifact
}

// qualifyCollidingTrees disambiguates bound objects of one module whose trees
// share a path, e.g. two narrower views of the module that both have a
// members field: dag://staff/members/head would name an artifact of each, and
// no address could tell them apart. Each colliding tree is rooted one level
// down instead, at a segment naming the bound object's type:
// dag://staff/staff-pull-tools/members/head. The unqualified address still
// matches the artifact (see Artifact.matchesPattern), so it selects them all,
// and where one is required the error lists the qualified addresses to choose
// from. Trees that collide with no other keep their plain paths.
func qualifyCollidingTrees(trees []boundTree) {
	if len(trees) < 2 {
		return
	}
	owners := map[string]int{}
	for _, tree := range trees {
		paths := map[string]bool{}
		for _, entry := range tree.entries {
			paths[strings.Join(entry.Path, "/")] = true
		}
		for path := range paths {
			owners[path]++
		}
	}
	for _, tree := range trees {
		if slices.ContainsFunc(tree.entries, func(entry *Artifact) bool { return owners[strings.Join(entry.Path, "/")] > 1 }) {
			qualifyBoundTree(tree.entries, tree.typeName)
		}
	}
}

// qualifyBoundTree inserts a segment naming the bound type between the module
// and the bound object's fields, in the tree and in each artifact's path. The
// root keeps its value, so evaluation is unchanged.
func qualifyBoundTree(entries []*Artifact, typeName string) {
	qualified := map[*ModTreeNode]bool{}
	for _, entry := range entries {
		root := entry.boundRootNode()
		if root == nil {
			continue
		}
		if !qualified[root] {
			qualified[root] = true
			root.Parent = &ModTreeNode{Name: root.Name, Parent: root.Parent}
			root.Name = typeName
		}
		entry.Path = entry.Node.CLIPath()
	}
}

// bindingArtifacts discovers one bound object's artifacts, rooted at its
// current value, which it returns too. A lazy binding (restored from a
// persisted session) is loaded here, as it would be for a tool call.
func (m *MCP) bindingArtifacts(ctx context.Context, srv *dagql.Server, binding scopeBinding) (dagql.AnyObjectResult, *Artifacts, error) {
	root, ok, err := m.boundToolObject(ctx, srv, binding.typeName)
	if err == nil && !ok {
		err = fmt.Errorf("no object of type %q is bound", binding.typeName)
	}
	if err != nil {
		return nil, nil, err
	}
	artifacts, err := BoundArtifacts(ctx, binding.module, root)
	return root, artifacts, err
}

// scopeWorkspace is the workspace of the scope: the conversation's bound
// workspace, and only that. An unbound conversation (Query.llm starts
// unbound) has no workspace part, whatever the calling client's current
// workspace is, so the scope depends only on the conversation's own recipe.
// None (a zero result) as well when the schema view cannot discover workspace
// artifacts.
func (m *MCP) scopeWorkspace(srv *dagql.Server) dagql.ObjectResult[*Workspace] {
	if m.workspace.Self() == nil || !schemaHasWorkspaceArtifacts(srv) {
		return dagql.ObjectResult[*Workspace]{}
	}
	return m.workspace
}

// schemaHasWorkspaceArtifacts reports whether the schema view can discover a
// workspace's artifacts (Workspace.artifacts).
func schemaHasWorkspaceArtifacts(srv *dagql.Server) bool {
	srv = srv.Canonical()
	workspaceType, ok := srv.ObjectType("Workspace")
	if !ok {
		return false
	}
	_, ok = workspaceType.FieldSpec("artifacts", srv.View)
	return ok
}

// scopeWorkspaceArtifacts selects Workspace.artifacts on the scope's
// workspace, so its discovery rules (load failures, SDK generators,
// entrypoint shorthand) apply unchanged.
func scopeWorkspaceArtifacts(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace], include []string) (*Artifacts, error) {
	srv = srv.Canonical()
	sel := dagql.Selector{View: srv.View, Field: "artifacts"}
	if include != nil {
		sel.Args = []dagql.NamedInput{{Name: "include", Value: dagql.Opt(dagql.ArrayInput[dagql.String](dagql.NewStringArray(include...)))}}
	}
	var artifacts dagql.ObjectResult[*Artifacts]
	if err := srv.Select(ctx, ws, &artifacts, sel); err != nil {
		return nil, fmt.Errorf("workspace artifacts: %w", err)
	}
	entries := make([]*Artifact, 0, len(artifacts.Self().Entries))
	for _, entry := range artifacts.Self().Entries {
		entries = append(entries, entry.Clone())
	}
	return &Artifacts{Entries: entries, AllDimensions: artifacts.Self().AllDimensions}, nil
}

// scopeLLM returns the conversation whose scope (LLM.artifacts) a tool
// argument's address resolves in: the conversation dispatching the call — or,
// for a server without one (dagger mcp), the conversation it serves — with
// the changes earlier calls of this turn made to its workspace and bindings
// folded in, as step() folds them before continuations. So an address sees
// the tools' state as of this call, and the value lifted from it has a
// recipe: the conversation's own artifacts, filtered by the address.
func (m *MCP) scopeLLM(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*LLM], error) {
	base := m.currentLLM()
	if base.Self() == nil {
		base = m.scopeBase
	}
	if base.Self() == nil {
		return base, errors.New("no conversation to resolve the address in")
	}
	wsBefore, err := base.Self().mcp.WorkspaceID()
	if err != nil {
		return base, err
	}
	toolsBefore, err := base.Self().mcp.BoundToolBindings()
	if err != nil {
		return base, err
	}
	sels := stateDeltaSelectors(m, wsBefore, toolsBefore)
	if len(sels) == 0 {
		return base, nil
	}
	srv = srv.Canonical()
	for i := range sels {
		sels[i].View = srv.View
	}
	var folded dagql.ObjectResult[*LLM]
	if err := srv.Select(ctx, base, &folded, sels...); err != nil {
		return base, fmt.Errorf("record this turn's tool state: %w", err)
	}
	return folded, nil
}

// scopeSelectors select a DAG address in a conversation's scope:
// artifacts(include: [<path>]).filterUri(uri: <uri>). As for workspace
// resolution (Workspace.resolve), the path narrows which workspace modules
// load.
func scopeSelectors(srv *dagql.Server, parsed *dagaddress.Address, uri string) []dagql.Selector {
	artifacts := dagql.Selector{View: srv.View, Field: "artifacts"}
	if parsed.Path != "" {
		artifacts.Args = []dagql.NamedInput{{Name: "include", Value: dagql.Opt(dagql.ArrayInput[dagql.String](dagql.NewStringArray(parsed.Path)))}}
	}
	return []dagql.Selector{artifacts, {
		View: srv.View, Field: "filterUri",
		Args: []dagql.NamedInput{{Name: "uri", Value: dagql.String(uri)}},
	}}
}

// liftScopeSelection lifts a DAG address, with or without the dag://
// scheme, into the part of the conversation's scope it selects: the
// Artifacts selection, which must not be empty, or with one, the Artifact it
// must select exactly. The result is selected through the conversation
// itself, so its ID is a real recipe that a module function receiving it can
// load and evaluate.
func (m *MCP) liftScopeSelection(ctx context.Context, srv *dagql.Server, addr string, one bool) (dagql.AnyObjectResult, error) {
	parsed, err := dagaddress.Parse(addr)
	if err != nil {
		return nil, err
	}
	llm, err := m.scopeLLM(ctx, srv)
	if err != nil {
		return nil, err
	}
	srv = srv.Canonical()
	sels := scopeSelectors(srv, parsed, addr)
	var selection dagql.ObjectResult[*Artifacts]
	if err := srv.Select(ctx, llm, &selection, sels...); err != nil {
		return nil, err
	}
	// A filter matching nothing is a valid selection, but an address the
	// model typed that names nothing is a mistake: passed on, a function
	// like check would succeed without running anything. Count the items,
	// not the schema paths: a collection path with an unknown key is a path
	// with no items.
	if err := requireScopeMatch(ctx, selection.Self(), parsed, one); err != nil {
		return nil, err
	}
	if !one {
		return selection, nil
	}
	var artifact dagql.ObjectResult[*Artifact]
	if err := srv.Select(ctx, llm, &artifact, append(sels, dagql.Selector{View: srv.View, Field: "one"})...); err != nil {
		return nil, err
	}
	return artifact, nil
}

// requireScopeMatch requires a selection to have items: exactly one when one
// is set. Collections are expanded to count their items, as Artifacts.one
// does. The error names the address as given, not the selection's normalized
// URI, which spells an empty selection "dag://{}" and a collection key by its
// dimension's identifier. It is built here rather than by Artifacts.one so
// the tool result is the message alone, without the trace report an engine
// error carries into it.
func requireScopeMatch(ctx context.Context, selection *Artifacts, addr *dagaddress.Address, one bool) error {
	if len(selection.Entries) > 0 {
		var err error
		selection, err = selection.Expand(ctx)
		if err != nil {
			return err
		}
	}
	switch n := len(selection.Entries); {
	case n == 0:
		return fmt.Errorf("no artifact matches %s", addr)
	case n == 1 || !one:
		return nil
	}
	lines := make([]string, 0, len(selection.Entries))
	for _, artifact := range selection.Entries {
		uri, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return err
		}
		lines = append(lines, uri)
	}
	return fmt.Errorf("%s matches %d artifacts:\n%s", addr, len(selection.Entries), strings.Join(lines, "\n"))
}

// resolveScopeObject evaluates the one value a relative DAG address names in
// the conversation's scope: its bound tools with their live state, and its
// workspace. It is LLM.artifacts(include: [<path>]).filterUri(<addr>).one.value,
// with Workspace.resolve's rules: a type assertion only chooses among
// several matches, so an artifact of another type is reported as such, and
// the value must have the argument's type. Artifact.value evaluates each
// artifact in its own workspace (or, for a bound tool's, the conversation's).
func (m *MCP) resolveScopeObject(ctx context.Context, srv *dagql.Server, parsed *dagaddress.Address, addr, typeName string) (dagql.AnyObjectResult, error) {
	llm, err := m.scopeLLM(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	srv = srv.Canonical()
	untyped := *parsed
	untyped.Types = nil
	var selection dagql.ObjectResult[*Artifacts]
	if err := srv.Select(ctx, llm, &selection, scopeSelectors(srv, parsed, untyped.String())...); err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	if len(parsed.Types) > 0 && len(selection.Self().Entries) > 1 {
		typed := dagaddress.Address{HasScheme: true, Types: parsed.Types}
		if err := srv.Select(ctx, selection, &selection, dagql.Selector{
			View: srv.View, Field: "filterUri",
			Args: []dagql.NamedInput{{Name: "uri", Value: dagql.String(typed.String())}},
		}); err != nil {
			return nil, fmt.Errorf("resolve %q: %w", addr, err)
		}
	}
	if err := requireScopeMatch(ctx, selection.Self(), parsed, true); err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	var one dagql.ObjectResult[*Artifact]
	if err := srv.Select(ctx, selection, &one, dagql.Selector{View: srv.View, Field: "one"}); err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	artifact := one.Self()
	if err := artifact.AssertType(parsed.Types); err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	if artifact.TypeName != typeName {
		return nil, fmt.Errorf("resolve %q: artifact is a %s, not a %s", addr, artifact.TypeName, typeName)
	}
	// Guard against reference cycles, as Workspace.resolve does.
	normalized, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	ctx, err = WithArtifactReference(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	// Evaluation is user-facing work of the tool call: keep its spans
	// visible, as for any other address (resolveObjectAddress).
	var obj dagql.AnyObjectResult
	if err := srv.Select(dagql.WithNonInternalTelemetry(ctx), one, &obj, dagql.Selector{View: srv.View, Field: "value"}); err != nil {
		return nil, fmt.Errorf("resolve %q: %w", addr, err)
	}
	return obj, nil
}

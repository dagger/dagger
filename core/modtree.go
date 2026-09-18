package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"

	"github.com/dagger/dagger/util/parallel"
	"github.com/iancoleman/strcase"
)

type ModTreeNode struct {
	RootValue   dagql.AnyObjectResult
	Parent      *ModTreeNode
	Name        string
	Description string
	DagqlServer *dagql.Server
	// This module is the same across all ModTreeNode, this is the root module.
	Module dagql.ObjectResult[*Module]
	// This original module is the one in which the node has been defined.
	OriginalModule dagql.ObjectResult[*Module]
	Type           dagql.ObjectResult[*TypeDef]
	Directives     []string
	types          map[string]dagql.ObjectResult[*TypeDef]

	// WorkspaceEntrypoint is set on the module root when its targets are
	// exposed without a module prefix. Path retains the qualified identity.
	WorkspaceEntrypoint bool
	CollectionDimension *ArtifactDimension
	CollectionKey       *string
}

func (node *ModTreeNode) Path() ModTreePath {
	if node.Parent == nil {
		return nil
	}
	if node.CollectionDimension != nil {
		return node.Parent.Path()
	}
	var path ModTreePath
	path = append(path, node.Parent.Path()...)
	path = append(path, node.Name)
	return path
}

func NewModTree(ctx context.Context, mod dagql.ObjectResult[*Module]) (*ModTreeNode, error) {
	main := mod.Self()
	mainType, ok := main.mainObjectTypeDefResult()
	if !ok {
		return nil, fmt.Errorf("%q: no main object", main.Name())
	}
	srv, err := dagqlServerForModule(ctx, mod)
	if err != nil {
		return nil, err
	}
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	defaults, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, err
	}
	types := map[string]dagql.ObjectResult[*TypeDef]{}
	// Discover core fields in the caller's view, including fields added after
	// the module's authored version. Execution still uses the module's deps.
	for _, dep := range slices.Concat(main.Deps.Mods(), defaults.Mods()) {
		defs, err := dep.TypeDefs(ctx, srv)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			if def.Self().AsObject.Valid {
				types[def.Self().AsObject.Value.Self().Name] = def
			}
		}
	}
	for _, def := range main.ObjectDefs {
		types[def.Self().AsObject.Value.Self().Name] = def
	}
	return &ModTreeNode{
		types:          types,
		DagqlServer:    srv,
		Module:         mod,
		OriginalModule: mod,
		Type:           mainType,
		Description:    main.Description,
	}, nil
}

// ServiceNameAttr identifies the service displayed by the frontend.
const ServiceNameAttr = telemetryattrs.ServiceNameAttr

// Initialize a standalone dagql server for querying the given module
func dagqlServerForModule(ctx context.Context, mod dagql.ObjectResult[*Module]) (*dagql.Server, error) {
	main := mod.Self()
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := dagql.NewServer(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("create module dagql server: %w", err)
	}
	srv.Around(AroundFunc)
	InstallCoreSchemaLoaders(srv)
	// Install default "dependencies" (ie the core)
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("%q: load core schema: %w", main.Name(), err)
	}
	// Install dependencies
	for _, defaultDep := range defaultDeps.Mods() {
		if err := defaultDep.Install(ctx, srv); err != nil {
			return nil, fmt.Errorf("%q: serve core schema: %w", main.Name(), err)
		}
	}
	// Install the main module
	if err := NewUserMod(mod).Install(ctx, srv); err != nil {
		return nil, fmt.Errorf("%q: serve module: %w", main.Name(), err)
	}
	return srv, nil
}

func (node *ModTreeNode) Clone() *ModTreeNode {
	cp := *node
	return &cp
}

func (node *ModTreeNode) DagqlValue(ctx context.Context, dest any) error {
	return node.dagqlValue(ctx, dest, nil)
}

// dagqlValue selects the node's value, passing leafArgs as arguments to the
// final Select (the leaf function). Parent objects are always auto-constructed
// with defaults (no leafArgs), so leafArgs only ever fill the leaf itself — e.g.
// the @agent fold supplies `base` here.
func (node *ModTreeNode) dagqlValue(ctx context.Context, dest any, leafArgs []dagql.NamedInput) error {
	// We can't direct-select the dagql path, because Select() doesn't support traversing
	// lists
	// FIXME: as an optimization, one-shot when possible?
	srv := node.DagqlServer
	if node.RootValue != nil {
		return srv.Select(ctx, node.RootValue, dest)
	}
	// 1. Are we the root? Select the module's main object from Query root.
	// A node is also treated as root if its parent is a synthetic naming-only
	// node (e.g. injected by workspace checks reparenting, which sets
	// Parent to an empty ModTreeNode with nil Module).
	if node.Parent == nil || (node.Parent.Module.Self() == nil && node.Parent.RootValue == nil && node.Parent.Type.Self() == nil) {
		mod := node.Module.Self()
		if mod == nil {
			return fmt.Errorf("%q: get value: missing module", node.PathString())
		}
		var ctor *Function
		if objType := node.ObjectType(); objType != nil && objType.Constructor.Valid {
			ctor = objType.Constructor.Value.Self()
		}
		args, err := boundWorkspaceArgs(ctx, srv, ctor)
		if err != nil {
			return fmt.Errorf("%q: get value: %w", node.PathString(), err)
		}
		return srv.Select(ctx, srv.Root(), dest, dagql.Selector{
			Field: gqlFieldName(mod.Name()),
			Args:  append(args, leafArgs...),
		})
	}
	// 2. Is parent an object?
	if parentObjType := node.Parent.ObjectType(); parentObjType != nil {
		var parentObjValue dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentObjValue); err != nil {
			return err
		}
		fn, _ := parentObjType.FunctionByName(node.Name)
		args, err := boundWorkspaceArgs(ctx, srv, fn)
		if err != nil {
			return fmt.Errorf("%q: get value: %w", node.PathString(), err)
		}
		if node.CollectionDimension != nil {
			if node.CollectionKey == nil {
				return fmt.Errorf("collection item %q has no key", node.CollectionDimension.Identifier)
			}
			members, err := parentObjType.CollectionMembers()
			if err != nil {
				return err
			}
			key, err := collectionInputFromText(members.Get.Args[0].Self().TypeDef.Self(), *node.CollectionKey)
			if err != nil {
				return err
			}
			args = append(args, dagql.NamedInput{Name: "key", Value: key})
		}
		return srv.Select(dagql.WithNonInternalTelemetry(ctx), parentObjValue, dest, dagql.Selector{
			Field: node.Name,
			Args:  append(args, leafArgs...),
		})
	}
	return fmt.Errorf("%q: get value: parent is not an object", node.PathString())
}

func boundWorkspaceArgs(ctx context.Context, srv *dagql.Server, fn *Function) ([]dagql.NamedInput, error) {
	if fn == nil {
		return nil, nil
	}
	var argName string
	for _, argRes := range fn.Args {
		arg := argRes.Self()
		if arg.IsWorkspace() && !arg.TypeDef.Self().Optional {
			argName = arg.Name
			break
		}
	}
	if argName == "" {
		return nil, nil
	}
	wsID, err := workspaceArgValue(ctx, srv)
	if err != nil {
		return nil, err
	}
	if wsID == nil {
		// Nothing to inherit: leave the arg off and let dagql report it as
		// missing, which names both the argument and the field.
		return nil, nil
	}
	return []dagql.NamedInput{{
		Name:  argName,
		Value: wsID,
	}}, nil
}

func debugTrace(ctx context.Context, msg string, args ...any) {
	_ = parallel.
		New().
		WithContextualTracer(true).
		WithJob(fmt.Sprintf(msg, args...), nil).
		Run(ctx)
}

// Walk the tree and return all matching nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupNodes(ctx context.Context, matches func(*ModTreeNode) (bool, bool), include []string, exclude []string) ([]*ModTreeNode, error) {
	var res []*ModTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		// FIXME: prune the search tree more aggressively, for efficiency
		// BUT be careful to not break matching!
		collect, descend := matches(n)
		if collect {
			if len(include) > 0 {
				if match, err := n.Match(ctx, include); err != nil {
					return false, err
				} else if !match {
					debugTrace(ctx, "%q: does not match %v. Skipping", n.PathString(), include)
					return false, nil
				}
			}
			if len(exclude) > 0 {
				if match, err := n.Match(ctx, exclude); err != nil {
					return false, err
				} else if match {
					return false, nil
				}
			}
			res = append(res, n)
			return descend, nil
		}
		return descend, nil
	})
	slices.SortStableFunc(res, func(a, b *ModTreeNode) int {
		return strings.Compare(a.PathString(), b.PathString())
	})
	// Deduplicate by path — a function and its object subtree can both
	// produce the same leaf (e.g. toolchain services appear in both).
	res = slices.CompactFunc(res, func(a, b *ModTreeNode) bool {
		return a.PathString() == b.PathString()
	})
	return res, err
}

type ModTreePath []string

func NewModTreePath(s string) ModTreePath {
	return ModTreePath(strings.Split(s, ":"))
}

func (p ModTreePath) CliCase() []string {
	cliCase := make([]string, len(p))
	for i := range p {
		cliCase[i] = strcase.ToKebab(p[i])
	}
	return cliCase
}

func (p ModTreePath) APICase() []string {
	apiCase := make([]string, len(p))
	for i := range p {
		apiCase[i] = gqlFieldName(p[i])
	}
	return apiCase
}

func (p ModTreePath) Contains(ctx context.Context, target ModTreePath) (result bool) {
	defer func() {
		debugTrace(ctx, "%v.Contains(%v) -> %v", p, target, result)
	}()
	if len(target) < len(p) {
		// if the target is shorter, it can't be a sub-path
		return false
	}
	targetParent := target[:len(p)]
	return p.Equals(ctx, targetParent)
}

func (p ModTreePath) Equals(ctx context.Context, other ModTreePath) (result bool) {
	defer func() {
		debugTrace(ctx, "%v.Equals(%v) -> %v", p, other, result)
	}()
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if gqlFieldName(p[i]) != gqlFieldName(other[i]) {
			debugTrace(ctx, "%v.Equals(%v): %q != %q -> NOT EQUAL", p, other, gqlFieldName(p[i]), gqlFieldName(other[i]))
			return false
		}
	}
	return true
}

func (p ModTreePath) Glob(ctx context.Context, pattern string) (bool, error) {
	// Normalize both pattern and path to CLI case (kebab-case) for consistent matching
	slashPattern := strings.Join(NewModTreePath(pattern).CliCase(), "/")
	slashPath := strings.Join(p.CliCase(), "/")
	if match, err := doublestar.PathMatch(slashPattern, slashPath); err != nil {
		return false, err
	} else if match {
		debugTrace(ctx, "%q.Glob(%q) -> MATCH", slashPath, slashPattern)
		return true, nil
	}
	debugTrace(ctx, "%q.Glob(%q) -> no match", slashPath, slashPattern)
	return false, nil
}

func (node *ModTreeNode) Match(ctx context.Context, patterns []string) (bool, error) {
	if node.Parent == nil {
		// The root node matches everything
		return true, nil
	}
	if len(patterns) == 0 {
		return true, nil
	}
	for _, pattern := range patterns {
		if match, err := node.Path().Glob(ctx, pattern); err != nil {
			return false, err
		} else if match {
			return true, nil
		}
		patternAsPath := NewModTreePath(pattern)
		if patternAsPath.Contains(ctx, node.Path()) {
			return true, nil
		}
	}
	return false, nil
}

func (node *ModTreeNode) PathString() string {
	return strings.Join(node.Path().CliCase(), ":")
}

// CommandPath is the path users type to select this target.
func (node *ModTreeNode) CommandPath() ModTreePath {
	path := node.Path()
	if node.inWorkspaceEntrypoint() && len(path) > 0 {
		return path[1:]
	}
	return path
}

// inWorkspaceEntrypoint reports whether node is in the workspace entrypoint
// module.
func (node *ModTreeNode) inWorkspaceEntrypoint() bool {
	for parent := node; parent != nil; parent = parent.Parent {
		if parent.WorkspaceEntrypoint {
			return true
		}
	}
	return false
}

func (node *ModTreeNode) CommandName() string {
	return strings.Join(node.CommandPath().CliCase(), ":")
}

type WalkFunc func(context.Context, *ModTreeNode) (bool, error)

func (node *ModTreeNode) Walk(ctx context.Context, fn WalkFunc) error {
	return node.walk(ctx, fn, make(map[string]bool))
}

func (node *ModTreeNode) walk(ctx context.Context, fn WalkFunc, visiting map[string]bool) error {
	// The callback is always called so that leaves (checks, services, etc.)
	// are always discovered regardless of cycle state.
	enter, err := fn(ctx, node)
	if err != nil {
		return err
	}
	if !enter {
		return nil
	}

	// Cycle detection: if this node's object type has already been seen
	// along the current path, don't descend into its children.
	// This prevents infinite recursion when e.g. Service.start() returns
	// Service, which has start(), which returns Service, etc.
	var typeName string
	if obj := node.ObjectType(); obj != nil {
		typeName = obj.Name
	}
	if typeName != "" {
		if visiting[typeName] {
			return nil
		}
		visiting[typeName] = true
		defer delete(visiting, typeName)
	}

	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := child.walk(ctx, fn, visiting); err != nil {
			return err
		}
	}
	return nil
}

// Children exposes each reachable schema field once. Full type definitions are
// used for descent; a field's directives remain attached to its own node.
func (node *ModTreeNode) Children(ctx context.Context) ([]*ModTreeNode, error) {
	var children []*ModTreeNode
	obj := node.ObjectType()
	if obj == nil {
		return nil, nil
	}
	add := func(name, description string, typ dagql.ObjectResult[*TypeDef], fn *Function) {
		child := &ModTreeNode{Parent: node, Name: name, Description: description,
			DagqlServer: node.DagqlServer, Module: node.Module, OriginalModule: node.OriginalModule,
			Type: typ, types: node.types}
		if fn != nil {
			for _, directive := range fn.Directives() {
				if isArtifactDirective(directive.Name) {
					child.Directives = append(child.Directives, directive.Name)
				}
			}
		}
		if class, ok := node.DagqlServer.ObjectType(obj.Name); ok {
			if spec, ok := class.FieldSpec(name, node.DagqlServer.View); ok {
				for _, dir := range spec.Directives {
					if isArtifactDirective(dir.Name) && !slices.Contains(child.Directives, dir.Name) {
						child.Directives = append(child.Directives, dir.Name)
					}
				}
			}
		}
		if !typ.Self().Optional && typ.Self().Kind == TypeDefKindObject {
			if full, ok := node.types[typ.Self().ToType().Name()]; ok {
				child.Type = full
			}
		}
		children = append(children, child)
	}
	for _, fnResult := range obj.Functions {
		fn := fnResult.Self()
		if functionRequiresCallerArgs(fn) {
			continue
		}
		add(fn.Name, fn.Description, fn.ReturnType, fn)
	}
	for _, fieldResult := range obj.Fields {
		field := fieldResult.Self()
		add(field.Name, field.Description, field.TypeDef, nil)
	}
	return children, nil
}

func (node *ModTreeNode) ChildrenNames(ctx context.Context) ([]string, error) {
	children, err := node.Children(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(children))
	for i := range children {
		names[i] = children[i].Name
	}
	return names, nil
}

func (node *ModTreeNode) Child(ctx context.Context, name string) (*ModTreeNode, error) {
	children, err := node.Children(ctx)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.Name == name {
			return child, nil
		}
	}
	return nil, nil
}

func (node *ModTreeNode) ObjectType() *ObjectTypeDef {
	if node == nil || node.Type.Self() == nil {
		return nil
	}
	typeDef := node.Type.Self()
	if !typeDef.AsObject.Valid {
		return nil
	}
	return typeDef.AsObject.Value.Self()
}

type persistedModTree struct {
	Nodes []persistedModTreeNode `json:"nodes,omitempty"`
}

type persistedModTreeNode struct {
	CollectionDimension    *ArtifactDimension `json:"collectionDimension,omitempty"`
	CollectionKey          *string            `json:"collectionKey,omitempty"`
	RootValueResultID      uint64             `json:"rootValueResultID,omitempty"`
	ID                     int                `json:"id"`
	ParentID               int                `json:"parentID,omitempty"`
	Name                   string             `json:"name,omitempty"`
	Description            string             `json:"description,omitempty"`
	ModuleResultID         uint64             `json:"moduleResultID,omitempty"`
	OriginalModuleResultID uint64             `json:"originalModuleResultID,omitempty"`
	TypeResultID           uint64             `json:"typeResultID,omitempty"`
	Directives             []string           `json:"directives,omitempty"`
}

type persistedModTreeEncoder struct {
	encCtx   *dagql.PersistEncodeContext
	ids      map[*ModTreeNode]int
	visiting map[*ModTreeNode]bool
	tree     persistedModTree
}

func newPersistedModTreeEncoder(enc *dagql.PersistEncodeContext) *persistedModTreeEncoder {
	return &persistedModTreeEncoder{
		encCtx:   enc,
		ids:      map[*ModTreeNode]int{},
		visiting: map[*ModTreeNode]bool{},
	}
}

func (enc *persistedModTreeEncoder) Add(node *ModTreeNode) (int, error) {
	if node == nil {
		return 0, nil
	}
	if id, ok := enc.ids[node]; ok {
		return id, nil
	}
	if enc.visiting[node] {
		return 0, fmt.Errorf("encode persisted mod tree node %q: parent cycle", node.Name)
	}
	enc.visiting[node] = true
	defer delete(enc.visiting, node)

	parentID, err := enc.Add(node.Parent)
	if err != nil {
		return 0, err
	}

	id := len(enc.tree.Nodes) + 1
	enc.ids[node] = id
	persisted := persistedModTreeNode{
		CollectionDimension: node.CollectionDimension,
		CollectionKey:       node.CollectionKey,
		ID:                  id,
		ParentID:            parentID,
		Name:                node.Name,
		Description:         node.Description,
		Directives:          node.Directives,
	}
	if node.RootValue != nil {
		persisted.RootValueResultID, err = encodePersistedObjectRef(enc.encCtx, node.RootValue, "artifact root value")
		if err != nil {
			return 0, err
		}
	}
	if node.Module.Self() != nil {
		moduleID, err := encodePersistedObjectRef(enc.encCtx, node.Module, "mod tree module")
		if err != nil {
			return 0, err
		}
		persisted.ModuleResultID = moduleID
	}
	if node.OriginalModule.Self() != nil {
		originalModuleID, err := encodePersistedObjectRef(enc.encCtx, node.OriginalModule, "mod tree original module")
		if err != nil {
			return 0, err
		}
		persisted.OriginalModuleResultID = originalModuleID
	}
	if node.Type.Self() != nil {
		typeID, err := encodePersistedObjectRef(enc.encCtx, node.Type, "mod tree typedef")
		if err != nil {
			return 0, err
		}
		persisted.TypeResultID = typeID
	}
	enc.tree.Nodes = append(enc.tree.Nodes, persisted)
	return id, nil
}

func decodePersistedModTree(ctx context.Context, dec *dagql.PersistDecodeContext, tree persistedModTree) (map[int]*ModTreeNode, error) {
	nodes := make(map[int]*ModTreeNode, len(tree.Nodes))
	serverByModuleID := map[uint64]*dagql.Server{}

	for _, persisted := range tree.Nodes {
		if persisted.ID == 0 {
			return nil, fmt.Errorf("decode persisted mod tree: zero node ID")
		}
		if _, exists := nodes[persisted.ID]; exists {
			return nil, fmt.Errorf("decode persisted mod tree: duplicate node ID %d", persisted.ID)
		}

		node := &ModTreeNode{
			CollectionDimension: persisted.CollectionDimension,
			CollectionKey:       persisted.CollectionKey,
			Name:                persisted.Name,
			Description:         persisted.Description,
			Directives:          persisted.Directives,
		}
		if persisted.RootValueResultID != 0 {
			value, err := loadPersistedResultByResultID(ctx, dec, persisted.RootValueResultID, "artifact root value")
			if err != nil {
				return nil, err
			}
			node.RootValue = value.(dagql.AnyObjectResult)
		}
		if persisted.ModuleResultID != 0 {
			module, err := loadPersistedObjectResultByResultID[*Module](ctx, dec, persisted.ModuleResultID, "mod tree module")
			if err != nil {
				return nil, err
			}
			node.Module = module
			if srv, ok := serverByModuleID[persisted.ModuleResultID]; ok {
				node.DagqlServer = srv
			} else {
				srv, err := dagqlServerForModule(ctx, module)
				if err != nil {
					return nil, fmt.Errorf("decode persisted mod tree server for module %d: %w", persisted.ModuleResultID, err)
				}
				serverByModuleID[persisted.ModuleResultID] = srv
				node.DagqlServer = srv
			}
		}
		if persisted.OriginalModuleResultID != 0 {
			originalModule, err := loadPersistedObjectResultByResultID[*Module](ctx, dec, persisted.OriginalModuleResultID, "mod tree original module")
			if err != nil {
				return nil, err
			}
			node.OriginalModule = originalModule
		}
		if persisted.TypeResultID != 0 {
			typeDef, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dec, persisted.TypeResultID, "mod tree typedef")
			if err != nil {
				return nil, err
			}
			node.Type = typeDef
		}
		nodes[persisted.ID] = node
	}

	for _, persisted := range tree.Nodes {
		if persisted.ParentID == 0 {
			continue
		}
		parent, ok := nodes[persisted.ParentID]
		if !ok {
			return nil, fmt.Errorf("decode persisted mod tree node %d: unknown parent %d", persisted.ID, persisted.ParentID)
		}
		nodes[persisted.ID].Parent = parent
	}

	return nodes, nil
}

func attachModTreeNodeDependencyResults(
	node *ModTreeNode,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	return attachModTreeNodeDependencyResultsWithSeen(node, attach, map[*ModTreeNode]struct{}{})
}

func attachModTreeNodeDependencyResultsWithSeen(
	node *ModTreeNode,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
	seen map[*ModTreeNode]struct{},
) ([]dagql.AnyResult, error) {
	if node == nil {
		return nil, nil
	}
	if _, ok := seen[node]; ok {
		return nil, nil
	}
	seen[node] = struct{}{}

	var owned []dagql.AnyResult
	if node.Parent != nil {
		parentDeps, err := attachModTreeNodeDependencyResultsWithSeen(node.Parent, attach, seen)
		if err != nil {
			return nil, err
		}
		owned = append(owned, parentDeps...)
	}

	if node.RootValue != nil {
		value, err := attach(node.RootValue)
		if err != nil {
			return nil, err
		}
		node.RootValue = value.(dagql.AnyObjectResult)
		owned = append(owned, value)
	}
	if node.Module.Self() != nil {
		attached, err := attach(node.Module)
		if err != nil {
			return nil, fmt.Errorf("attach mod tree module: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Module])
		if !ok {
			return nil, fmt.Errorf("attach mod tree module: unexpected result %T", attached)
		}
		node.Module = typed
		owned = append(owned, typed)
	}
	if node.OriginalModule.Self() != nil {
		attached, err := attach(node.OriginalModule)
		if err != nil {
			return nil, fmt.Errorf("attach mod tree original module: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Module])
		if !ok {
			return nil, fmt.Errorf("attach mod tree original module: unexpected result %T", attached)
		}
		node.OriginalModule = typed
		owned = append(owned, typed)
	}
	if node.Type.Self() != nil {
		attached, err := attach(node.Type)
		if err != nil {
			return nil, fmt.Errorf("attach mod tree typedef: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach mod tree typedef: unexpected result %T", attached)
		}
		node.Type = typed
		owned = append(owned, typed)
	}

	return owned, nil
}

func isArtifactDirective(name string) bool {
	return name == "check" || name == "generate" || name == "up" || name == "agent"
}

// NewArtifactTree starts discovery at an engine object, such as an installed SDK.
func NewArtifactTree(ctx context.Context, value dagql.AnyObjectResult) (*ModTreeNode, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, err
	}
	types := map[string]dagql.ObjectResult[*TypeDef]{}
	for _, dep := range deps.Mods() {
		defs, err := dep.TypeDefs(ctx, srv)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			if def.Self().AsObject.Valid {
				types[def.Self().ToType().Name()] = def
			}
		}
	}
	return &ModTreeNode{RootValue: value, Type: types[value.Type().Name()], types: types, DagqlServer: srv}, nil
}

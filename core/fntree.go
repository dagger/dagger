package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
	"github.com/iancoleman/strcase"
)

func NewFnTree(ctx context.Context, mod *Module) (*FnTreeNode, error) {
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil, fmt.Errorf("%q: no main object", mod.Name())
	}
	srv, err := dagqlServerForModule(ctx, mod)
	if err != nil {
		return nil, err
	}
	return &FnTreeNode{
		DagqlServer: srv,
		DagqlRoot:   mod,
		Type: &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(mainObj),
		},
		Description: mod.Description,
	}, nil
}

// Initialize a standalone dagql server for querying the given module
func dagqlServerForModule(ctx context.Context, mod *Module) (*dagql.Server, error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache, err := q.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("%q: get dagql cache: %w", mod.Name(), err)
	}
	srv := dagql.NewServer(q, cache)
	srv.Around(AroundFunc)
	// Install default "dependencies" (ie the core)
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("%q: load core schema: %w", mod.Name(), err)
	}
	// Install dependencies
	for _, defaultDep := range defaultDeps.Mods {
		if err := defaultDep.Install(ctx, srv); err != nil {
			return nil, fmt.Errorf("%q: serve core schema: %w", mod.Name(), err)
		}
	}
	// Install toolchains
	for _, tcMod := range mod.ToolchainModules {
		if err := tcMod.Install(ctx, srv); err != nil {
			return nil, fmt.Errorf("%q: serve toolchain module %q: %w", mod.Name(), tcMod.Name(), err)
		}
	}
	// Install the main module
	if err := mod.Install(ctx, srv); err != nil {
		return nil, fmt.Errorf("%q: serve module: %w", mod.Name(), err)
	}
	return srv, nil
}

type FnTreeNode struct {
	Parent      *FnTreeNode
	Path        []string
	Description string
	DagqlServer *dagql.Server
	DagqlRoot   *Module
	DagqlPath   []dagql.Selector
	Type        *TypeDef
	IsCheck     bool
}

// The address of the dagger module that is the root of the tree
// If the node is a "file", the root address is the URL of the filesystem root
func (node *FnTreeNode) RootAddress() string {
	mod := node.DagqlRoot
	if mod == nil {
		return ""
	}
	modSrc := mod.Source.Value.Self()
	if modSrc == nil {
		return ""
	}
	return modSrc.AsString()
}

func (node *FnTreeNode) Clone() *FnTreeNode {
	cp := *node
	cp.Path = slices.Clone(node.Path)
	cp.DagqlPath = slices.Clone(node.DagqlPath)
	return &cp
}

func (node *FnTreeNode) DagqlValue(ctx context.Context, dest any) error {
	// We can't direct-select the dagql path, because Select() doesn't support traversing
	// lists
	// FIXME: as an optimization, one-shot when possible?
	srv := node.DagqlServer
	if node.Parent == nil {
		return srv.Select(ctx, srv.Root(), dest)
	}
	if parentObjType := node.Parent.ObjectType(); parentObjType != nil {
		// Parent is an object
		var parentObjValue dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentObjValue); err != nil {
			return err
		}
		return srv.Select(ctx, parentObjValue, dest, node.DagqlPath[len(node.DagqlPath)-1:]...)
	}
	if parentNamedObjListType := node.Parent.NamedObjectListType(ctx); parentNamedObjListType != nil {
		var parentListValue []dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentListValue); err != nil {
			return err
		}
		// FIXME: we rely on list indices being stable...
		index := node.DagqlPath[len(node.DagqlPath)-1].Nth
		return srv.Select(ctx, parentListValue[index], dest)
	}
	return fmt.Errorf("%q: no known way to get dagql value", node.Name())
}

func (node *FnTreeNode) Get(ctx context.Context, path []string) (*FnTreeNode, error) {
	target := node
	for _, segment := range path {
		if target == nil {
			return nil, fmt.Errorf("node not found: %s", segment)
		}
		var err error
		target, err = target.Child(ctx, segment)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

func debugTrace(ctx context.Context, msg string, args ...any) {
	_ = parallel.
		New().
		WithContextualTracer(true).
		WithJob(fmt.Sprintf(msg, args...), nil).
		Run(ctx)
}

// Walk the tree and return all check nodes, with include and exclude filters applied.
func (node *FnTreeNode) RollupChecks(ctx context.Context, include []string, exclude []string) ([]*FnTreeNode, error) {
	var checks []*FnTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *FnTreeNode) error {
		if !n.IsCheck {
			return nil
		}
		if len(include) > 0 {
			if match, err := n.Match(include); err != nil {
				return err
			} else if !match {
				return nil
			}
		}
		if len(exclude) > 0 {
			if match, err := n.Match(exclude); err != nil {
				return err
			} else if match {
				return nil
			}
		}
		checks = append(checks, n)
		return nil
	})
	return checks, err
}

func (node *FnTreeNode) Match(include []string) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	cliName := func() string {
		path := node.Path
		for i := range path {
			path[i] = strcase.ToKebab(path[i])
		}
		return strings.Join(path, "/")
	}
	gqlName := func() string {
		path := node.Path
		for i := range path {
			path[i] = gqlFieldName(path[i])
		}
		return strings.Join(path, "/")
	}
	for _, name := range []string{cliName(), gqlName()} {
		for _, pattern := range include {
			if match, err := fnPathContains(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			if match, err := fnPathGlob(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			pattern = strings.ReplaceAll(pattern, ":", "/")
			name = strings.ReplaceAll(name, ":", "/")
			matched, err := doublestar.PathMatch(pattern, name)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

// Match a function-path against a glob pattern
func fnPathGlob(pattern, target string) (bool, error) {
	// FIXME: match against both gqlFieldCase and cliCase
	slashPattern := strings.ReplaceAll(pattern, ":", "/")
	slashTarget := strings.ReplaceAll(target, ":", "/")
	return doublestar.PathMatch(slashPattern, slashTarget)
}

// Check if a function-path contains another
func fnPathContains(base, target string) (bool, error) {
	// FIXME: match against both gqlFieldCase and cliCase
	slashTarget := path.Clean("/" + strings.ReplaceAll(target, ":", "/"))
	slashBase := path.Clean("/" + strings.ReplaceAll(base, ":", "/"))
	rel, err := filepath.Rel(slashBase, slashTarget)
	if err != nil {
		return false, err
	}
	return !strings.HasPrefix(rel, ".."), nil
}

func (n *FnTreeNode) Name() string {
	return strings.Join(n.Path, ":")
}

type WalkFunc func(context.Context, *FnTreeNode) error

func (node *FnTreeNode) Walk(ctx context.Context, fn WalkFunc) error {
	if err := fn(ctx, node); err != nil {
		return err
	}
	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	for _, childName := range children {
		child, err := node.Child(ctx, childName)
		if err != nil {
			return err
		}
		if err := child.Walk(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}

func (node *FnTreeNode) Children(ctx context.Context) ([]string, error) {
	// 1. Is this node an object?
	if objType := node.ObjectType(); objType != nil {
		var children []string
		for _, fn := range objType.Functions {
			if functionRequiresArgs(fn) {
				continue
			}
			children = append(children, fn.Name)
		}
		for _, field := range objType.Fields {
			children = append(children, field.Name)
		}
		return children, nil
	}
	// 2. Is this node a list of named objects?
	if namedObjListType := node.NamedObjectListType(ctx); namedObjListType != nil {
		srv := node.DagqlServer
		var namedObjectValues []dagql.AnyObjectResult
		if err := srv.Select(ctx, srv.Root(), &namedObjectValues, node.DagqlPath...); err != nil {
			return nil, err
		}
		names := make([]string, len(namedObjectValues))
		for i, namedObj := range namedObjectValues {
			if err := srv.Select(ctx, namedObj, &names[i], dagql.Selector{Field: "name"}); err != nil {
				return nil, err
			}
		}
		return names, nil
	}
	return nil, nil
}

// Return the specified child node, or nil if it doesn't exist
func (node *FnTreeNode) Child(ctx context.Context, name string) (*FnTreeNode, error) {
	if objType := node.ObjectType(); objType != nil {
		fn, isFunction := objType.FunctionByName(name)
		if isFunction {
			return &FnTreeNode{
				Parent:      node,
				Path:        append(slices.Clone(node.Path), name),
				DagqlServer: node.DagqlServer,
				DagqlRoot:   node.DagqlRoot,
				DagqlPath:   append(slices.Clone(node.DagqlPath), dagql.Selector{Field: gqlFieldName(name)}),
				Type:        fn.ReturnType,
				IsCheck:     fn.IsCheck,
				Description: fn.Description,
			}, nil
		}
		field, isField := objType.FieldByName(name)
		if isField {
			return &FnTreeNode{
				Parent:      node,
				Path:        append(slices.Clone(node.Path), name),
				DagqlServer: node.DagqlServer,
				DagqlRoot:   node.DagqlRoot,
				DagqlPath:   append(slices.Clone(node.DagqlPath), dagql.Selector{Field: gqlFieldName(name)}),
				Type:        field.TypeDef,
				IsCheck:     false,
				Description: field.Description,
			}, nil
		}
		return nil, nil
	}
	if namedObjectListType := node.NamedObjectListType(ctx); namedObjectListType != nil {
		srv := node.DagqlServer
		var namedObjectValues []dagql.AnyObjectResult
		if err := srv.Select(ctx, srv.Root(), &namedObjectValues, node.DagqlPath...); err != nil {
			return nil, err
		}
		for i, namedObj := range namedObjectValues {
			var objName string
			if err := srv.Select(ctx, namedObj, &objName, dagql.Selector{Field: "name"}); err != nil {
				return nil, err
			}
			if objName == name {
				// FIXME: this assumes the list result is cached at least for the session, and
				// indices are stable across re-selections of the same list value.
				// If this is not the case (eg. if the function returning the list has caching disabled *and* doesn't produce the same list each time),
				// then indices will be wrong and bad things will happen
				// FIXME: fix the above by caching previously selected dagql values, and re-using them across calls to the node. This might be a PITA.
				return &FnTreeNode{
					Parent:      node,
					Path:        append(slices.Clone(node.Path), objName),
					DagqlServer: node.DagqlServer,
					DagqlRoot:   node.DagqlRoot,
					DagqlPath:   append(slices.Clone(node.DagqlPath), dagql.Selector{Nth: i}),
					Type: &TypeDef{
						Kind:     TypeDefKindObject,
						AsObject: dagql.NonNull(namedObjectListType),
					},
					// FIXME: support dynamic description(). For now description is blank
				}, nil
			}
		}
	}
	return nil, nil
}

func (node *FnTreeNode) NamedObjectListType(ctx context.Context) *ObjectTypeDef {
	listType := node.Type.AsList.Value
	if listType == nil {
		return nil
	}
	if listType.ElementTypeDef.Kind != TypeDefKindObject {
		return nil
	}
	objModType, ok, err := node.DagqlRoot.ModTypeFor(ctx, listType.ElementTypeDef, true)
	if err != nil {
		return nil
	}
	if !ok {
		return nil
	}
	objType := objModType.TypeDef().AsObject.Value
	if objType == nil {
		return nil
	}
	nameType, hasName := objType.FunctionOrFieldByName("name")
	if !hasName {
		return nil
	}
	if nameType.Kind != TypeDefKindString {
		return nil
	}
	return objType
}

func (node *FnTreeNode) ObjectType() *ObjectTypeDef {
	return node.Type.AsObject.Value
}

func (node *FnTreeNode) BaseName() string {
	if len(node.Path) == 0 {
		return ""
	}
	return node.Path[len(node.Path)-1]
}

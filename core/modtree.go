package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
	"github.com/iancoleman/strcase"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ModTreeNode struct {
	Parent      *ModTreeNode
	Name        string
	Description string
	DagqlServer *dagql.Server
	Module      *Module
	Type        *TypeDef
	IsCheck     bool
}

func (node *ModTreeNode) Path() ModTreePath {
	if node.Parent == nil {
		return nil
	}
	var path ModTreePath
	path = append(path, node.Parent.Path()...)
	path = append(path, node.Name)
	return path
}

func NewModTree(ctx context.Context, mod *Module) (*ModTreeNode, error) {
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil, fmt.Errorf("%q: no main object", mod.Name())
	}
	srv, err := dagqlServerForModule(ctx, mod)
	if err != nil {
		return nil, err
	}
	return &ModTreeNode{
		DagqlServer: srv,
		Module:      mod,
		Type: &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(mainObj),
		},
		Description: mod.Description,
	}, nil
}

func (node *ModTreeNode) RunCheck(ctx context.Context, include, exclude []string) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, node.PathString(),
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.String(telemetry.CheckNameAttr, node.PathString()),
		),
	)
	defer func() {
		span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, rerr == nil))
		telemetry.EndWithCause(span, &rerr)
	}()

	if node.IsCheck {
		return node.runLeafCheck(ctx)
	}
	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New().WithTracing(false)
	for _, child := range children {
		if len(include) > 0 {
			if match, err := node.Match(ctx, include); err != nil {
				return err
			} else if !match {
				continue
			}
		}
		if len(exclude) > 0 {
			if match, err := node.Match(ctx, exclude); err != nil {
				return err
			} else if match {
				continue
			}
		}
		jobs = jobs.WithJob(child.Name, func(ctx context.Context) error {
			return child.RunCheck(ctx, nil, nil)
		})
	}
	return jobs.Run(ctx) // don't suppress the error. That can be handled by the top-level caller if necessary
}

func (node *ModTreeNode) runLeafCheck(ctx context.Context) error {
	return func() error {
		var status dagql.AnyResult
		if err := node.DagqlValue(ctx, &status); err != nil {
			return err
		}
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status); ok {
			// If the check returns a syncable type, sync it
			srv := node.DagqlServer
			if syncField, has := obj.ObjectType().FieldSpec("sync", srv.View); has {
				if !syncField.Args.HasRequired(srv.View) {
					if err := srv.Select(
						dagql.WithNonInternalTelemetry(ctx),
						obj,
						&status,
						dagql.Selector{Field: "sync"},
					); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}()
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

// The address of the dagger module that is the root of the tree
// If the node is a "file", the root address is the URL of the filesystem root
func (node *ModTreeNode) RootAddress() string {
	mod := node.Module
	if mod == nil {
		return ""
	}
	modSrc := mod.Source.Value.Self()
	if modSrc == nil {
		return ""
	}
	return modSrc.AsString()
}

func (node *ModTreeNode) Clone() *ModTreeNode {
	cp := *node
	return &cp
}

func (node *ModTreeNode) DagqlValue(ctx context.Context, dest any) error {
	// We can't direct-select the dagql path, because Select() doesn't support traversing
	// lists
	// FIXME: as an optimization, one-shot when possible?
	srv := node.DagqlServer
	// 1. Are we the root?
	if node.Parent == nil {
		return srv.Select(ctx, srv.Root(), dest)
	}
	// 2. Is parent an object?
	if parentObjType := node.Parent.ObjectType(); parentObjType != nil {
		var parentObjValue dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentObjValue); err != nil {
			return err
		}
		return srv.Select(dagql.WithNonInternalTelemetry(ctx), parentObjValue, dest, dagql.Selector{Field: node.Name})
	}
	// 3. Is parent a list of named objects?
	if parentNamedObjListType := node.Parent.NamedObjectListType(ctx); parentNamedObjListType != nil {
		var siblingValues []dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &siblingValues); err != nil {
			return err
		}
		for _, sibling := range siblingValues {
			var name string
			if err := srv.Select(ctx, sibling, &name, dagql.Selector{Field: "name"}); err != nil {
				return err
			}
			if name == node.Name {
				return srv.Select(dagql.WithNonInternalTelemetry(ctx), sibling, dest)
			}
		}
	}
	return fmt.Errorf("%q: get value: parent is neither an object nor a list of objects", node.PathString())
}

func debugTrace(ctx context.Context, msg string, args ...any) {
	_ = parallel.
		New().
		WithContextualTracer(true).
		WithJob(fmt.Sprintf(msg, args...), nil).
		Run(ctx)
}

// Walk the tree and return all check nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupChecks(ctx context.Context, include []string, exclude []string, all bool) ([]*ModTreeNode, error) {
	var checks []*ModTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		// FIXME: prune the search tree more aggressively, for efficiency
		// BUT be careful to not break matching!
		if n.IsCheck {
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
			checks = append(checks, n)
			return false, nil // checks are always leaves - no point in trying to walk
		}
		if !all {
			if namedObjListType := n.NamedObjectListType(ctx); namedObjListType != nil {
				debugTrace(ctx, "%q: static-walking dynamic object list", n.PathString())
				// If all=false, don't walk dynamic object lists (they're more expensive)
				// Instead, walk (statically) their element object, to determine if it contains checks
				staticNode := &ModTreeNode{
					Parent:      n,
					Name:        "...",
					DagqlServer: node.DagqlServer,
					Module:      node.Module,
					Type: &TypeDef{
						Kind:     TypeDefKindObject,
						AsObject: dagql.NonNull(namedObjListType),
					},
					Description: namedObjListType.Description,
				}
				staticChecks, err := staticNode.RollupChecks(ctx, include, exclude, false)
				if err != nil {
					return false, err
				}
				debugTrace(ctx, "%q: static-walk: %d checks per object", n.PathString(), len(staticChecks))
				if len(staticChecks) > 0 {
					debugTrace(ctx, "%q: static-walk: %d checks per object: success! adding to check list", n.PathString(), len(staticChecks))
					checks = append(checks, n)
				}
				return false, nil
			}
		}
		return true, nil
	})
	return checks, err
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

func (p ModTreePath) ApiCase() []string {
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
	targetParent := ModTreePath(target[:len(p)])
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
	slashPattern := strings.ReplaceAll(pattern, ":", "/")
	for _, pathVariant := range [][]string{p.ApiCase(), p.CliCase()} {
		slashPath := strings.Join(pathVariant, "/")
		if match, err := doublestar.PathMatch(slashPattern, slashPath); err != nil {
			return false, err
		} else if match {
			debugTrace(ctx, "%q.Glob(%q) -> MATCH", slashPath, slashPattern)
			return true, nil
		}
		debugTrace(ctx, "%q.Glob(%q) -> no match", slashPath, slashPattern)
	}
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
	return strings.Join(node.Path(), ":")
}

type WalkFunc func(context.Context, *ModTreeNode) (bool, error)

func (node *ModTreeNode) Walk(ctx context.Context, fn WalkFunc) error {
	enter, err := fn(ctx, node)
	if err != nil {
		return err
	}
	if !enter {
		return nil
	}
	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := child.Walk(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}

func (node *ModTreeNode) Children(ctx context.Context) ([]*ModTreeNode, error) {
	var children []*ModTreeNode
	if objType := node.ObjectType(); objType != nil {
		// 1. Is this node an object?
		for _, fn := range objType.Functions {
			if functionRequiresArgs(fn) {
				continue
			}
			children = append(children, &ModTreeNode{
				Parent:      node,
				Name:        fn.Name,
				DagqlServer: node.DagqlServer,
				Module:      node.Module,
				Type:        fn.ReturnType,
				IsCheck:     fn.IsCheck,
				Description: fn.Description,
			})
		}
		for _, field := range objType.Fields {
			children = append(children, &ModTreeNode{
				Parent:      node,
				Name:        field.Name,
				DagqlServer: node.DagqlServer,
				Module:      node.Module,
				Type:        field.TypeDef,
				IsCheck:     false,
				Description: field.Description,
			})
		}
	} else if namedObjListType := node.NamedObjectListType(ctx); namedObjListType != nil {
		// 2. Is this node a list of named objects?

		srv := node.DagqlServer
		var namedObjects []dagql.AnyObjectResult
		if err := node.DagqlValue(ctx, &namedObjects); err != nil {
			return nil, err
		}
		for _, namedObj := range namedObjects {
			var name string
			if err := srv.Select(ctx, namedObj, &name, dagql.Selector{Field: "name"}); err != nil {
				return nil, err
			}
			children = append(children, &ModTreeNode{
				Parent:      node,
				Name:        name,
				DagqlServer: node.DagqlServer,
				Module:      node.Module,
				Type: &TypeDef{
					Kind:     TypeDefKindObject,
					AsObject: dagql.NonNull(namedObjListType),
				},
				Description: "", // FIXME: how to support dynamic description?
			})
		}
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

func (node *ModTreeNode) NamedObjectListType(ctx context.Context) *ObjectTypeDef {
	listType := node.Type.AsList.Value
	if listType == nil {
		return nil
	}
	if listType.ElementTypeDef.Kind != TypeDefKindObject {
		return nil
	}
	objModType, ok, err := node.Module.ModTypeFor(ctx, listType.ElementTypeDef, true)
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

func (node *ModTreeNode) ObjectType() *ObjectTypeDef {
	return node.Type.AsObject.Value
}

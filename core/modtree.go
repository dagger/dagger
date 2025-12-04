package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger/telemetry"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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

func (n *ModTreeNode) Path() []string {
	if n.Parent == nil {
		return nil
	}
	var path []string
	path = append(path, n.Parent.Path()...)
	path = append(path, n.Name)
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

func (n *ModTreeNode) RunCheck(ctx context.Context, include, exclude []string) error {
	if n.IsCheck {
		return n.runLeafCheck(ctx)
	}
	children, err := n.Children(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New().WithContextualTracer(true)
	for _, child := range children {
		if len(include) > 0 {
			if match, err := n.Match(include); err != nil {
				return err
			} else if !match {
				continue
			}
		}
		if len(exclude) > 0 {
			if match, err := n.Match(exclude); err != nil {
				return err
			} else if match {
				continue
			}
		}
		jobs = jobs.WithJob(child.Name, func(ctx context.Context) error {
			return child.RunCheck(ctx, nil, nil)
		})
	}
	return jobs.Run(ctx)
}

func (n *ModTreeNode) runLeafCheck(ctx context.Context) error {
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	var span trace.Span
	if clientMD.CloudScaleOutEngineID != "" { // don't dupe telemetry if the client is an engine scaling out to us
		ctx, span = Tracer(ctx).Start(ctx, n.PathString(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.CheckNameAttr, n.PathString()),
			),
		)
	}
	var checkErr error
	defer func() {
		if span != nil {
			// Set the passed attribute on the span for telemetry
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, checkErr == nil))
			telemetry.EndWithCause(span, &checkErr)
		}
	}()
	checkErr = func() error {
		var status dagql.AnyResult
		if err := n.DagqlValue(ctx, &status); err != nil {
			return err
		}
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status); ok {
			// If the check returns a syncable type, sync it
			srv := n.DagqlServer
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
	// We can't distinguish legitimate errors from failed checks, so we just discard.
	// Bubbling them up to here makes telemetry more useful (no green when a check failed)
	return nil
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
		return srv.Select(ctx, parentObjValue, dest, dagql.Selector{Field: node.Name})
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
				return srv.Select(ctx, sibling, dest)
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
func (node *ModTreeNode) RollupChecks(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	var checks []*ModTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		if len(include) > 0 {
			if match, err := n.Match(include); err != nil {
				return false, err
			} else if !match {
				return false, nil
			}
		}
		if len(exclude) > 0 {
			if match, err := n.Match(exclude); err != nil {
				return false, err
			} else if match {
				return false, nil
			}
		}
		if n.IsCheck {
			checks = append(checks, n)
			return false, nil // checks are always leaves - no point in trying to walk
		}
		return true, nil
	})
	return checks, err
}

func (node *ModTreeNode) Match(include []string) (bool, error) {
	if node.Parent == nil {
		// The root node matches everything
		return true, nil
	}
	if len(include) == 0 {
		return true, nil
	}
	cliName := func() string {
		path := node.Path()
		for i := range path {
			path[i] = strcase.ToKebab(path[i])
		}
		return strings.Join(path, "/")
	}
	gqlName := func() string {
		path := node.Path()
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

func (n *ModTreeNode) PathString() string {
	return strings.Join(n.Path(), ":")
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

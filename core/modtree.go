package core

import (
	"context"
	"errors"
	"fmt"
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
	IsGenerator bool
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

func (node *ModTreeNode) Run(
	ctx context.Context,
	// should return true if that's a leaf we need to execute
	// for instance if we want to run a check, return true if IsCheck is true
	isLeaf func(*ModTreeNode) bool,
	// run the right function on the leaf. For instance run as a check, or run as a generator
	// clientMetadata is used to know if we want to try to scale out
	// this callback is used to keep this function generic and allow to return different values
	runLeaf func(context.Context, *ModTreeNode, *engine.ClientMetadata) error,
	// called inside a defer. Used to set properties to traces, for instance the CheckPassed attribute for checks.
	// if there's an error, it's already added to the telemetry, no need to do it here
	onDefer func(trace.Span, error),
	// telemetry attribute to set with the name of the node
	telemetryNameAttr string,
	include, exclude []string,
) (rerr error) {
	clientMD, _ := engine.ClientMetadataFromContext(ctx)

	// Only create telemetry span if we're NOT a scale-out target (to avoid duplication)
	var span trace.Span
	if clientMD == nil || clientMD.CloudScaleOutEngineID == "" {
		ctx, span = Tracer(ctx).Start(ctx, node.PathString(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetryNameAttr, node.PathString()),
			),
		)
		defer func() {
			onDefer(span, rerr)
			telemetry.EndWithCause(span, &rerr)
		}()
	}

	if isLeaf(node) {
		return runLeaf(ctx, node, clientMD)
	}

	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New().WithTracing(false)
	for _, child := range children {
		// FIXME: filtering uses `node` instead of `child` - should match against the child being iterated
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
			return child.Run(ctx, isLeaf, runLeaf, onDefer, telemetryNameAttr, nil, nil)
		})
	}
	return jobs.Run(ctx) // don't suppress the error. That can be handled by the top-level caller if necessary
}

func (node *ModTreeNode) RunCheck(ctx context.Context, include, exclude []string) error {
	return node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsCheck },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) error {
			// Try scale-out if enabled (will be false for scaled-out sessions)
			if clientMD != nil && clientMD.EnableCloudScaleOut {
				if ok, err := node.tryRunCheckScaleOut(ctx); ok {
					return err
				}
			}
			return n.runLeafCheckLocally(ctx)
		},
		func(span trace.Span, err error) {
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, err == nil))
		},
		telemetry.CheckNameAttr,
		include, exclude)
}

func (node *ModTreeNode) RunGenerator(ctx context.Context, include, exclude []string) (*Changeset, error) {
	var cs *Changeset
	err := node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsGenerator },
		func(ctx context.Context, n *ModTreeNode, _ *engine.ClientMetadata) error {
			changes, err := n.runLeafGeneratorLocally(ctx)
			cs = changes
			return err
		},
		func(_ trace.Span, _ error) {},
		telemetry.GeneratorNameAttr,
		include, exclude)
	return cs, err
}

func (node *ModTreeNode) runLeafCheckLocally(ctx context.Context) error {
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
}

func (node *ModTreeNode) runLeafGeneratorLocally(ctx context.Context) (*Changeset, error) {
	var changes dagql.ObjectResult[*Changeset]
	if err := node.DagqlValue(ctx, &changes); err != nil {
		return nil, err
	}
	return changes.Self(), nil
}

func (node *ModTreeNode) tryRunCheckScaleOut(ctx context.Context) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}

	cloudClient, useCloud, err := q.CloudEngineClient(ctx,
		node.RootAddress(),
		node.PathString(),
		nil,
	)
	if err != nil {
		return true, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloud {
		return false, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudClient.Close())
	}()

	query := cloudClient.Dagger().QueryBuilder()

	// Load the module based on its source kind
	modSrc := node.Module.Source.Value.Self()
	switch modSrc.Kind {
	case ModuleSourceKindLocal:
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				modSrc.Local.ContextDirectoryPath,
				modSrc.SourceRootSubpath,
			))
	case ModuleSourceKindGit:
		query = query.Select("moduleSource").
			Arg("refString", modSrc.AsString()).
			Arg("refPin", modSrc.Git.Commit).
			Arg("requireKind", modSrc.Kind)
	case ModuleSourceKindDir:
		dirIDEnc, err := modSrc.DirSrc.OriginalContextDir.ID().Encode()
		if err != nil {
			return true, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", modSrc.DirSrc.OriginalSourceRootSubpath)
	}

	query = query.Select("asModule")
	query = query.Select("check").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.SelectMultiple("completed", "passed")

	var res struct{ Completed, Passed bool }
	return true, query.Bind(&res).Execute(ctx)
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
	if mod.Toolchains != nil {
		for _, entry := range mod.Toolchains.Entries() {
			if err := entry.Module.Install(ctx, srv); err != nil {
				return nil, fmt.Errorf("%q: serve toolchain module %q: %w", mod.Name(), entry.Module.Name(), err)
			}
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
	// 1. Are we the root? Select the module's main object from Query root.
	if node.Parent == nil {
		return srv.Select(ctx, srv.Root(), dest, dagql.Selector{Field: gqlFieldName(node.Module.Name())})
	}
	// 2. Is parent an object?
	if parentObjType := node.Parent.ObjectType(); parentObjType != nil {
		var parentObjValue dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentObjValue); err != nil {
			return err
		}
		return srv.Select(dagql.WithNonInternalTelemetry(ctx), parentObjValue, dest, dagql.Selector{Field: node.Name})
	}
	return fmt.Errorf("%q: get value: parent is not an object", node.PathString())
}

func debugTrace(ctx context.Context, msg string, args ...any) {
	_ = parallel.
		New().
		WithContextualTracer(true).
		WithJob(fmt.Sprintf(msg, args...), nil).
		Run(ctx)
}

// Walk the tree and return all matching nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupNodes(ctx context.Context, matches func(*ModTreeNode) bool, include []string, exclude []string) ([]*ModTreeNode, error) {
	var res []*ModTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		// FIXME: prune the search tree more aggressively, for efficiency
		// BUT be careful to not break matching!
		if matches(n) {
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
			return false, nil // always looking for leaves - no point in trying to walk
		}
		return true, nil
	})
	return res, err
}

// Walk the tree and return all check nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupChecks(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	return node.RollupNodes(ctx, func(n *ModTreeNode) bool {
		return n.IsCheck
	}, include, exclude)
}

// Walk the tree and return all generator nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupGenerator(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	return node.RollupNodes(ctx, func(n *ModTreeNode) bool {
		return n.IsGenerator
	}, include, exclude)
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
				IsGenerator: fn.IsGenerator,
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
				IsGenerator: false,
				Description: field.Description,
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

func (node *ModTreeNode) ObjectType() *ObjectTypeDef {
	return node.Type.AsObject.Value
}

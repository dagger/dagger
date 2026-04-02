package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger/querybuilder"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"

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
	// This module is the same across all ModTreeNode, this is the root module.
	Module *Module
	// This original module is the one in which the node has been defined.
	OriginalModule *Module
	Type           *TypeDef
	IsCheck        bool
	IsGenerator    bool
	resolveValues  func(context.Context) ([]dagql.AnyResult, error)
	filterSet      CollectionFilterSet
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

func NewModTree(ctx context.Context, mod *Module, filterSet CollectionFilterSet) (*ModTreeNode, error) {
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil, fmt.Errorf("%q: no main object", mod.Name())
	}
	srv, err := dagqlServerForModule(ctx, mod)
	if err != nil {
		return nil, err
	}
	return &ModTreeNode{
		DagqlServer:    srv,
		Module:         mod,
		OriginalModule: mod,
		filterSet:      filterSet.Clone(),
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
	include, exclude []string,
) (rerr error) {
	clientMD, _ := engine.ClientMetadataFromContext(ctx)

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
			return child.Run(ctx, isLeaf, runLeaf, nil, nil)
		})
	}
	return jobs.Run(ctx) // don't suppress the error. That can be handled by the top-level caller if necessary
}

func (node *ModTreeNode) RunCheck(ctx context.Context, include, exclude []string) error {
	return node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsCheck },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			// Try scale-out if enabled (will be false for scaled-out sessions)
			if clientMD != nil && clientMD.EnableCloudScaleOut {
				if ok, err := node.tryRunCheckScaleOut(ctx); ok {
					return err
				}
			}
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
			return n.runCheckLocally(ctx)
		},
		include, exclude)
}

func (node *ModTreeNode) runCheckLocally(ctx context.Context) error {
	values, err := node.ResolveValues(ctx)
	if err != nil {
		return err
	}
	for _, status := range values {
		if status == nil {
			continue
		}
		if err := node.syncCheckResult(ctx, status); err != nil {
			return err
		}
	}
	return nil
}

func (node *ModTreeNode) syncCheckResult(ctx context.Context, status dagql.AnyResult) error {
	obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status)
	if !ok {
		return nil
	}
	srv := node.DagqlServer
	syncField, has := obj.ObjectType().FieldSpec("sync", srv.View)
	if !has || syncField.Args.HasRequired(srv.View) {
		return nil
	}
	return srv.Select(
		dagql.WithNonInternalTelemetry(ctx),
		obj,
		&status,
		dagql.Selector{Field: "sync"},
	)
}

func (node *ModTreeNode) tryRunCheckScaleOut(ctx context.Context) (_ bool, rerr error) {
	if node.filterSet.HasAny() {
		return false, nil
	}
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

	query, err := node.buildScaleOutModuleQuery(cloudClient.Dagger().QueryBuilder())
	if err != nil {
		return true, err
	}

	query = query.Select("check").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.Select("error")
	query = query.Select("id")

	var errID string
	if err := query.Bind(&errID).Execute(ctx); err != nil {
		return true, err
	}

	if errID != "" {
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return true, err
		}
		var idp call.ID
		if err := idp.Decode(errID); err != nil {
			return true, err
		}
		errObj, err := dagql.NewID[*Error](&idp).Load(ctx, srv)
		if err != nil {
			return true, err
		}
		return true, errObj.Self()
	}

	return true, nil
}

func (node *ModTreeNode) RunGenerator(ctx context.Context, include, exclude []string) (*Changeset, error) {
	var cs *Changeset
	err := node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsGenerator },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			// Try scale-out if enabled (will be false for scaled-out sessions)
			if clientMD != nil && clientMD.EnableCloudScaleOut {
				if ok, changes, err := node.tryRunGeneratorScaleOut(ctx); ok {
					cs = changes
					return err
				}
			}
			ctx, span := Tracer(ctx).Start(ctx, node.PathString(),
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.Bool(telemetry.UIRollUpLogsAttr, true),
					attribute.Bool(telemetry.UIRollUpSpansAttr, true),
					attribute.String(telemetry.GeneratorNameAttr, node.PathString()),
				),
			)
			defer telemetry.EndWithCause(span, &rerr)
			changes, err := n.runGeneratorLocally(ctx)
			cs = changes
			return err
		},
		include, exclude)
	return cs, err
}

func (node *ModTreeNode) runGeneratorLocally(ctx context.Context) (*Changeset, error) {
	values, err := node.ResolveValues(ctx)
	if err != nil {
		return nil, err
	}

	if len(values) == 0 {
		return NewEmptyChangeset(ctx)
	}

	changesets := make([]*Changeset, 0, len(values))
	for _, value := range values {
		changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](value)
		if !ok {
			return nil, fmt.Errorf("expected generator result to be Changeset, got %T", value)
		}
		changesets = append(changesets, changes.Self())
	}

	if len(changesets) == 1 {
		return changesets[0], nil
	}

	res, err := NewEmptyChangeset(ctx)
	if err != nil {
		return nil, err
	}
	return res.WithChangesets(ctx, changesets, FailEarlyOnConflicts)
}

func (node *ModTreeNode) tryRunGeneratorScaleOut(ctx context.Context) (_ bool, _ *Changeset, rerr error) {
	if node.filterSet.HasAny() {
		return false, nil, nil
	}
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, nil, err
	}

	cloudClient, useCloud, err := q.CloudEngineClient(ctx,
		node.RootAddress(),
		node.PathString(),
		nil,
	)
	if err != nil {
		return true, nil, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloud {
		return false, nil, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudClient.Close())
	}()

	query, err := node.buildScaleOutModuleQuery(cloudClient.Dagger().QueryBuilder())
	if err != nil {
		return true, nil, err
	}

	query = query.Select("generator").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.Select("changes")

	var cs Changeset
	if err := query.Bind(&cs).Execute(ctx); err != nil {
		return true, nil, err
	}

	// ResolveRefs to load Directory objects from IDs
	if err := cs.ResolveRefs(ctx, node.DagqlServer); err != nil {
		return true, nil, fmt.Errorf("resolve changeset refs: %w", err)
	}

	return true, &cs, nil
}

// buildScaleOutModuleQuery builds a query to load a module for scale-out execution.
// It handles all module source kinds (Local, Git, Dir) and returns a query
// positioned at the "asModule" selection, ready for check/generator-specific queries.
func (node *ModTreeNode) buildScaleOutModuleQuery(query *querybuilder.Selection) (*querybuilder.Selection, error) {
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
			return nil, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", modSrc.DirSrc.OriginalSourceRootSubpath)
	}
	return query.Select("asModule"), nil
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
	for _, defaultDep := range defaultDeps.Mods() {
		if err := defaultDep.Install(ctx, srv); err != nil {
			return nil, fmt.Errorf("%q: serve core schema: %w", mod.Name(), err)
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

func (node *ModTreeNode) ResolveValues(ctx context.Context) ([]dagql.AnyResult, error) {
	if node.resolveValues != nil {
		return node.resolveValues(ctx)
	}
	var value dagql.AnyResult
	if err := node.DagqlValue(ctx, &value); err != nil {
		return nil, err
	}
	return []dagql.AnyResult{value}, nil
}

func (node *ModTreeNode) DagqlValue(ctx context.Context, dest any) error {
	// We can't direct-select the dagql path, because Select() doesn't support traversing
	// lists
	// FIXME: as an optimization, one-shot when possible?
	srv := node.DagqlServer
	// 1. Are we the root? Select the module's main object from Query root.
	// A node is also treated as root if its parent is a synthetic naming-only
	// node (e.g. injected by workspace checks reparenting, which sets
	// Parent to an empty ModTreeNode with nil Module).
	if node.Parent == nil || node.Parent.Module == nil {
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
	slices.SortStableFunc(res, func(a, b *ModTreeNode) int {
		return strings.Compare(a.PathString(), b.PathString())
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

func (node *ModTreeNode) CollectionFilterValues(ctx context.Context, typeNames []string, include []string, exclude []string) ([]*CollectionFilterValues, error) {
	orderedTypeNames := make([]string, 0, len(typeNames))
	seenTypes := make(map[string]string, len(typeNames))
	for _, typeName := range typeNames {
		key := gqlObjectName(typeName)
		if _, ok := seenTypes[key]; ok {
			continue
		}
		seenTypes[key] = typeName
		orderedTypeNames = append(orderedTypeNames, key)
	}

	valuesByType := make(map[string][]string, len(orderedTypeNames))
	seenValuesByType := make(map[string]map[string]struct{}, len(orderedTypeNames))
	for _, typeName := range orderedTypeNames {
		seenValuesByType[typeName] = map[string]struct{}{}
	}

	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		if !n.Type.AsCollection.Valid {
			return true, nil
		}
		if len(orderedTypeNames) > 0 {
			if _, ok := seenTypes[gqlObjectName(n.Type.AsObject.Value.Name)]; !ok {
				return true, nil
			}
		}
		if len(include) > 0 {
			match, err := n.Match(ctx, include)
			if err != nil {
				return false, err
			}
			if !match {
				return true, nil
			}
		}
		if len(exclude) > 0 {
			match, err := n.Match(ctx, exclude)
			if err != nil {
				return false, err
			}
			if match {
				return true, nil
			}
		}

		values, err := n.ResolveValues(ctx)
		if err != nil {
			return false, err
		}

		typeKey := gqlObjectName(n.Type.AsObject.Value.Name)
		if _, ok := seenValuesByType[typeKey]; !ok {
			seenValuesByType[typeKey] = map[string]struct{}{}
			if _, ok := seenTypes[typeKey]; !ok {
				seenTypes[typeKey] = n.Type.AsObject.Value.Name
				orderedTypeNames = append(orderedTypeNames, typeKey)
			}
		}
		for _, value := range values {
			collectionObj, ok := dagql.UnwrapAs[*ModuleObject](value)
			if !ok {
				return false, fmt.Errorf("expected collection node %q to resolve to ModuleObject, got %T", n.PathString(), value)
			}
			keys, err := n.filteredCollectionKeys(collectionObj, true)
			if err != nil {
				return false, err
			}
			for _, key := range keys {
				keyValue, err := normalizeCollectionFilterValue(collectionObj.Collection.KeyType, key)
				if err != nil {
					return false, err
				}
				if _, ok := seenValuesByType[typeKey][keyValue]; ok {
					continue
				}
				seenValuesByType[typeKey][keyValue] = struct{}{}
				valuesByType[typeKey] = append(valuesByType[typeKey], keyValue)
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]*CollectionFilterValues, 0, len(orderedTypeNames))
	for _, typeKey := range orderedTypeNames {
		result = append(result, &CollectionFilterValues{
			TypeName: seenTypes[typeKey],
			Values:   valuesByType[typeKey],
		})
	}
	return result, nil
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
	objType := node.ObjectType()
	if objType == nil {
		return nil, nil
	}
	if node.Type.AsCollection.Valid {
		return node.collectionChildren(ctx)
	}
	return node.objectChildren(ctx, objType)
}

func (node *ModTreeNode) objectChildren(ctx context.Context, objType *ObjectTypeDef) ([]*ModTreeNode, error) {
	children := make([]*ModTreeNode, 0, len(objType.Functions)+len(objType.Fields))
	nodeType := objType.Name

	for _, fn := range objType.Functions {
		if functionRequiresArgs(fn) {
			continue
		}
		childType, description, isLeaf := node.memberObjectType(node.OriginalModule, nodeType, fn.ReturnType, fn.Description)
		children = append(children, node.newChildNode(
			fn.Name,
			description,
			childType,
			fn.IsCheck && isLeaf,
			fn.IsGenerator && isLeaf,
			node.OriginalModule,
			node.memberResolver(fn.Name),
		))
	}

	for _, field := range objType.Fields {
		childType, description, _ := node.memberObjectType(node.OriginalModule, nodeType, field.TypeDef, field.Description)
		children = append(children, node.newChildNode(
			field.Name,
			description,
			childType,
			false,
			false,
			node.OriginalModule,
			node.memberResolver(field.Name),
		))
	}

	return children, nil
}

func (node *ModTreeNode) collectionChildren(ctx context.Context) ([]*ModTreeNode, error) {
	rawCollectionType := &TypeDef{
		Kind:         TypeDefKindObject,
		AsObject:     dagql.NonNull(node.ObjectType()),
		AsCollection: dagql.NonNull(node.Type.AsCollection.Value),
	}
	collection := node.Type.AsCollection.Value
	childrenByName := map[string]*ModTreeNode{}

	valueObj := collection.ValueType.AsObject.Value
	if fullValueObj, ok := node.OriginalModule.ObjectByName(valueObj.Name); ok {
		valueObj = fullValueObj
	}
	for _, fn := range valueObj.Functions {
		if functionRequiresArgs(fn) {
			continue
		}
		childType, description, isLeaf := node.memberObjectType(node.OriginalModule, valueObj.Name, fn.ReturnType, fn.Description)
		childrenByName[fn.Name] = node.newChildNode(
			fn.Name,
			description,
			childType,
			fn.IsCheck && isLeaf,
			fn.IsGenerator && isLeaf,
			node.OriginalModule,
			node.collectionItemResolver(ctx, fn.Name),
		)
	}
	for _, field := range valueObj.Fields {
		childType, description, _ := node.memberObjectType(node.OriginalModule, valueObj.Name, field.TypeDef, field.Description)
		childrenByName[field.Name] = node.newChildNode(
			field.Name,
			description,
			childType,
			false,
			false,
			node.OriginalModule,
			node.collectionItemResolver(ctx, field.Name),
		)
	}

	projector := newCollectionProjector(node.Module)
	if batchTypeDef := projector.projectCollectionBatchTypeDef(rawCollectionType); batchTypeDef != nil {
		for _, fn := range batchTypeDef.AsObject.Value.Functions {
			if functionRequiresArgs(fn) {
				continue
			}
			childType, description, isLeaf := node.memberObjectType(node.OriginalModule, batchTypeDef.AsObject.Value.Name, fn.ReturnType, fn.Description)
			childrenByName[fn.Name] = node.newChildNode(
				fn.Name,
				description,
				childType,
				fn.IsCheck && isLeaf,
				fn.IsGenerator && isLeaf,
				node.OriginalModule,
				node.collectionBatchResolver(fn.Name),
			)
		}
	}

	children := make([]*ModTreeNode, 0, len(childrenByName))
	for _, child := range childrenByName {
		children = append(children, child)
	}
	slices.SortFunc(children, func(a, b *ModTreeNode) int {
		return strings.Compare(a.Name, b.Name)
	})
	return children, nil
}

func (node *ModTreeNode) newChildNode(
	name string,
	description string,
	typeDef *TypeDef,
	isCheck bool,
	isGenerator bool,
	originalModule *Module,
	resolve func(context.Context) ([]dagql.AnyResult, error),
) *ModTreeNode {
	return &ModTreeNode{
		Parent:         node,
		Name:           name,
		Description:    description,
		DagqlServer:    node.DagqlServer,
		Module:         node.Module,
		OriginalModule: originalModule,
		Type:           typeDef,
		IsCheck:        isCheck,
		IsGenerator:    isGenerator,
		resolveValues:  resolve,
		filterSet:      node.filterSet,
	}
}

func (node *ModTreeNode) memberObjectType(originalModule *Module, nodeType string, returnType *TypeDef, description string) (*TypeDef, string, bool) {
	if returnType.AsObject.Valid && returnType.ToType().Name() != nodeType {
		if subObj, ok := originalModule.ObjectByName(returnType.ToType().Name()); ok {
			return &TypeDef{Kind: TypeDefKindObject, AsObject: dagql.NonNull(subObj)}, subObj.Description, false
		}
	}
	return returnType, description, true
}

func (node *ModTreeNode) memberResolver(name string) func(context.Context) ([]dagql.AnyResult, error) {
	return func(ctx context.Context) ([]dagql.AnyResult, error) {
		parentValues, err := node.ResolveValues(ctx)
		if err != nil {
			return nil, err
		}
		return node.selectMemberValues(ctx, parentValues, name)
	}
}

func (node *ModTreeNode) collectionBatchResolver(name string) func(context.Context) ([]dagql.AnyResult, error) {
	return func(ctx context.Context) ([]dagql.AnyResult, error) {
		collectionValues, err := node.ResolveValues(ctx)
		if err != nil {
			return nil, err
		}
		batchValues, err := node.selectMemberValues(ctx, collectionValues, collectionBatchFieldName)
		if err != nil {
			return nil, err
		}
		return node.selectMemberValues(ctx, batchValues, name)
	}
}

func (node *ModTreeNode) collectionItemResolver(ctx context.Context, name string) func(context.Context) ([]dagql.AnyResult, error) {
	getFn, ok := node.ObjectType().FunctionByName(node.Type.AsCollection.Value.GetFunctionName)
	if !ok {
		return func(context.Context) ([]dagql.AnyResult, error) {
			return nil, fmt.Errorf("collection get function %q not found on %q", node.Type.AsCollection.Value.GetFunctionName, node.ObjectType().Name)
		}
	}
	getModFun, err := NewModFunction(ctx, node.Module, node.ObjectType(), getFn)
	if err != nil {
		return func(context.Context) ([]dagql.AnyResult, error) {
			return nil, fmt.Errorf("failed to create collection get function %q: %w", getFn.Name, err)
		}
	}
	if err := getModFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return func(context.Context) ([]dagql.AnyResult, error) {
			return nil, fmt.Errorf("failed to merge user defaults for %q: %w", getFn.Name, err)
		}
	}

	return func(ctx context.Context) ([]dagql.AnyResult, error) {
		collectionValues, err := node.ResolveValues(ctx)
		if err != nil {
			return nil, err
		}

		results := make([]dagql.AnyResult, 0)
		for _, collectionValue := range collectionValues {
			collectionObj, ok := dagql.UnwrapAs[*ModuleObject](collectionValue)
			if !ok {
				return nil, fmt.Errorf("expected collection node %q to resolve to ModuleObject, got %T", node.PathString(), collectionValue)
			}
			keys, err := node.filteredCollectionKeys(collectionObj, false)
			if err != nil {
				return nil, err
			}
			parentResult, ok := dagql.UnwrapAs[dagql.AnyResult](collectionValue)
			if !ok {
				parentCtx := dagql.ContextWithID(ctx, collectionValue.ID())
				parentResult, err = dagql.NewResultForCurrentID(parentCtx, collectionObj)
				if err != nil {
					return nil, err
				}
			}
			for _, key := range keys {
				keyInput, err := collectionObj.collectionKeyInput(key)
				if err != nil {
					return nil, err
				}
				itemID, err := collectionObj.collectionGetID(parentResult, keyInput)
				if err != nil {
					return nil, err
				}
				itemCtx := dagql.ContextWithID(ctx, itemID)
				itemValue, err := collectionObj.callCollectionGet(itemCtx, parentResult, getModFun, node.DagqlServer, keyInput)
				if err != nil {
					return nil, err
				}
				childValue, err := node.selectMemberValue(ctx, itemValue, name)
				if err != nil {
					return nil, err
				}
				results = append(results, childValue)
			}
		}
		return results, nil
	}
}

func (node *ModTreeNode) selectMemberValues(ctx context.Context, parents []dagql.AnyResult, name string) ([]dagql.AnyResult, error) {
	values := make([]dagql.AnyResult, 0, len(parents))
	for _, parent := range parents {
		child, err := node.selectMemberValue(ctx, parent, name)
		if err != nil {
			return nil, err
		}
		values = append(values, child)
	}
	return values, nil
}

func (node *ModTreeNode) selectMemberValue(ctx context.Context, parent dagql.AnyResult, name string) (dagql.AnyResult, error) {
	parentObj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](parent)
	if !ok {
		return nil, fmt.Errorf("node %q parent is not an object: %T", node.PathString(), parent)
	}
	var child dagql.AnyResult
	if err := node.DagqlServer.Select(dagql.WithNonInternalTelemetry(ctx), parentObj, &child, dagql.Selector{Field: name}); err != nil {
		return nil, err
	}
	return child, nil
}

func (node *ModTreeNode) filteredCollectionKeys(collectionObj *ModuleObject, ignoreSelf bool) ([]any, error) {
	currentKeys, err := collectionObj.collectionKeys()
	if err != nil {
		return nil, err
	}
	filterValues, ok := node.filterSet.ValuesFor(collectionObj.TypeDef.Name)
	if !ok || ignoreSelf {
		return currentKeys, nil
	}

	selected := make(map[string]struct{}, len(filterValues))
	for _, filterValue := range filterValues {
		keyID, err := normalizeCollectionFilterValue(collectionObj.Collection.KeyType, filterValue)
		if err != nil {
			return nil, err
		}
		selected[keyID] = struct{}{}
	}
	if len(selected) == 0 {
		return []any{}, nil
	}

	filtered := make([]any, 0, len(currentKeys))
	for _, key := range currentKeys {
		keyID, err := normalizeCollectionFilterValue(collectionObj.Collection.KeyType, key)
		if err != nil {
			return nil, err
		}
		if _, ok := selected[keyID]; ok {
			filtered = append(filtered, key)
		}
	}
	return filtered, nil
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
	if node.Type == nil {
		return nil
	}
	return node.Type.AsObject.Value
}

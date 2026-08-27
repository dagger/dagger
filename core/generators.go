package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/util/parallel"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Generator represents a generator function
type Generator struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the generator complete"`
	Changes   dagql.ObjectResult[*Changeset]
}

func (*Generator) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Generator",
		NonNull:   true,
	}
}

func (g *Generator) Path() []string {
	return g.Node.Path()
}

func (g *Generator) Description() string {
	return g.Node.Description
}

func (g *Generator) Name() string {
	return g.Node.PathString()
}

func (g *Generator) OriginalModule() *Module {
	return g.Node.OriginalModule.Self()
}

func (g *Generator) Clone() *Generator {
	c := *g
	c.Node = g.Node.Clone()
	return &c
}

func (g *Generator) Run(ctx context.Context) (*Generator, error) {
	g = g.Clone()

	cs, _ := g.Node.RunGenerator(ctx, nil, nil) // ignore error as already sent to the trace if needed
	g.Completed = true
	g.Changes = cs
	return g, nil
}

func (g *Generator) RequireChangesResult(field string) (dagql.ObjectResult[*Changeset], error) {
	if !g.Completed {
		return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("generator %q must be run before querying %s", g.Name(), field)
	}
	if g.Changes.Self() == nil {
		return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("generator %q did not produce a changeset result", g.Name())
	}
	return g.Changes, nil
}

func (g *Generator) RequireChanges(field string) (*Changeset, error) {
	changes, err := g.RequireChangesResult(field)
	if err != nil {
		return nil, err
	}
	return changes.Self(), nil
}

// ModuleLoadFailure is a workspace module that best-effort `dagger generate`
// could not load and skipped.
type ModuleLoadFailure struct {
	// Name is the module's workspace name (what the skipped-module span is
	// called).
	Name string `json:"name"`
	// Dir is the module's workspace-root-relative directory, or "" when it
	// has none (a git source). Changes tells whether the run regenerated
	// files under it, which is often exactly what repairs a failed load.
	Dir string `json:"dir,omitempty"`
	// Message is the described load error (see the engine's
	// describeLoadFailure).
	Message string `json:"message"`
}

// Regenerated reports whether any of the run's changed paths (workspace-root
// relative) fall under the module's directory — i.e. whether this run
// rewrote (part of) the module, so its load failure may already be fixed.
func (f ModuleLoadFailure) Regenerated(changed []string) bool {
	if f.Dir == "" {
		return false
	}
	dir := path.Clean(filepath.ToSlash(f.Dir))
	for _, p := range changed {
		p = path.Clean(filepath.ToSlash(p))
		if dir == "." || p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

type GeneratorGroup struct {
	Node       *ModTreeNode `json:"node"`
	Generators []*Generator `json:"generators"`
	// LoadFailures carries the per-module load failures tolerated during an
	// unscoped 'dagger generate' (empty when strict, or when every module
	// loaded). Surfaced on the API (as messages) so the CLI can warn and honor
	// --require-load, and classified against the run's changes (see Changes).
	LoadFailures []ModuleLoadFailure `json:"loadFailures,omitempty"`
	// BoundWorkspace is the Workspace this group was rolled up from — the one
	// `Workspace.generators` was called on, including any overlay edits. Run
	// threads it into the context (WorkspaceToContext) so each generator leaf's
	// auto-injected Workspace! (and any currentWorkspace read) resolves against
	// it, rather than the session's frozen current workspace. Persisted by its
	// result ID (see persistedGeneratorGroupPayload.BoundWorkspaceResultID), the
	// same way each generator persists its Changeset, so a decoded group still
	// resolves against the right workspace when re-run.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

var _ dagql.PersistedObject = (*Generator)(nil)
var _ dagql.PersistedObjectDecoder = (*Generator)(nil)
var _ dagql.HasDependencyResults = (*Generator)(nil)
var _ dagql.PersistedObject = (*GeneratorGroup)(nil)
var _ dagql.PersistedObjectDecoder = (*GeneratorGroup)(nil)
var _ dagql.HasDependencyResults = (*GeneratorGroup)(nil)

type persistedGeneratorPayload struct {
	NodeID          int    `json:"nodeID,omitempty"`
	Completed       bool   `json:"completed,omitempty"`
	ChangesResultID uint64 `json:"changesResultID,omitempty"`
}

type persistedGeneratorObjectPayload struct {
	Tree      persistedModTree          `json:"tree"`
	Generator persistedGeneratorPayload `json:"generator"`
}

type persistedGeneratorGroupPayload struct {
	Tree                   persistedModTree            `json:"tree"`
	NodeID                 int                         `json:"nodeID,omitempty"`
	Generators             []persistedGeneratorPayload `json:"generators,omitempty"`
	LoadFailures           []ModuleLoadFailure         `json:"loadFailures,omitempty"`
	BoundWorkspaceResultID uint64                      `json:"boundWorkspaceResultID,omitempty"`
}

func NewGeneratorGroup(ctx context.Context, mod dagql.ObjectResult[*Module], include []string) (*GeneratorGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	generatorNodes, err := rootNode.RollupGenerator(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	generators := make([]*Generator, 0, len(generatorNodes))

	for _, generatorNode := range generatorNodes {
		generators = append(generators, &Generator{Node: generatorNode})
	}

	return &GeneratorGroup{
		Node:       rootNode,
		Generators: generators,
	}, nil
}

func (*GeneratorGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GeneratorGroup",
		NonNull:   true,
	}
}

func (gg *GeneratorGroup) List(ctx context.Context) []*Generator {
	return gg.Generators
}

// Run all the generators in the group
func (gg *GeneratorGroup) Run(ctx context.Context) (*GeneratorGroup, error) {
	gg = gg.Clone()

	// Run the generators against the workspace this group was rolled up from, so
	// overlay edits applied since the session loaded are visible to each
	// generator (its auto-injected Workspace! and any currentWorkspace read
	// resolve against BoundWorkspace, not the frozen session workspace).
	if gg.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, gg.BoundWorkspace)
	}

	jobs := parallel.New().WithContextualTracer(true)
	for _, generator := range gg.Generators {
		// Reset output fields, in case we're re-running
		generator.Completed = false
		generator.Changes = dagql.ObjectResult[*Changeset]{}
		jobs = jobs.WithJob(generator.Name(), func(ctx context.Context) error {
			cs, err := generator.Node.RunGenerator(ctx, nil, nil)
			generator.Completed = true
			generator.Changes = cs
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return gg, nil
}

func (gg *GeneratorGroup) IsEmpty(ctx context.Context) (bool, error) {
	for _, g := range gg.Generators {
		changes, err := g.RequireChanges("isEmpty")
		if err != nil {
			return false, err
		}
		if empty, err := changes.IsEmpty(ctx); err != nil {
			return false, err
		} else if !empty {
			return false, nil
		}
	}
	return true, nil
}

func (gg *GeneratorGroup) Changes(ctx context.Context, conflictStrategy WithChangesetsMergeConflict) (*Changeset, error) {
	res, err := NewEmptyChangeset(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]dagql.ObjectResult[*Changeset], 0, len(gg.Generators))
	cs := make([]*Changeset, 0, len(gg.Generators))
	for _, g := range gg.Generators {
		changes, err := g.RequireChangesResult("changes")
		if err != nil {
			return nil, err
		}
		results = append(results, changes)
		cs = append(cs, changes.Self())
	}
	merged, err := res.WithChangesets(ctx, cs, conflictStrategy)
	if err != nil {
		return nil, err
	}
	if err := gg.verifySkippedModules(ctx, results); err != nil {
		return nil, err
	}
	return merged, nil
}

// RegeneratedModuleMessage is what the report says about a skipped module that
// loads once the run's changes are applied.
const RegeneratedModuleMessage = "could not load before this run's changes; loads with them applied"

// verifySkippedModules settles the modules this group skipped at load time.
// A module that failed to load because its generated files were missing or
// stale is often repaired by the very run that skipped it — which can only be
// told after generating, and only for sure by trying. For each skipped module
// whose directory the run's changes touch, load it again against the
// workspace with those changes applied, under a "regenerated" span named like
// the skipped-module span (GenerateRegeneratedAttr). The span's outcome is the
// answer the report shows in place of the pre-generation error: it loads, or
// it still fails — with the post-generation error, which is the one the user
// has to fix. Untouched modules are left alone: nothing in this run could have
// changed their outcome, so their load error stands.
//
// The changed paths and the overlay come from the per-generator changesets
// rather than the merged one: those are attached results, while the merged
// changeset is only attached once Changes returns. Their union is what the
// merge contains anyway (a conflict would have failed the merge above).
func (gg *GeneratorGroup) verifySkippedModules(ctx context.Context, changes []dagql.ObjectResult[*Changeset]) error {
	if len(gg.LoadFailures) == 0 || gg.BoundWorkspace.Self() == nil {
		return nil
	}
	var changed []string
	for _, cs := range changes {
		paths, err := cs.Self().ComputePaths(ctx)
		if err != nil {
			return fmt.Errorf("verify skipped modules: compute changed paths: %w", err)
		}
		changed = append(changed, paths.Added...)
		changed = append(changed, paths.Modified...)
		changed = append(changed, paths.Removed...)
	}

	// Resolve the modules (and their workspace-installed dependencies)
	// against the bound workspace, as the generators themselves ran.
	ctx = WorkspaceToContext(ctx, gg.BoundWorkspace)

	var generated dagql.ObjectResult[*Directory]
	for _, failure := range gg.LoadFailures {
		if !failure.Regenerated(changed) {
			continue
		}
		if generated.Self() == nil {
			var err error
			generated, err = gg.generatedWorkspaceRoot(ctx, changes)
			if err != nil {
				return fmt.Errorf("verify skipped modules: %w", err)
			}
		}
		// Each module's outcome is its own span's status; a failure here is the
		// report's answer, not a reason to fail the run (the skipped module was
		// already tolerated at load time).
		verifySkippedModule(ctx, generated, failure)
	}
	return nil
}

// generatedWorkspaceRoot is the bound workspace's full root with the run's
// changesets applied, in generator order — what the workspace holds once the
// changes are applied. It goes through Directory.withChanges on a full read
// rather than Workspace.withChanges: a host-backed workspace overlay reads
// sparsely (only the touched paths, and a changeset touches its parent
// directories), which is not a tree a module can be loaded from.
func (gg *GeneratorGroup) generatedWorkspaceRoot(ctx context.Context, changes []dagql.ObjectResult[*Changeset]) (dagql.ObjectResult[*Directory], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}
	var root dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, gg.BoundWorkspace, &root, dagql.Selector{
		Field: "directory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/")}},
	}); err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("read workspace root: %w", err)
	}
	for _, cs := range changes {
		csID, err := cs.ID()
		if err != nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("changeset ID: %w", err)
		}
		if err := dag.Select(ctx, root, &root, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*Changeset](csID)}},
		}); err != nil {
			return dagql.ObjectResult[*Directory]{}, fmt.Errorf("apply changes to workspace root: %w", err)
		}
	}
	return root, nil
}

// verifySkippedModule loads one skipped module from the generated workspace
// root and records the outcome on its "regenerated" span: OK with
// RegeneratedModuleMessage, or the described post-generation load error.
func verifySkippedModule(ctx context.Context, generated dagql.ObjectResult[*Directory], failure ModuleLoadFailure) {
	var rerr error
	ctx, span := Tracer(ctx).Start(ctx, failure.Name,
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.Bool(telemetryattrs.GenerateRegeneratedAttr, true),
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
		),
	)
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		rerr = err
		return
	}
	var src dagql.ObjectResult[*ModuleSource]
	if err := dag.Select(ctx, generated, &src, dagql.Selector{
		Field: "asModuleSource",
		Args:  []dagql.NamedInput{{Name: "sourceRootPath", Value: dagql.NewString(filepath.ToSlash(failure.Dir))}},
	}); err != nil {
		rerr = LoadFailureCause("still fails to load with this run's changes: ", err)
		return
	}
	var mod dagql.ObjectResult[*Module]
	if err := dag.Select(ctx, src, &mod, dagql.Selector{Field: "asModule"}); err != nil {
		rerr = LoadFailureCause("still fails to load with this run's changes: ", err)
		return
	}
	span.SetAttributes(attribute.String(telemetry.UIMessageAttr, RegeneratedModuleMessage))
}

func (gg *GeneratorGroup) Clone() *GeneratorGroup {
	c := *gg
	if gg.Node != nil {
		c.Node = gg.Node.Clone()
	}
	c.Generators = make([]*Generator, len(gg.Generators))
	for i := range c.Generators {
		c.Generators[i] = gg.Generators[i].Clone()
	}
	if gg.LoadFailures != nil {
		c.LoadFailures = append([]ModuleLoadFailure(nil), gg.LoadFailures...)
	}
	return &c
}

func encodePersistedGeneratorPayload(
	cache dagql.PersistedObjectCache,
	tree *persistedModTreeEncoder,
	g *Generator,
) (persistedGeneratorPayload, error) {
	if g == nil {
		return persistedGeneratorPayload{}, fmt.Errorf("encode persisted generator: nil generator")
	}
	nodeID, err := tree.Add(g.Node)
	if err != nil {
		return persistedGeneratorPayload{}, err
	}
	payload := persistedGeneratorPayload{
		NodeID:    nodeID,
		Completed: g.Completed,
	}
	if g.Completed && g.Changes.Self() != nil {
		changesID, err := encodePersistedObjectRef(cache, g.Changes, "generator changes")
		if err != nil {
			return persistedGeneratorPayload{}, err
		}
		payload.ChangesResultID = changesID
	}
	return payload, nil
}

func decodePersistedGeneratorPayload(
	ctx context.Context,
	dag *dagql.Server,
	nodes map[int]*ModTreeNode,
	payload persistedGeneratorPayload,
) (*Generator, error) {
	if payload.NodeID == 0 {
		return nil, fmt.Errorf("decode persisted generator: missing node ID")
	}
	node, ok := nodes[payload.NodeID]
	if !ok {
		return nil, fmt.Errorf("decode persisted generator: unknown node ID %d", payload.NodeID)
	}
	g := &Generator{
		Node:      node,
		Completed: payload.Completed,
	}
	if payload.ChangesResultID != 0 {
		changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dag, payload.ChangesResultID, "generator changes")
		if err != nil {
			return nil, err
		}
		g.Changes = changes
	}
	return g, nil
}

func (g *Generator) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	tree := newPersistedModTreeEncoder(cache)
	generatorPayload, err := encodePersistedGeneratorPayload(cache, tree, g)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload, err := json.Marshal(persistedGeneratorObjectPayload{
		Tree:      tree.tree,
		Generator: generatorPayload,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted generator payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payload), nil
}

func (*Generator) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedGeneratorObjectPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted generator payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dag, persisted.Tree)
	if err != nil {
		return nil, err
	}
	return decodePersistedGeneratorPayload(ctx, dag, nodes, persisted.Generator)
}

func (g *Generator) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if g == nil {
		return nil, nil
	}
	owned, err := attachModTreeNodeDependencyResults(g.Node, attach)
	if err != nil {
		return nil, err
	}
	if g.Changes.Self() != nil {
		attached, err := attach(g.Changes)
		if err != nil {
			return nil, fmt.Errorf("attach generator changes: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Changeset])
		if !ok {
			return nil, fmt.Errorf("attach generator changes: unexpected result %T", attached)
		}
		g.Changes = typed
		owned = append(owned, typed)
	}
	return owned, nil
}

func (gg *GeneratorGroup) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if gg == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted generator group: nil generator group")
	}
	tree := newPersistedModTreeEncoder(cache)
	nodeID, err := tree.Add(gg.Node)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	generatorPayloads := make([]persistedGeneratorPayload, 0, len(gg.Generators))
	for _, generator := range gg.Generators {
		generatorPayload, err := encodePersistedGeneratorPayload(cache, tree, generator)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		generatorPayloads = append(generatorPayloads, generatorPayload)
	}
	groupPayload := persistedGeneratorGroupPayload{
		Tree:         tree.tree,
		NodeID:       nodeID,
		Generators:   generatorPayloads,
		LoadFailures: gg.LoadFailures,
	}
	if gg.BoundWorkspace.Self() != nil {
		wsID, err := encodePersistedObjectRef(cache, gg.BoundWorkspace, "bound workspace")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		groupPayload.BoundWorkspaceResultID = wsID
	}
	payload, err := json.Marshal(groupPayload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted generator group payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payload), nil
}

func (*GeneratorGroup) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedGeneratorGroupPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted generator group payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dag, persisted.Tree)
	if err != nil {
		return nil, err
	}
	var node *ModTreeNode
	if persisted.NodeID != 0 {
		var ok bool
		node, ok = nodes[persisted.NodeID]
		if !ok {
			return nil, fmt.Errorf("decode persisted generator group: unknown node ID %d", persisted.NodeID)
		}
	}
	generators := make([]*Generator, 0, len(persisted.Generators))
	for _, generatorPayload := range persisted.Generators {
		generator, err := decodePersistedGeneratorPayload(ctx, dag, nodes, generatorPayload)
		if err != nil {
			return nil, err
		}
		generators = append(generators, generator)
	}
	group := &GeneratorGroup{
		Node:         node,
		Generators:   generators,
		LoadFailures: persisted.LoadFailures,
	}
	if persisted.BoundWorkspaceResultID != 0 {
		ws, err := loadPersistedObjectResultByResultID[*Workspace](ctx, dag, persisted.BoundWorkspaceResultID, "bound workspace")
		if err != nil {
			return nil, err
		}
		group.BoundWorkspace = ws
	}
	return group, nil
}

func (gg *GeneratorGroup) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if gg == nil {
		return nil, nil
	}
	owned, err := attachModTreeNodeDependencyResults(gg.Node, attach)
	if err != nil {
		return nil, err
	}
	for _, generator := range gg.Generators {
		generatorDeps, err := generator.AttachDependencyResults(ctx, nil, attach)
		if err != nil {
			return nil, err
		}
		owned = append(owned, generatorDeps...)
	}
	// Attach the bound workspace so it becomes cache-backed and its result ID
	// resolves when the group is persisted (EncodePersistedObject) and reloaded.
	if gg.BoundWorkspace.Self() != nil {
		attached, err := attach(gg.BoundWorkspace)
		if err != nil {
			return nil, fmt.Errorf("attach bound workspace: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Workspace])
		if !ok {
			return nil, fmt.Errorf("attach bound workspace: unexpected result %T", attached)
		}
		gg.BoundWorkspace = typed
		owned = append(owned, typed)
	}
	return owned, nil
}

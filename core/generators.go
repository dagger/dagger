package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
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

type GeneratorGroup struct {
	Node       *ModTreeNode `json:"node"`
	Generators []*Generator `json:"generators"`
	// LoadFailures carries the per-module load-failure messages tolerated during
	// an unscoped 'dagger generate' (empty when strict, or when every module
	// loaded). Surfaced on the API so the CLI can warn and honor --require-load.
	LoadFailures []string `json:"loadFailures,omitempty"`
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
	LoadFailures           []string                    `json:"loadFailures,omitempty"`
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
	cs := make([]*Changeset, 0, len(gg.Generators))
	for _, g := range gg.Generators {
		changes, err := g.RequireChanges("changes")
		if err != nil {
			return nil, err
		}
		cs = append(cs, changes)
	}
	return res.WithChangesets(ctx, cs, conflictStrategy)
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
		c.LoadFailures = append([]string(nil), gg.LoadFailures...)
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

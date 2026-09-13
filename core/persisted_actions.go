package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// Codecs and dependency hooks for module-tree action values: checks, ups and
// terminal targets with their groups. Every payload carries one node table
// shared by its members so parent sharing survives, and each group records
// its exact bound workspace. Decoding restores data and references only: no
// action runs and no terminal or service starts.

type persistedCheckPayload struct {
	NodeID        int    `json:"nodeID,omitempty"`
	Completed     bool   `json:"completed,omitempty"`
	Passed        bool   `json:"passed,omitempty"`
	IsGenerate    bool   `json:"isGenerate,omitempty"`
	ErrorResultID uint64 `json:"errorResultID,omitempty"`
}

type persistedUpPayload struct {
	NodeID       int           `json:"nodeID,omitempty"`
	PortMappings []PortForward `json:"portMappings,omitempty"`
}

type persistedActionLeafPayload struct {
	NodeID int `json:"nodeID,omitempty"`
}

type persistedTerminalTargetPayload struct {
	Tree     persistedModTree           `json:"tree"`
	Terminal persistedActionLeafPayload `json:"terminal"`
}

type persistedCheckObjectPayload struct {
	Tree  persistedModTree      `json:"tree"`
	Check persistedCheckPayload `json:"check"`
}

type persistedUpObjectPayload struct {
	Tree persistedModTree   `json:"tree"`
	Up   persistedUpPayload `json:"up"`
}

type persistedCheckGroupPayload struct {
	Tree                   persistedModTree        `json:"tree"`
	NodeID                 int                     `json:"nodeID,omitempty"`
	Checks                 []persistedCheckPayload `json:"checks,omitempty"`
	BoundWorkspaceResultID uint64                  `json:"boundWorkspaceResultID,omitempty"`
}

type persistedUpGroupPayload struct {
	Tree                   persistedModTree     `json:"tree"`
	NodeID                 int                  `json:"nodeID,omitempty"`
	Ups                    []persistedUpPayload `json:"ups,omitempty"`
	BoundWorkspaceResultID uint64               `json:"boundWorkspaceResultID,omitempty"`
}

type persistedTerminalGroupPayload struct {
	Tree                   persistedModTree             `json:"tree"`
	NodeID                 int                          `json:"nodeID,omitempty"`
	Terminals              []persistedActionLeafPayload `json:"terminals,omitempty"`
	BoundWorkspaceResultID uint64                       `json:"boundWorkspaceResultID,omitempty"`
}

func persistedModTreeNodeByID(nodes map[int]*ModTreeNode, id int, what string) (*ModTreeNode, error) {
	if id == 0 {
		return nil, nil
	}
	node, ok := nodes[id]
	if !ok {
		return nil, fmt.Errorf("decode persisted %s: unknown node ID %d", what, id)
	}
	return node, nil
}

func encodePersistedCheck(enc *dagql.PersistEncodeContext, tree *persistedModTreeEncoder, c *Check) (persistedCheckPayload, error) {
	if c == nil {
		return persistedCheckPayload{}, fmt.Errorf("encode persisted check: nil check")
	}
	nodeID, err := tree.Add(c.Node)
	if err != nil {
		return persistedCheckPayload{}, err
	}
	payload := persistedCheckPayload{
		NodeID:     nodeID,
		Completed:  c.Completed,
		Passed:     c.Passed,
		IsGenerate: c.IsGenerate,
	}
	if c.Error.Valid && c.Error.Value.Self() != nil {
		errorID, err := encodePersistedObjectRef(enc, c.Error.Value, "check error")
		if err != nil {
			return persistedCheckPayload{}, err
		}
		payload.ErrorResultID = errorID
	}
	return payload, nil
}

func decodePersistedCheck(ctx context.Context, dec *dagql.PersistDecodeContext, nodes map[int]*ModTreeNode, payload persistedCheckPayload) (*Check, error) {
	node, err := persistedModTreeNodeByID(nodes, payload.NodeID, "check")
	if err != nil {
		return nil, err
	}
	c := &Check{
		Node:       node,
		Completed:  payload.Completed,
		Passed:     payload.Passed,
		IsGenerate: payload.IsGenerate,
	}
	if payload.ErrorResultID != 0 {
		errRes, err := loadPersistedObjectResultByResultID[*Error](ctx, dec, payload.ErrorResultID, "check error")
		if err != nil {
			return nil, err
		}
		c.Error = dagql.NonNull(errRes)
	}
	return c, nil
}

func encodePersistedUp(tree *persistedModTreeEncoder, up *Up) (persistedUpPayload, error) {
	if up == nil {
		return persistedUpPayload{}, fmt.Errorf("encode persisted up: nil up")
	}
	nodeID, err := tree.Add(up.Node)
	if err != nil {
		return persistedUpPayload{}, err
	}
	return persistedUpPayload{NodeID: nodeID, PortMappings: append([]PortForward(nil), up.PortMappings...)}, nil
}

func decodePersistedUp(nodes map[int]*ModTreeNode, payload persistedUpPayload) (*Up, error) {
	node, err := persistedModTreeNodeByID(nodes, payload.NodeID, "up")
	if err != nil {
		return nil, err
	}
	return &Up{Node: node, PortMappings: append([]PortForward(nil), payload.PortMappings...)}, nil
}

func encodePersistedActionLeaf(tree *persistedModTreeEncoder, node *ModTreeNode, what string) (persistedActionLeafPayload, error) {
	nodeID, err := tree.Add(node)
	if err != nil {
		return persistedActionLeafPayload{}, fmt.Errorf("encode persisted %s: %w", what, err)
	}
	return persistedActionLeafPayload{NodeID: nodeID}, nil
}

func encodePersistedBoundWorkspace(enc *dagql.PersistEncodeContext, ws dagql.ObjectResult[*Workspace]) (uint64, error) {
	if ws.Self() == nil {
		return 0, nil
	}
	return encodePersistedObjectRef(enc, ws, "bound workspace")
}

func decodePersistedBoundWorkspace(ctx context.Context, dec *dagql.PersistDecodeContext, id uint64) (dagql.ObjectResult[*Workspace], error) {
	return loadPersistedObjectResultByResultID[*Workspace](ctx, dec, id, "bound workspace")
}

func attachPersistedBoundWorkspace(ws *dagql.ObjectResult[*Workspace], attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if ws.Self() == nil {
		return nil, nil
	}
	attached, err := attach(*ws)
	if err != nil {
		return nil, fmt.Errorf("attach bound workspace: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Workspace])
	if !ok {
		return nil, fmt.Errorf("attach bound workspace: unexpected result %T", attached)
	}
	*ws = typed
	return []dagql.AnyResult{typed}, nil
}

func attachPersistedCheckResults(c *Check, attach func(dagql.AnyResult) (dagql.AnyResult, error), seen map[*ModTreeNode]struct{}) ([]dagql.AnyResult, error) {
	if c == nil {
		return nil, nil
	}
	owned, err := attachModTreeNodeDependencyResultsWithSeen(c.Node, attach, seen)
	if err != nil {
		return nil, err
	}
	if c.Error.Valid && c.Error.Value.Self() != nil {
		attached, err := attach(c.Error.Value)
		if err != nil {
			return nil, fmt.Errorf("attach check error: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Error])
		if !ok {
			return nil, fmt.Errorf("attach check error: unexpected result %T", attached)
		}
		c.Error = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	return owned, nil
}

// TerminalTarget.

func (target *TerminalTarget) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if target == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted terminal target: nil target")
	}
	tree := newPersistedModTreeEncoder(enc)
	leaf, err := encodePersistedActionLeaf(tree, target.Node, "terminal target")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectPayload(persistedTerminalTargetPayload{Tree: tree.tree, Terminal: leaf})
}

func (*TerminalTarget) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedTerminalTargetPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted terminal target payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	node, err := persistedModTreeNodeByID(nodes, persisted.Terminal.NodeID, "terminal target")
	if err != nil {
		return nil, err
	}
	return &TerminalTarget{Node: node}, nil
}

func (target *TerminalTarget) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if target == nil {
		return nil, nil
	}
	return attachModTreeNodeDependencyResults(target.Node, attach)
}

// Check.

func (c *Check) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	tree := newPersistedModTreeEncoder(enc)
	payload, err := encodePersistedCheck(enc, tree, c)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectPayload(persistedCheckObjectPayload{Tree: tree.tree, Check: payload})
}

func (*Check) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCheckObjectPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted check payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	return decodePersistedCheck(ctx, dec, nodes, persisted.Check)
}

func (c *Check) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return attachPersistedCheckResults(c, attach, map[*ModTreeNode]struct{}{})
}

// Up.

func (up *Up) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	tree := newPersistedModTreeEncoder(enc)
	payload, err := encodePersistedUp(tree, up)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectPayload(persistedUpObjectPayload{Tree: tree.tree, Up: payload})
}

func (*Up) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedUpObjectPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted up payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	return decodePersistedUp(nodes, persisted.Up)
}

func (up *Up) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if up == nil {
		return nil, nil
	}
	return attachModTreeNodeDependencyResults(up.Node, attach)
}

// CheckGroup.

func (r *CheckGroup) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if r == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted check group: nil check group")
	}
	tree := newPersistedModTreeEncoder(enc)
	nodeID, err := tree.Add(r.Node)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedCheckGroupPayload{NodeID: nodeID, Checks: make([]persistedCheckPayload, 0, len(r.Checks))}
	for _, check := range r.Checks {
		encoded, err := encodePersistedCheck(enc, tree, check)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Checks = append(payload.Checks, encoded)
	}
	if payload.BoundWorkspaceResultID, err = encodePersistedBoundWorkspace(enc, r.BoundWorkspace); err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload.Tree = tree.tree
	return encodePersistedObjectPayload(payload)
}

func (*CheckGroup) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCheckGroupPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted check group payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	node, err := persistedModTreeNodeByID(nodes, persisted.NodeID, "check group")
	if err != nil {
		return nil, err
	}
	group := &CheckGroup{Node: node, Checks: make([]*Check, 0, len(persisted.Checks))}
	for _, check := range persisted.Checks {
		decoded, err := decodePersistedCheck(ctx, dec, nodes, check)
		if err != nil {
			return nil, err
		}
		group.Checks = append(group.Checks, decoded)
	}
	if group.BoundWorkspace, err = decodePersistedBoundWorkspace(ctx, dec, persisted.BoundWorkspaceResultID); err != nil {
		return nil, err
	}
	return group, nil
}

func (r *CheckGroup) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if r == nil {
		return nil, nil
	}
	seen := map[*ModTreeNode]struct{}{}
	owned, err := attachModTreeNodeDependencyResultsWithSeen(r.Node, attach, seen)
	if err != nil {
		return nil, err
	}
	for _, check := range r.Checks {
		deps, err := attachPersistedCheckResults(check, attach, seen)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}
	deps, err := attachPersistedBoundWorkspace(&r.BoundWorkspace, attach)
	if err != nil {
		return nil, err
	}
	return append(owned, deps...), nil
}

// UpGroup.

func (ug *UpGroup) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if ug == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted up group: nil up group")
	}
	tree := newPersistedModTreeEncoder(enc)
	nodeID, err := tree.Add(ug.Node)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedUpGroupPayload{NodeID: nodeID, Ups: make([]persistedUpPayload, 0, len(ug.Ups))}
	for _, up := range ug.Ups {
		encoded, err := encodePersistedUp(tree, up)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Ups = append(payload.Ups, encoded)
	}
	if payload.BoundWorkspaceResultID, err = encodePersistedBoundWorkspace(enc, ug.BoundWorkspace); err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload.Tree = tree.tree
	return encodePersistedObjectPayload(payload)
}

func (*UpGroup) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedUpGroupPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted up group payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	node, err := persistedModTreeNodeByID(nodes, persisted.NodeID, "up group")
	if err != nil {
		return nil, err
	}
	group := &UpGroup{Node: node, Ups: make([]*Up, 0, len(persisted.Ups))}
	for _, up := range persisted.Ups {
		decoded, err := decodePersistedUp(nodes, up)
		if err != nil {
			return nil, err
		}
		group.Ups = append(group.Ups, decoded)
	}
	if group.BoundWorkspace, err = decodePersistedBoundWorkspace(ctx, dec, persisted.BoundWorkspaceResultID); err != nil {
		return nil, err
	}
	return group, nil
}

func (ug *UpGroup) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if ug == nil {
		return nil, nil
	}
	seen := map[*ModTreeNode]struct{}{}
	owned, err := attachModTreeNodeDependencyResultsWithSeen(ug.Node, attach, seen)
	if err != nil {
		return nil, err
	}
	for _, up := range ug.Ups {
		if up == nil {
			continue
		}
		deps, err := attachModTreeNodeDependencyResultsWithSeen(up.Node, attach, seen)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}
	deps, err := attachPersistedBoundWorkspace(&ug.BoundWorkspace, attach)
	if err != nil {
		return nil, err
	}
	return append(owned, deps...), nil
}

// TerminalGroup.

func (r *TerminalGroup) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if r == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted terminal group: nil terminal group")
	}
	tree := newPersistedModTreeEncoder(enc)
	nodeID, err := tree.Add(r.Node)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedTerminalGroupPayload{NodeID: nodeID, Terminals: make([]persistedActionLeafPayload, 0, len(r.Terminals))}
	for _, terminal := range r.Terminals {
		if terminal == nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted terminal group: nil terminal target")
		}
		leaf, err := encodePersistedActionLeaf(tree, terminal.Node, "terminal target")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Terminals = append(payload.Terminals, leaf)
	}
	if payload.BoundWorkspaceResultID, err = encodePersistedBoundWorkspace(enc, r.BoundWorkspace); err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload.Tree = tree.tree
	return encodePersistedObjectPayload(payload)
}

func (*TerminalGroup) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedTerminalGroupPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted terminal group payload: %w", err)
	}
	nodes, err := decodePersistedModTree(ctx, dec, persisted.Tree)
	if err != nil {
		return nil, err
	}
	node, err := persistedModTreeNodeByID(nodes, persisted.NodeID, "terminal group")
	if err != nil {
		return nil, err
	}
	group := &TerminalGroup{Node: node, Terminals: make([]*TerminalTarget, 0, len(persisted.Terminals))}
	for _, leaf := range persisted.Terminals {
		terminalNode, err := persistedModTreeNodeByID(nodes, leaf.NodeID, "terminal target")
		if err != nil {
			return nil, err
		}
		group.Terminals = append(group.Terminals, &TerminalTarget{Node: terminalNode})
	}
	if group.BoundWorkspace, err = decodePersistedBoundWorkspace(ctx, dec, persisted.BoundWorkspaceResultID); err != nil {
		return nil, err
	}
	return group, nil
}

func (r *TerminalGroup) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if r == nil {
		return nil, nil
	}
	seen := map[*ModTreeNode]struct{}{}
	owned, err := attachModTreeNodeDependencyResultsWithSeen(r.Node, attach, seen)
	if err != nil {
		return nil, err
	}
	for _, terminal := range r.Terminals {
		if terminal == nil {
			continue
		}
		deps, err := attachModTreeNodeDependencyResultsWithSeen(terminal.Node, attach, seen)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}
	deps, err := attachPersistedBoundWorkspace(&r.BoundWorkspace, attach)
	if err != nil {
		return nil, err
	}
	return append(owned, deps...), nil
}

// Visitors.

func visitPersistedCheckRefs(w *persistedRefWalker, p *persistedCheckPayload) error {
	return w.child("errorResultID", &p.ErrorResultID)
}

var persistedTerminalTargetVisitor = persistedStructVisitor("", func(p *persistedTerminalTargetPayload, w *persistedRefWalker) error {
	return visitPersistedModTreeRefs(w.at("tree"), &p.Tree)
})

var persistedCheckVisitor = persistedStructVisitor("", func(p *persistedCheckObjectPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	return visitPersistedCheckRefs(w.at("check"), &p.Check)
})

var persistedUpVisitor = persistedStructVisitor("", func(p *persistedUpObjectPayload, w *persistedRefWalker) error {
	return visitPersistedModTreeRefs(w.at("tree"), &p.Tree)
})

var persistedCheckGroupVisitor = persistedStructVisitor("", func(p *persistedCheckGroupPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	for i := range p.Checks {
		if err := visitPersistedCheckRefs(w.at("checks").index(i), &p.Checks[i]); err != nil {
			return err
		}
	}
	return w.child("boundWorkspaceResultID", &p.BoundWorkspaceResultID)
})

var persistedUpGroupVisitor = persistedStructVisitor("", func(p *persistedUpGroupPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	return w.child("boundWorkspaceResultID", &p.BoundWorkspaceResultID)
})

var persistedTerminalGroupVisitor = persistedStructVisitor("", func(p *persistedTerminalGroupPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	return w.child("boundWorkspaceResultID", &p.BoundWorkspaceResultID)
})

var persistedActionFamilies = []dagql.PersistedObjectFamily{
	{Name: "core.TerminalTarget", Typed: (*TerminalTarget)(nil), Visitor: persistedTerminalTargetVisitor},
	{Name: "core.Check", Typed: (*Check)(nil), Visitor: persistedCheckVisitor},
	{Name: "core.Up", Typed: (*Up)(nil), Visitor: persistedUpVisitor},
	{Name: "core.CheckGroup", Typed: (*CheckGroup)(nil), Visitor: persistedCheckGroupVisitor},
	{Name: "core.UpGroup", Typed: (*UpGroup)(nil), Visitor: persistedUpGroupVisitor},
	{Name: "core.TerminalGroup", Typed: (*TerminalGroup)(nil), Visitor: persistedTerminalGroupVisitor},
}

func init() {
	for _, family := range persistedActionFamilies {
		dagql.RegisterPersistedObjectFamily(family)
	}
}

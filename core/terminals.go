package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// TerminalTarget is a module value that supports an interactive terminal.
type TerminalTarget struct {
	Node *ModTreeNode `json:"node"`
}

// TerminalGroup is a set of terminal targets selected from a module tree.
type TerminalGroup struct {
	Node      *ModTreeNode      `json:"node"`
	Terminals []*TerminalTarget `json:"terminals"`

	// BoundWorkspace is the Workspace this group was rolled up from. Run uses it
	// so auto-injected Workspace arguments resolve against that workspace.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

func NewTerminalGroup(ctx context.Context, mod dagql.ObjectResult[*Module], include []string) (*TerminalGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	terminalNodes, err := rootNode.RollupTerminals(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	terminals := make([]*TerminalTarget, 0, len(terminalNodes))
	for _, terminalNode := range terminalNodes {
		terminals = append(terminals, &TerminalTarget{Node: terminalNode})
	}

	return &TerminalGroup{
		Node:      rootNode,
		Terminals: terminals,
	}, nil
}

func (*TerminalGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TerminalGroup",
		NonNull:   true,
	}
}

func (r *TerminalGroup) List() []*TerminalTarget {
	return r.Terminals
}

// Run opens the selected terminal target. A terminal group must contain one
// target because interactive terminals cannot run in parallel.
func (r *TerminalGroup) Run(ctx context.Context) error {
	switch len(r.Terminals) {
	case 0:
		return fmt.Errorf("no terminal targets selected")
	case 1:
		// Continue below.
	default:
		names := make([]string, 0, len(r.Terminals))
		for _, terminal := range r.Terminals {
			names = append(names, terminal.Name())
		}
		return fmt.Errorf("terminal selection matched %d targets: %s", len(names), strings.Join(names, ", "))
	}

	if r.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, r.BoundWorkspace)
	}
	return r.Terminals[0].Node.RunTerminal(ctx)
}

func (*TerminalTarget) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TerminalTarget",
		NonNull:   true,
	}
}

func (t *TerminalTarget) Path() []string {
	return t.Node.Path()
}

func (t *TerminalTarget) Name() string {
	return t.Node.PathString()
}

func (t *TerminalTarget) Description() string {
	return t.Node.Description
}

func (t *TerminalTarget) OriginalModule() *Module {
	return t.Node.OriginalModule.Self()
}

func supportsTerminal(node *ModTreeNode) bool {
	if node == nil || node.Type.Self() == nil {
		return false
	}
	typeDef := node.Type.Self()
	if typeDef.Optional || typeDef.Kind != TypeDefKindObject || !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
		return false
	}

	switch typeDef.AsObject.Value.Self().Name {
	case "Container", "Directory":
		return true
	default:
		return false
	}
}

func (node *ModTreeNode) RunTerminal(ctx context.Context) error {
	if !supportsTerminal(node) {
		return fmt.Errorf("%q: unsupported terminal target type", node.PathString())
	}

	var target dagql.AnyObjectResult
	if err := node.DagqlValue(ctx, &target); err != nil {
		return err
	}
	var result dagql.AnyObjectResult
	return node.DagqlServer.Select(dagql.WithNonInternalTelemetry(ctx), target, &result,
		dagql.Selector{Field: "terminal"},
	)
}

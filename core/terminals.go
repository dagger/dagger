package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
	target, err := r.selected()
	if err != nil {
		return err
	}
	return target.Node.RunTerminal(r.workspaceContext(ctx))
}

// Exec runs the selected terminal target's command non-interactively, with
// stdin as its standard input, and returns the container after execution. Any
// exit code is allowed, so the caller can inspect it.
func (r *TerminalGroup) Exec(ctx context.Context, stdin string) (dagql.ObjectResult[*Container], error) {
	target, err := r.selected()
	if err != nil {
		return dagql.ObjectResult[*Container]{}, err
	}
	return target.Node.ExecTerminal(r.workspaceContext(ctx), stdin)
}

func (r *TerminalGroup) selected() (*TerminalTarget, error) {
	switch len(r.Terminals) {
	case 0:
		return nil, fmt.Errorf("no terminal targets selected")
	case 1:
		return r.Terminals[0], nil
	default:
		names := make([]string, 0, len(r.Terminals))
		for _, terminal := range r.Terminals {
			names = append(names, terminal.Name())
		}
		return nil, fmt.Errorf("terminal selection matched %d targets: %s", len(names), strings.Join(names, ", "))
	}
}

func (r *TerminalGroup) workspaceContext(ctx context.Context) context.Context {
	if r.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, r.BoundWorkspace)
	}
	return ctx
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
	return t.Node.CommandName()
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

// ExecTerminal runs the command that RunTerminal would open, in the same
// container, with stdin as its standard input instead of a terminal. Like a
// terminal, each call runs the command again.
func (node *ModTreeNode) ExecTerminal(ctx context.Context, stdin string) (res dagql.ObjectResult[*Container], _ error) {
	if !supportsTerminal(node) {
		return res, fmt.Errorf("%q: unsupported terminal target type", node.PathString())
	}
	srv := node.DagqlServer
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return res, err
	}

	// Get a new synthetic container for each call, so the exec below is
	// never a cache hit.
	var ctr dagql.ObjectResult[*Container]
	switch node.Type.Self().AsObject.Value.Self().Name {
	case "Container":
		if err := node.DagqlValue(ctx, &ctr); err != nil {
			return res, err
		}
		// Evaluate first: the default terminal command can be set lazily.
		if err := cache.Evaluate(ctx, ctr); err != nil {
			return res, err
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return res, err
		}
		clone, err := cloneContainerForTerminal(ctx, query, ctr.Self())
		if err != nil {
			return res, err
		}
		ctr, err = newSyntheticTerminalContainerResult(srv, clone, "terminal_exec_container")
		if err != nil {
			return res, err
		}
	case "Directory":
		var dir dagql.ObjectResult[*Directory]
		if err := node.DagqlValue(ctx, &dir); err != nil {
			return res, err
		}
		ctr, err = dir.Self().terminalContainer(ctx, dagql.ObjectResult[*Container]{}, dir)
		if err != nil {
			return res, err
		}
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return res, err
	}
	attached, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, ctr)
	if err != nil {
		return res, err
	}
	ctr, ok := attached.(dagql.ObjectResult[*Container])
	if !ok {
		return res, fmt.Errorf("attach terminal container: expected %T, got %T", ctr, attached)
	}

	// Match the terminal's command, and its fallback to sh.
	defaults := ctr.Self().DefaultTerminalCmd
	args := defaults.Args
	if len(args) == 0 {
		args = []string{"sh"}
	}
	ctx = dagql.WithNonInternalTelemetry(ctx)
	if err := srv.Select(ctx, ctr, &res, dagql.Selector{
		Field: "withExec",
		Args: []dagql.NamedInput{
			{Name: "args", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args...))},
			{Name: "stdin", Value: dagql.String(stdin)},
			{Name: "expect", Value: ReturnAny},
			{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(defaults.ExperimentalPrivilegedNesting.Value.Bool())},
			{Name: "insecureRootCapabilities", Value: dagql.NewBoolean(defaults.InsecureRootCapabilities.Value.Bool())},
		},
	}); err != nil {
		return res, err
	}
	// Run the command now, in the context of the bound workspace.
	return res, cache.Evaluate(ctx, res)
}

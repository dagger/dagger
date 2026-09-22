package core

import (
	"context"
	"fmt"
	"slices"
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

// TerminalCopy is a directory to copy into a terminal container.
type TerminalCopy struct {
	Path   string      `doc:"Location of the copied directory. A relative path is relative to the container's working directory."`
	Source DirectoryID `doc:"The directory to copy."`
}

func (TerminalCopy) TypeName() string {
	return "TerminalCopy"
}

// TerminalSetupArgs set up a terminal container before its command runs.
type TerminalSetupArgs struct {
	Copy []dagql.InputObject[TerminalCopy] `default:"[]"`
	Init []string                          `default:"[]"`
}

// Run opens the selected terminal target.
func (r *TerminalGroup) Run(ctx context.Context, setup TerminalSetupArgs) error {
	target, err := r.selected()
	if err != nil {
		return err
	}
	return target.Node.RunTerminal(r.workspaceContext(ctx), setup)
}

// Exec runs the selected terminal target's command with args appended, and
// returns the container after execution.
func (r *TerminalGroup) Exec(ctx context.Context, args []string, stdin string, setup TerminalSetupArgs) (dagql.ObjectResult[*Container], error) {
	target, err := r.selected()
	if err != nil {
		return dagql.ObjectResult[*Container]{}, err
	}
	return target.Node.ExecTerminal(r.workspaceContext(ctx), args, stdin, setup)
}

// selected returns the one target to open, because terminals cannot run in
// parallel. If more than one target is selected, it is the only container, or
// else the only container in the workspace entrypoint module.
func (r *TerminalGroup) selected() (*TerminalTarget, error) {
	if len(r.Terminals) == 0 {
		return nil, fmt.Errorf("no terminal targets selected")
	}
	if len(r.Terminals) == 1 {
		return r.Terminals[0], nil
	}
	var containers, entrypointContainers []*TerminalTarget
	for _, terminal := range r.Terminals {
		if terminalType(terminal.Node) != "Container" {
			continue
		}
		containers = append(containers, terminal)
		if terminal.Node.inWorkspaceEntrypoint() {
			entrypointContainers = append(entrypointContainers, terminal)
		}
	}
	if len(containers) == 1 {
		return containers[0], nil
	}
	if len(entrypointContainers) == 1 {
		return entrypointContainers[0], nil
	}
	names := make([]string, 0, len(r.Terminals))
	for _, terminal := range r.Terminals {
		names = append(names, terminal.Name())
	}
	return nil, fmt.Errorf("terminal selection matched %d targets: %s; select one, or run 'dagger shell -l' to list them", len(names), strings.Join(names, ", "))
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

// terminalType returns the type name of a node that supports a terminal:
// "Container" or "Directory". Otherwise, it returns "".
func terminalType(node *ModTreeNode) string {
	if node == nil || node.Type.Self() == nil {
		return ""
	}
	typeDef := node.Type.Self()
	if typeDef.Optional || typeDef.Kind != TypeDefKindObject || !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
		return ""
	}
	switch name := typeDef.AsObject.Value.Self().Name; name {
	case "Container", "Directory":
		return name
	default:
		return ""
	}
}

func (node *ModTreeNode) RunTerminal(ctx context.Context, setup TerminalSetupArgs) error {
	ctr, err := node.terminalContainer(ctx, setup)
	if err != nil {
		return err
	}
	var result dagql.AnyObjectResult
	return node.DagqlServer.Select(dagql.WithNonInternalTelemetry(ctx), ctr, &result,
		dagql.Selector{Field: "terminal"},
	)
}

// ExecTerminal runs the command of the terminal that RunTerminal opens, with
// args appended.
func (node *ModTreeNode) ExecTerminal(ctx context.Context, args []string, stdin string, setup TerminalSetupArgs) (res dagql.ObjectResult[*Container], _ error) {
	ctr, err := node.terminalContainer(ctx, setup)
	if err != nil {
		return res, err
	}
	srv := node.DagqlServer

	// Exec in a copy with a unique identity, so that it is never a cache hit.
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
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return res, err
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

	ctx = dagql.WithNonInternalTelemetry(ctx)
	if err := srv.Select(ctx, ctr, &res, terminalExec(ctr.Self(), args, stdin, ReturnAny)); err != nil {
		return res, err
	}
	// Run the command now, in the context of the bound workspace.
	return res, cache.Evaluate(ctx, res)
}

// terminalContainer returns the evaluated container of the terminal for node,
// after setup.
func (node *ModTreeNode) terminalContainer(ctx context.Context, setup TerminalSetupArgs) (ctr dagql.ObjectResult[*Container], _ error) {
	switch terminalType(node) {
	case "Container":
		if err := node.DagqlValue(ctx, &ctr); err != nil {
			return ctr, err
		}
	case "Directory":
		var dir dagql.ObjectResult[*Directory]
		if err := node.DagqlValue(ctx, &dir); err != nil {
			return ctr, err
		}
		var err error
		// Unlike Directory.terminal, allow changes, so that setup can
		// change the directory.
		ctr, err = dir.Self().terminalContainer(ctx, dagql.ObjectResult[*Container]{}, dir, false)
		if err != nil {
			return ctr, err
		}
	default:
		return ctr, fmt.Errorf("%q: unsupported terminal target type", node.PathString())
	}

	// Evaluate before setup: the default terminal command can be set lazily.
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return ctr, err
	}
	if err := cache.Evaluate(ctx, ctr); err != nil {
		return ctr, err
	}
	var sels []dagql.Selector
	for _, cp := range setup.Copy {
		sels = append(sels, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(cp.Value.Path)},
				{Name: "source", Value: cp.Value.Source},
			},
		})
	}
	for _, init := range setup.Init {
		sels = append(sels, terminalExec(ctr.Self(), []string{"-c", init}, "", ReturnSuccess))
	}
	if len(sels) == 0 {
		return ctr, nil
	}
	ctx = dagql.WithNonInternalTelemetry(ctx)
	if err := node.DagqlServer.Select(ctx, ctr, &ctr, sels...); err != nil {
		return ctr, err
	}
	return ctr, cache.Evaluate(ctx, ctr)
}

// terminalExec selects an exec of the terminal command of ctr, with args
// appended.
func terminalExec(ctr *Container, args []string, stdin string, expect ReturnTypes) dagql.Selector {
	term := ctr.WithTerminalDefaults(TerminalArgs{})
	cmd := append(slices.Clone(term.Cmd), args...)
	return dagql.Selector{
		Field: "withExec",
		Args: []dagql.NamedInput{
			{Name: "args", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(cmd...))},
			{Name: "stdin", Value: dagql.String(stdin)},
			{Name: "expect", Value: expect},
			{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(term.ExperimentalPrivilegedNesting.Value.Bool())},
			{Name: "insecureRootCapabilities", Value: dagql.NewBoolean(term.InsecureRootCapabilities.Value.Bool())},
		},
	}
}

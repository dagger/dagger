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

// TerminalCopy is a directory to copy into a terminal container.
type TerminalCopy struct {
	Path   string      `doc:"Location of the copied directory. A relative path is relative to the container's working directory."`
	Source DirectoryID `doc:"The directory to copy."`
}

func (TerminalCopy) TypeName() string {
	return "TerminalCopy"
}

// TerminalSetup changes a terminal container before its command runs.
type TerminalSetup struct {
	// Directories to copy into the container, in order.
	Copies []TerminalCopy
	// Commands to write, in order, to the standard input of the terminal's
	// command. Only their changes to the filesystem are kept.
	Inits []string
}

// Run opens the selected terminal target. A terminal group must contain one
// target because interactive terminals cannot run in parallel.
func (r *TerminalGroup) Run(ctx context.Context, setup TerminalSetup) error {
	target, err := r.selected()
	if err != nil {
		return err
	}
	return target.Node.RunTerminal(r.workspaceContext(ctx), setup)
}

// Exec runs the selected terminal target's command non-interactively, with
// stdin as its standard input, and returns the container after execution. Any
// exit code is allowed, so the caller can inspect it.
func (r *TerminalGroup) Exec(ctx context.Context, stdin string, setup TerminalSetup) (dagql.ObjectResult[*Container], error) {
	target, err := r.selected()
	if err != nil {
		return dagql.ObjectResult[*Container]{}, err
	}
	return target.Node.ExecTerminal(r.workspaceContext(ctx), stdin, setup)
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

func (node *ModTreeNode) RunTerminal(ctx context.Context, setup TerminalSetup) error {
	ctr, err := node.terminalContainer(ctx, setup)
	if err != nil {
		return err
	}
	var result dagql.AnyObjectResult
	return node.DagqlServer.Select(dagql.WithNonInternalTelemetry(ctx), ctr, &result,
		dagql.Selector{Field: "terminal"},
	)
}

// ExecTerminal runs the command that RunTerminal would open, in the same
// container, with stdin as its standard input instead of a terminal. Like a
// terminal, each call runs the command again.
func (node *ModTreeNode) ExecTerminal(ctx context.Context, stdin string, setup TerminalSetup) (res dagql.ObjectResult[*Container], _ error) {
	ctr, err := node.terminalContainer(ctx, setup)
	if err != nil {
		return res, err
	}
	srv := node.DagqlServer
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return res, err
	}
	if err := cache.Evaluate(ctx, ctr); err != nil {
		return res, err
	}

	// Use a new synthetic copy of the container for each call, so the exec
	// below is never a cache hit.
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
	if err := srv.Select(ctx, ctr, &res, terminalStdinExec(ctr.Self(), stdin, ReturnAny)); err != nil {
		return res, err
	}
	// Run the command now, in the context of the bound workspace.
	return res, cache.Evaluate(ctx, res)
}

// terminalContainer returns the container in which the terminal for node
// opens, after setup.
func (node *ModTreeNode) terminalContainer(ctx context.Context, setup TerminalSetup) (ctr dagql.ObjectResult[*Container], _ error) {
	if !supportsTerminal(node) {
		return ctr, fmt.Errorf("%q: unsupported terminal target type", node.PathString())
	}
	srv := node.DagqlServer

	switch node.Type.Self().AsObject.Value.Self().Name {
	case "Container":
		if err := node.DagqlValue(ctx, &ctr); err != nil {
			return ctr, err
		}
	case "Directory":
		// Match Directory.terminal: the directory in the default image.
		var dir dagql.ObjectResult[*Directory]
		if err := node.DagqlValue(ctx, &dir); err != nil {
			return ctr, err
		}
		dirID, err := dir.ID()
		if err != nil {
			return ctr, err
		}
		coreSrv := srv.Canonical()
		if err := coreSrv.Select(ctx, coreSrv.Root(), &ctr,
			dagql.Selector{
				Field: "container",
				Args:  []dagql.NamedInput{{Name: "platform", Value: dir.Self().Platform}},
			},
			dagql.Selector{
				Field: "from",
				Args:  []dagql.NamedInput{{Name: "address", Value: dagql.String(defaultTerminalImage)}},
			},
			dagql.Selector{
				Field: "withMountedDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/src")},
					{Name: "source", Value: dagql.NewID[*Directory](dirID)},
					{Name: "readOnly", Value: dagql.Boolean(true)},
				},
			},
			dagql.Selector{
				Field: "withWorkdir",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String("/src")}},
			},
		); err != nil {
			return ctr, err
		}
	}
	if len(setup.Copies) == 0 && len(setup.Inits) == 0 {
		return ctr, nil
	}

	// Evaluate first: the default terminal command can be set lazily.
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return ctr, err
	}
	if err := cache.Evaluate(ctx, ctr); err != nil {
		return ctr, err
	}
	sels := make([]dagql.Selector, 0, len(setup.Copies)+len(setup.Inits))
	for _, cp := range setup.Copies {
		sels = append(sels, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(cp.Path)},
				{Name: "source", Value: cp.Source},
			},
		})
	}
	for _, init := range setup.Inits {
		sels = append(sels, terminalStdinExec(ctr.Self(), init, ReturnSuccess))
	}
	err = srv.Select(dagql.WithNonInternalTelemetry(ctx), ctr, &ctr, sels...)
	return ctr, err
}

// terminalStdinExec selects an exec of the terminal command of ctr, with
// stdin as its standard input.
func terminalStdinExec(ctr *Container, stdin string, expect ReturnTypes) dagql.Selector {
	// Match the terminal's command, and its fallback to sh.
	defaults := ctr.DefaultTerminalCmd
	args := defaults.Args
	if len(args) == 0 {
		args = []string{"sh"}
	}
	return dagql.Selector{
		Field: "withExec",
		Args: []dagql.NamedInput{
			{Name: "args", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args...))},
			{Name: "stdin", Value: dagql.String(stdin)},
			{Name: "expect", Value: expect},
			{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(defaults.ExperimentalPrivilegedNesting.Value.Bool())},
			{Name: "insecureRootCapabilities", Value: dagql.NewBoolean(defaults.InsecureRootCapabilities.Value.Bool())},
		},
	}
}

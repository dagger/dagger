package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
)

// Check represents a validation check with its result
type Check struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the check completed"`
	Passed    bool         `field:"true" doc:"Whether the check passed"`

	Error dagql.Nullable[dagql.ObjectResult[*Error]] `field:"true" doc:"If the check failed, this is the error"`

	// IsGenerate indicates this check was derived from a +generate function.
	// When true, the check passes if the generator produces an empty changeset.
	IsGenerate bool

	// Synthetic is set when the check was derived from an engine-injected
	// generator rather than a field on a module's schema. Node then exists only
	// for naming and pattern matching, so running the check goes through the
	// SyntheticGeneratorRunner the schema package supplies. Always paired with
	// IsGenerate.
	Synthetic *SyntheticGeneratorSpec

	// LoadFailure is set on a check standing in for a workspace module that
	// could not be loaded. `dagger check` loads best-effort so the modules
	// that do load still run; the one that did not is reported as a check that
	// fails, so it can neither abort the run nor pass unnoticed.
	LoadFailure *ModuleLoadFailure
}

// moduleLoadCheckName is the leaf the load-failure check is reported under, so
// it reads as "<module>:load" everywhere checks are named. Naming it after the
// module alone would collide with a real check of that name (an entrypoint
// module's checks drop their prefix), and the frontends dedupe by check name.
const moduleLoadCheckName = "load"

// generateCheckName is the leaf a generate-derived check is reported under, so
// it reads as "<generator>:up-to-date" everywhere checks are named. The check
// confirms the generated files are up to date, and the leaf names that intent.
// An empty changeset from the generator is how the check determines it, not
// what it asserts. The suffix keeps the check from colliding with the generator
// itself, which `dagger generate` lists under the un-suffixed name. It extends
// the generator's path rather than replacing its leaf the way
// moduleLoadCheckName does, because a module may declare several +generate
// functions and "<module>:up-to-date" would collide again.
const generateCheckName = "up-to-date"

// generateCheckNode is the naming-only node of the check derived from a
// generator. Every place that names such a check goes through it, so the list
// and the run report cannot disagree.
func generateCheckNode(generator *ModTreeNode) *ModTreeNode {
	return &ModTreeNode{Parent: generator, Name: generateCheckName}
}

// NewModuleLoadFailureCheck is the always-failing check that stands in for a
// workspace module `dagger check` could not load. Its nodes are naming-only
// (the shape reparentWorkspaceTreeRoot gives a module root) because the
// module's own tree is exactly what could not be built.
func NewModuleLoadFailureCheck(failure ModuleLoadFailure) *Check {
	return &Check{
		Node: &ModTreeNode{
			Parent: &ModTreeNode{
				Parent: &ModTreeNode{},
				Name:   failure.Name,
			},
			Name: moduleLoadCheckName,
			// Summary first: consumers that render a description as a single
			// line (`dagger check -l`) would otherwise print the whole load
			// error, which the skipped-module report already carries.
			Description: "this workspace module could not be loaded\n" + failure.Message,
		},
		LoadFailure: &failure,
	}
}

type CheckGroup struct {
	Node   *ModTreeNode `json:"node"`
	Checks []*Check     `json:"checks"`

	// BoundWorkspace is the Workspace this group was rolled up from — the one
	// `Workspace.checks` was called on, including any overlay edits. Run threads
	// it into the context (WorkspaceToContext) so each check leaf's auto-injected
	// Workspace! (and any currentWorkspace read) resolves against it, rather than
	// the session's frozen current workspace. Transient (not persisted): it is
	// re-established when `checks` re-runs on replay.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

// NewCheckGroup rolls up every check of the module. It takes no include
// patterns: a generate-derived check only gets its name once it is a Check, so
// callers filter the finished checks by Check.MatchNodes instead of the tree
// nodes here, where a pattern naming such a check would match nothing.
func NewCheckGroup(ctx context.Context, mod dagql.ObjectResult[*Module], noGenerate, onlyGenerate bool) (*CheckGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	var checks []*Check
	if !onlyGenerate {
		checkNodes, err := rootNode.RollupChecks(ctx, nil, nil)
		if err != nil {
			return nil, err
		}
		checks = make([]*Check, 0, len(checkNodes))
		for _, checkNode := range checkNodes {
			checks = append(checks, &Check{Node: checkNode})
		}
	}

	if !noGenerate {
		genNodes, err := rootNode.RollupGenerator(ctx, nil, nil)
		if err != nil {
			return nil, err
		}
		// Build a set of existing check paths to avoid duplicates when a
		// function is annotated with both +check and +generate.
		checkPaths := make(map[string]struct{}, len(checks))
		for _, c := range checks {
			checkPaths[c.Name()] = struct{}{}
		}
		for _, genNode := range genNodes {
			if _, exists := checkPaths[genNode.PathString()]; !exists {
				checks = append(checks, &Check{Node: genNode, IsGenerate: true})
			}
		}
	}

	return &CheckGroup{
		Node:   rootNode,
		Checks: checks,
	}, nil
}

func (*CheckGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CheckGroup",
		NonNull:   true,
	}
}

func (r *CheckGroup) List() []*Check {
	return r.Checks
}

// Run all the checks in the group.
func (r *CheckGroup) Run(ctx context.Context, failFast bool, syntheticRunner SyntheticGeneratorRunner) (*CheckGroup, error) {
	r = r.Clone()

	// Run the checks against the workspace this group was rolled up from, so
	// overlay edits applied since the session loaded are visible to each check
	// (its auto-injected Workspace! and any currentWorkspace read resolve against
	// BoundWorkspace, not the frozen session workspace).
	if r.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, r.BoundWorkspace)
	}

	jobs := parallel.New().WithContextualTracer(true).WithFailFast(failFast)
	for _, check := range r.Checks {
		// Reset output fields, in case we're re-running
		check.Completed = false
		check.Passed = false
		jobs = jobs.WithJob(check.Name(), func(ctx context.Context) error {
			err := check.run(ctx, syntheticRunner)
			check.Completed = true
			if err != nil {
				check.Passed = false
				errObj, errErr := NewErrorFromErr(ctx, err)
				if errErr != nil {
					return fmt.Errorf("create error from %w (%T): %w", err, err, errErr)
				}
				check.Error.Value = errObj
				check.Error.Valid = true
			} else {
				check.Passed = true
			}
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CheckGroup) Report(ctx context.Context) (dagql.ObjectResult[*File], error) {
	headers := []string{"check", "type", "description", "success"}
	rows := [][]string{}
	for _, check := range r.Checks {
		rows = append(rows, []string{
			check.Name(),
			check.CheckType(),
			check.Description(),
			check.ResultEmoji(),
		})
	}
	contents := markdownTable(headers, rows...)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}

	var file dagql.ObjectResult[*File]
	err = srv.Select(ctx, srv.Root(), &file,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("checks.md")},
				{Name: "contents", Value: dagql.String(contents)},
			},
		},
	)
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	return file, nil
}

func markdownTable(headers []string, rows ...[]string) string {
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	for range headers {
		sb.WriteString("| -- ")
	}
	sb.WriteString("|\n")
	for _, row := range rows {
		sb.WriteString("|" + strings.Join(row, " | ") + " |\n")
	}
	return sb.String()
}

func (r *CheckGroup) Clone() *CheckGroup {
	cp := *r
	if cp.Node != nil {
		cp.Node = cp.Node.Clone()
	}
	cp.Checks = make([]*Check, len(r.Checks))
	for i := range cp.Checks {
		cp.Checks[i] = r.Checks[i].Clone()
	}
	return &cp
}

// Path agrees with Name: both identify the check, not the node that runs it.
func (c *Check) Path() []string {
	return c.NamingNode().Path()
}

func (c *Check) Description() string {
	return c.Node.Description
}

func (c *Check) OriginalModule() *Module {
	return c.Node.OriginalModule.Self()
}

func (*Check) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Check",
		NonNull:   true,
	}
}

func (c *Check) ResultEmoji() string {
	if c.Completed {
		if c.Passed {
			return "🟢"
		}
		return "🔴"
	}
	return ""
}

func (c *Check) Name() string {
	return c.NamingNode().CommandName()
}

// NamingNode is the canonical node a check is named by; MatchNodes adds the
// compatibility aliases patterns may still be written against. A
// generate-derived check reports under an up-to-date leaf its generator node
// does not carry, so this wraps that node the way NewModuleLoadFailureCheck
// builds its own naming-only nodes. Node itself has to stay the real generator
// node, because that is what RunGeneratorAsCheck and Generator{Node: ...}
// dispatch on.
func (c *Check) NamingNode() *ModTreeNode {
	if !c.IsGenerate {
		return c.Node
	}
	return generateCheckNode(c.Node)
}

// MatchNodes are the nodes include and skip patterns are tried against. A
// generate-derived check is listed under its up-to-date name, so that name has
// to select it. Patterns written against the generator it came from have to
// keep selecting it too: a "*" spans a single segment, so "go:*" matches the
// generator and not the check name, which is one leaf longer.
func (c *Check) MatchNodes() []*ModTreeNode {
	if !c.IsGenerate {
		return []*ModTreeNode{c.Node}
	}
	return []*ModTreeNode{c.NamingNode(), c.Node}
}

func (c *Check) CheckType() string {
	switch {
	case c.LoadFailure != nil:
		return "load"
	case c.IsGenerate:
		return "generate"
	default:
		return "check"
	}
}

func (c *Check) Clone() *Check {
	cp := *c
	cp.Node = c.Node.Clone()
	if c.Synthetic != nil {
		synthetic := *c.Synthetic
		synthetic.Path = append([]string(nil), c.Synthetic.Path...)
		cp.Synthetic = &synthetic
	}
	return &cp
}

// run dispatches the check to whatever produces its outcome.
func (c *Check) run(ctx context.Context, syntheticRunner SyntheticGeneratorRunner) error {
	switch {
	case c.LoadFailure != nil:
		return c.reportLoadFailure(ctx)
	case c.Synthetic != nil:
		return c.runSynthetic(ctx, syntheticRunner)
	case c.IsGenerate:
		return c.Node.RunGeneratorAsCheck(ctx, nil, nil)
	default:
		return c.Node.RunCheck(ctx, nil, nil)
	}
}

// runAsCheckSpan runs fn under the check span the frontends build the checks
// report from (ModTreeNode.runAsCheck emits it for a check backed by a module
// field). A check with no field behind it has to emit the span here, or the
// report never counts it and a failure reads as all-passed.
func (c *Check) runAsCheckSpan(ctx context.Context, fn func(context.Context) error) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, c.Name(),
		trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.String(telemetry.CheckNameAttr, c.Name()),
		),
	)
	defer func() {
		span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, rerr == nil))
		telemetry.EndWithCause(span, &rerr)
	}()
	return fn(ctx)
}

// runSynthetic runs an engine-injected generator and passes when it changes
// nothing. The generator is transient: it exists to reuse Generator's synthetic
// dispatch and workspace-to-changeset conversion, not to become check state.
func (c *Check) runSynthetic(ctx context.Context, syntheticRunner SyntheticGeneratorRunner) error {
	return c.runAsCheckSpan(ctx, func(ctx context.Context) error {
		generator, err := (&Generator{Node: c.Node, Synthetic: c.Synthetic}).Run(ctx, syntheticRunner)
		if err != nil {
			return err
		}
		changes, err := generator.RequireChanges(ctx, "check")
		if err != nil {
			return err
		}
		empty, err := changes.IsEmpty(ctx)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("generate function %s produced changes; run 'dagger generate %s' to apply",
				c.Node.PathString(), c.Node.PathString())
		}
		return nil
	})
}

// reportLoadFailure fails the check from the module's recorded load error.
// There is no function to run: the module is exactly what could not be loaded.
func (c *Check) reportLoadFailure(ctx context.Context) error {
	return c.runAsCheckSpan(ctx, func(context.Context) error {
		return errors.New(c.LoadFailure.Message)
	})
}

func (c *Check) Run(ctx context.Context, syntheticRunner SyntheticGeneratorRunner) (*Check, error) {
	c = c.Clone()

	err := c.run(ctx, syntheticRunner)
	c.Completed = true
	if err != nil {
		c.Passed = false
		errObj, errErr := NewErrorFromErr(ctx, err)
		if errErr != nil {
			return nil, fmt.Errorf("create error from %w (%T): %w", err, err, errErr)
		}
		c.Error.Value = errObj
		c.Error.Valid = true
	} else {
		c.Passed = true
	}
	return c, nil
}

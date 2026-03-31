package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
)

// Check represents a validation check with its result
type Check struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the check completed"`
	Passed    bool         `field:"true" doc:"Whether the check passed"`

	Error dagql.Nullable[dagql.ObjectResult[*Error]] `field:"true" doc:"If the check failed, this is the error"`
}

type CheckGroup struct {
	Node   *ModTreeNode `json:"node"`
	Checks []*Check     `json:"checks"`
}

func NewCheckGroup(ctx context.Context, mod *Module, include []string) (*CheckGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	checkNodes, err := rootNode.RollupChecks(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	checks := make([]*Check, 0, len(checkNodes))

	for _, checkNode := range checkNodes {
		checks = append(checks, &Check{Node: checkNode})
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

// Run all the checks in the group
func (r *CheckGroup) Run(ctx context.Context) (*CheckGroup, error) {
	r = r.Clone()

	jobs := parallel.New().WithContextualTracer(true)
	for _, check := range r.Checks {
		// Reset output fields, in case we're re-running
		check.Completed = false
		check.Passed = false
		jobs = jobs.WithJob(check.Name(), func(ctx context.Context) error {
			err := check.Node.RunCheck(ctx, nil, nil)
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
			// Don't propagate check failures as job errors;
			// the failure is captured in the Check itself so
			// callers can query .passed, .error, and .report.
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CheckGroup) Report(ctx context.Context) (*File, error) {
	var passed, failed, pending int
	for _, check := range r.Checks {
		switch {
		case !check.Completed:
			pending++
		case check.Passed:
			passed++
		default:
			failed++
		}
	}

	var sb strings.Builder
	total := len(r.Checks)
	fmt.Fprintf(&sb, "%d/%d checks passed", passed, total)
	if failed > 0 {
		fmt.Fprintf(&sb, ", %d failed", failed)
	}
	if pending > 0 {
		fmt.Fprintf(&sb, ", %d pending", pending)
	}
	sb.WriteString("\n")

	// List failures first with error details
	for _, check := range r.Checks {
		if !check.Completed || check.Passed {
			continue
		}
		fmt.Fprintf(&sb, "\nFAIL %s", check.Name())
		if desc := check.Description(); desc != "" {
			fmt.Fprintf(&sb, " - %s", desc)
		}
		sb.WriteString("\n")
		if check.Error.Valid {
			fmt.Fprintf(&sb, "  error: %s\n", check.Error.Value.Self().Message)
		}
	}

	// Then list passing checks
	for _, check := range r.Checks {
		if !check.Completed || !check.Passed {
			continue
		}
		fmt.Fprintf(&sb, "\nPASS %s", check.Name())
		if desc := check.Description(); desc != "" {
			fmt.Fprintf(&sb, " - %s", desc)
		}
		sb.WriteString("\n")
	}

	// Then list pending checks
	for _, check := range r.Checks {
		if check.Completed {
			continue
		}
		fmt.Fprintf(&sb, "\nPEND %s", check.Name())
		if desc := check.Description(); desc != "" {
			fmt.Fprintf(&sb, " - %s", desc)
		}
		sb.WriteString("\n")
	}

	contents := sb.String()

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var file *File
	err = srv.Select(ctx, srv.Root(), &file,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("checks.txt")},
				{Name: "contents", Value: dagql.String(contents)},
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return file, nil
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

func (c *Check) Path() []string {
	return c.Node.Path()
}

func (c *Check) Description() string {
	return c.Node.Description
}

func (c *Check) OriginalModule() *Module {
	return c.Node.OriginalModule
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
	return c.Node.PathString()
}

func (c *Check) Clone() *Check {
	cp := *c
	cp.Node = c.Node.Clone()
	return &cp
}

func (c *Check) Run(ctx context.Context) (*Check, error) {
	c = c.Clone()

	err := c.Node.RunCheck(ctx, nil, nil)
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

package core

import (
	"context"
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

	var exclude []string
	if mod.Toolchains != nil {
		for _, entry := range mod.Toolchains.Entries() {
			for _, ignorePattern := range entry.IgnoreChecks {
				exclude = append(exclude, entry.FieldName+":"+ignorePattern)
			}
		}
	}
	checkNodes, err := rootNode.RollupChecks(ctx, include, exclude)
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
			check.Passed = (err == nil)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CheckGroup) Report(ctx context.Context) (*File, error) {
	headers := []string{"check", "description", "success"}
	rows := [][]string{}
	for _, check := range r.Checks {
		rows = append(rows, []string{
			check.Name(),
			check.Description(),
			check.ResultEmoji(),
		})
	}
	contents := markdownTable(headers, rows...)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var file *File
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
		return nil, err
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
	cp.Node = cp.Node.Clone()
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
	c.Passed = (err == nil)
	return c, nil
}

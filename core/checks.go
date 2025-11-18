package core

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"dagger.io/dagger/telemetry"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Check represents a validation check with its result
type Check struct {
	Path        []string `field:"true" doc:"The path of the check within its module"`
	Description string   `field:"true" doc:"The description of the check"`
	Completed   bool     `field:"true" doc:"Whether the check completed"`
	Passed      bool     `field:"true" doc:"Whether the check passed"`
}

func (*Check) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Check",
		NonNull:   true,
	}
}

type CheckGroup struct {
	Module *Module  `json:"modules"`
	Checks []*Check `json:"checks"`
}

func (*CheckGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CheckGroup",
		NonNull:   true,
	}
}

// Return the check name matching CLI case
func (c *Check) CliName() string {
	path := c.Path
	for i := range path {
		path[i] = strcase.ToKebab(path[i])
	}
	return strings.Join(path, "/")
}

func (c *Check) GqlName() string {
	path := c.Path
	for i := range path {
		path[i] = gqlFieldName(path[i])
	}
	return strings.Join(path, "/")
}

func (c *Check) Match(include []string) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	for _, name := range []string{c.CliName(), c.GqlName()} {
		for _, pattern := range include {
			// FIXME: match against both gqlFieldCase and cliCase
			matched, err := doublestar.PathMatch(pattern, name)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *CheckGroup) List(ctx context.Context) ([]*Check, error) {
	return r.Checks, nil
}

// Run all the checks in the group
func (r *CheckGroup) Run(ctx context.Context) (*CheckGroup, error) {
	r = r.Clone()
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	dagqlCache, err := q.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("run checks for module %q: get dagql cache: %w", r.Module.Name(), err)
	}
	dag := dagql.NewServer(q, dagqlCache)
	dag.Around(AroundFunc)
	// Install default dependencies (ie the core)
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("run checks for module %q: load core schema: %w", r.Module.Name(), err)
	}
	for _, defaultDep := range defaultDeps.Mods {
		if err := defaultDep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("run checks for module %q: serve core schema: %w", r.Module.Name(), err)
		}
	}
	for _, tcMod := range r.Module.ToolchainModules {
		if err := tcMod.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("run checks for module %q: serve toolchain module %q: %w", r.Module.Name(), tcMod.Name(), err)
		}
	}
	if err := r.Module.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("run checks for module %q: serve module: %w", r.Module.Name(), err)
	}

	eg := new(errgroup.Group)
	for _, check := range r.Checks {
		ctx, span := Tracer(ctx).Start(ctx, check.Name(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.CheckNameAttr, check.Name()),
			),
		)
		eg.Go(func() (rerr error) {
			defer telemetry.EndWithCause(span, &rerr)
			// Reset output fields, in case we're re-running
			check.Completed = false
			check.Passed = false
			defer func() {
				check.Completed = true
				check.Passed = rerr == nil
				// Set the passed attribute on the span for telemetry
				span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, check.Passed))
			}()

			selectPath := []dagql.Selector{{Field: gqlFieldName(r.Module.Name())}}
			for _, field := range check.Path {
				selectPath = append(selectPath, dagql.Selector{Field: field})
			}

			var checkParent dagql.AnyObjectResult
			if err := (func() (rerr error) {
				ctx, span := Tracer(ctx).Start(ctx, "load check context",
					// Prevent logs from bubbling up past this point.
					telemetry.Boundary(),
					// We're only using this span as a log encapsulation boundary; show
					// its child spans inline.
					telemetry.Passthrough(),
				)

				defer telemetry.EndWithCause(span, &rerr)
				return dag.Select(ctx, dag.Root(), &checkParent, selectPath[:len(selectPath)-1]...)
			})(); err != nil {
				return err
			}

			// Call the check
			var status dagql.AnyResult
			if err := dag.Select(dagql.WithRepeatedTelemetry(ctx), checkParent, &status, selectPath[len(selectPath)-1]); err != nil {
				return err
			}

			if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status); ok {
				// If the check returns a syncable type, sync it
				if syncField, has := obj.ObjectType().FieldSpec("sync", dag.View); has {
					if !syncField.Args.HasRequired(dag.View) {
						if err := dag.Select(
							dagql.WithRepeatedTelemetry(ctx),
							obj,
							&status,
							dagql.Selector{Field: "sync"},
						); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
	}
	// We can't distinguish legitimate errors from failed checks, so we just discard.
	// Bubbling them up to here makes telemetry more useful (no green when a check failed)
	_ = eg.Wait()
	return r, nil
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

func (r *CheckGroup) Report(ctx context.Context) (*File, error) {
	headers := []string{"check", "description", "success", "message"}
	rows := [][]string{}
	for _, check := range r.Checks {
		rows = append(rows, []string{
			check.Name(),
			check.Description,
			check.ResultEmoji(),
		})
	}
	contents := []byte(markdownTable(headers, rows...))
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return NewFileWithContents(ctx, "checks.md", contents, fs.FileMode(0644), nil, q.Platform())
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
	for i := range cp.Checks {
		cp.Checks[i] = cp.Checks[i].Clone()
	}
	return &cp
}

func (c *Check) Name() string {
	return strings.Join(c.Path, "/")
}

func (c *Check) Clone() *Check {
	cp := *c
	return &cp
}

func (c *Check) Query() []dagql.Selector {
	var q []dagql.Selector
	for _, field := range c.Path {
		q = append(q, dagql.Selector{Field: gqlFieldName(field)})
	}
	return q
}

package core

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"dagger.io/dagger/telemetry"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Check represents a validation check with its result
type Check struct {
	Path        []string `field:"true" doc:"The path of the check within its module"`
	Description string   `field:"true" doc:"The description of the check"`
	Completed   bool     `field:"true" doc:"Whether the check completed"`
	Passed      bool     `field:"true" doc:"Whether the check passed"`
	Module      *Module
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

	dag, err := dagForCheck(ctx, r.Module)
	if err != nil {
		return nil, err
	}

	eg := new(errgroup.Group)
	for _, check := range r.Checks {
		if ok, err := check.tryScaleOut(ctx, eg); err != nil {
			// TODO: currently err is always nil, but if it wasn't
			// then we should probably either cancel existing runs
			// that started or wait for them to finish before returning
			return nil, err
		} else if ok {
			continue
		}

		if err := check.startRun(ctx, dag, eg, false); err != nil {
			return nil, err
		}
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

func (c *Check) Run(ctx context.Context, skipCheckSpan bool) (*Check, error) {
	c = c.Clone()

	dag, err := dagForCheck(ctx, c.Module)
	if err != nil {
		return nil, err
	}
	eg := new(errgroup.Group)
	err = c.startRun(ctx, dag, eg, skipCheckSpan)
	if err != nil {
		return nil, err
	}
	// We can't distinguish legitimate errors from failed checks, so we just discard.
	// Bubbling them up to here makes telemetry more useful (no green when a check failed)
	_ = eg.Wait()
	return c, nil
}

// mutates Check, be sure to clone first if needed
func (c *Check) startRun(ctx context.Context, dag *dagql.Server, eg *errgroup.Group, skipCheckSpan bool) error {
	var span trace.Span
	if !skipCheckSpan {
		ctx, span = Tracer(ctx).Start(ctx, c.Name(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.CheckNameAttr, c.Name()),
			),
		)
	}
	eg.Go(func() (rerr error) {
		if span != nil {
			defer telemetry.End(span, func() error { return rerr })
		}

		// Reset output fields, in case we're re-running
		c.Completed = false
		c.Passed = false
		defer func() {
			c.Completed = true
			c.Passed = rerr == nil
			// Set the passed attribute on the span for telemetry
			if span != nil {
				span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, c.Passed))
			}
		}()

		selectPath := []dagql.Selector{{Field: gqlFieldName(c.Module.Name())}}
		for _, field := range c.Path {
			selectPath = append(selectPath, dagql.Selector{Field: field})
		}

		var checkParent dagql.AnyObjectResult
		if err := (func(ctx context.Context) (rerr error) {
			ctx, span := Tracer(ctx).Start(ctx, "load check context",
				// Prevent logs from bubbling up past this point.
				telemetry.Boundary(),
				// We're only using this span as a log encapsulation boundary; show
				// its child spans inline.
				telemetry.Passthrough(),
			)
			defer telemetry.End(span, func() error { return rerr })
			return dag.Select(ctx, dag.Root(), &checkParent, selectPath[:len(selectPath)-1]...)
		})(ctx); err != nil {
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

	return nil
}

// mutates Check, be sure to clone first if needed
func (c *Check) tryScaleOut(ctx context.Context, eg *errgroup.Group) (bool, error) {
	log := slog.SpanLogger(ctx, "scale-out")

	if c.Module.Source.Value.Self().Kind == ModuleSourceKindDir {
		// TODO: if the dir is transferable, can construct an ID in the remote engine and use it here, probably...
		// In mean time just don't scale out
		log.Info("skipping scale out for dir-based module")
		return false, nil
	}

	// TODO: ask cloud whether to scale out, if not return (false, nil) to continue locally

	ctx, span := Tracer(ctx).Start(ctx, c.Name(),
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.String(telemetry.CheckNameAttr, c.Name()),
		),
	)
	eg.Go(func() (rerr error) {
		defer telemetry.End(span, func() error { return rerr })

		var res struct {
			Completed bool
			Passed    bool
		}
		defer func() {
			c.Completed = true
			if rerr != nil {
				c.Passed = false
			} else {
				c.Completed = res.Completed
				c.Passed = res.Passed
			}
			// Set the passed attribute on the span for telemetry
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, c.Passed))
		}()

		q, err := CurrentQuery(ctx)
		if err != nil {
			return fmt.Errorf("current query: %w", err)
		}
		grpcCaller, err := q.NonModuleParentClientSessionCaller(ctx) // TODO: rewrite to just SessionCaller, or just use the bk client, etc.
		if err != nil {
			return fmt.Errorf("get session caller: %w", err)
		}
		callerClientMD, err := q.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return fmt.Errorf("main client metadata: %w", err)
		}
		callerCtx := engine.ContextWithClientMetadata(ctx, callerClientMD)
		spanExporter, err := q.CurrentSpanExporter(callerCtx)
		if err != nil {
			return fmt.Errorf("current span exporter: %w", err)
		}
		logExporter, err := q.CurrentLogExporter(callerCtx)
		if err != nil {
			return fmt.Errorf("current log exporter: %w", err)
		}
		metricExporter, err := q.CurrentMetricsExporter(callerCtx)
		if err != nil {
			return fmt.Errorf("current metric exporter: %w", err)
		}

		engineClient, _, err := client.ConnectE2E(ctx, client.Params{
			RunnerHost: "dagger-cloud://default-engine-config.dagger.cloud",
			// RunnerHost: "unix:///var/run/dagger/engine.sock",

			Module:   c.Module.Source.Value.Self().AsString(),
			Function: c.Name(), // TODO: ? not really a function technically

			CloudToken:      callerClientMD.CloudToken,
			CloudBasicToken: callerClientMD.CloudBasicToken,
			CloudOrgID:      callerClientMD.CloudOrg,

			EngineTrace:   spanExporter,
			EngineLogs:    logExporter,
			EngineMetrics: []sdkmetric.Exporter{metricExporter},

			ExistingSessionConn: grpcCaller.Conn(),
		})
		if err != nil {
			return fmt.Errorf("e2e connect: %w", err)
		}
		defer func() {
			err := engineClient.Close()
			if err != nil && rerr == nil {
				rerr = fmt.Errorf("close client: %w", err)
			}
		}()

		query := engineClient.Dagger().QueryBuilder()

		switch c.Module.Source.Value.Self().Kind {
		case ModuleSourceKindLocal:
			query = query.Select("moduleSource").
				Arg("refString", filepath.Join(
					c.Module.Source.Value.Self().Local.ContextDirectoryPath,
					c.Module.Source.Value.Self().SourceRootSubpath,
				))
		case ModuleSourceKindGit:
			query = query.Select("moduleSource").
				Arg("refString", c.Module.Source.Value.Self().Git.Symbolic). // XXX: ain't right
				Arg("refPin", c.Module.Source.Value.Self().Git.Commit).
				Arg("requireKind", c.Module.Source.Value.Self().Kind)
		case ModuleSourceKindDir:
			// TODO: shouldn't happen yet based on check at beginning
			return fmt.Errorf("cannot scale out checks for dir-based modules")
		}
		query = query.Select("asModule")

		query = query.Select("check").
			Arg("name", c.Name())

		query = query.Select("run").
			Arg("skipCheckSpan", true)

		query = query.SelectMultiple("completed", "passed")

		err = query.Bind(&res).Execute(ctx)
		if err != nil {
			return err
		}

		return nil
	})

	return true, nil
}

func dagForCheck(ctx context.Context, mod *Module) (*dagql.Server, error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	dagqlCache, err := q.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("run checks for module %q: get dagql cache: %w", mod.Name(), err)
	}
	dag := dagql.NewServer(q, dagqlCache)
	dag.Around(AroundFunc)
	// Install default dependencies (ie the core)
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("run checks for module %q: load core schema: %w", mod.Name(), err)
	}
	for _, defaultDep := range defaultDeps.Mods {
		if err := defaultDep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("run checks for module %q: serve core schema: %w", mod.Name(), err)
		}
	}
	for _, tcMod := range mod.ToolchainModules {
		if err := tcMod.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("run checks for module %q: serve toolchain module %q: %w", mod.Name(), tcMod.Name(), err)
		}
	}
	if err := mod.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("run checks for module %q: serve module: %w", mod.Name(), err)
	}

	return dag, nil
}

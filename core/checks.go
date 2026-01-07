package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger/telemetry"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Check represents a validation check with its result
type Check struct {
	Path        []string      `field:"true" doc:"The path of the check within its module"`
	Description string        `field:"true" doc:"The description of the check"`
	Completed   bool          `field:"true" doc:"Whether the check completed"`
	Passed      bool          `field:"true" doc:"Whether the check passed"`
	Source      *ModuleSource `field:"true" doc:"The module source where the check is defined (i.e., toolchains)"`
	Module      *Module       `field:"false" doc:"The module where the check is run"`
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
			if match, err := fnPathContains(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			if match, err := fnPathGlob(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			pattern = strings.ReplaceAll(pattern, ":", "/")
			name = strings.ReplaceAll(name, ":", "/")
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

// Match a function-path against a glob pattern
func fnPathGlob(pattern, target string) (bool, error) {
	// FIXME: match against both gqlFieldCase and cliCase
	slashPattern := strings.ReplaceAll(pattern, ":", "/")
	slashTarget := strings.ReplaceAll(target, ":", "/")
	return doublestar.PathMatch(slashPattern, slashTarget)
}

// Check if a function-path contains another
func fnPathContains(base, target string) (bool, error) {
	// FIXME: match against both gqlFieldCase and cliCase
	slashTarget := path.Clean("/" + strings.ReplaceAll(target, ":", "/"))
	slashBase := path.Clean("/" + strings.ReplaceAll(base, ":", "/"))
	rel, err := filepath.Rel(slashBase, slashTarget)
	if err != nil {
		return false, err
	}
	return !strings.HasPrefix(rel, ".."), nil
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
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
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
		// Reset output fields, in case we're re-running
		check.Completed = false
		check.Passed = false
		eg.Go(func() (rerr error) {
			defer func() {
				check.Completed = true
				check.Passed = rerr == nil
				// Set the passed attribute on the span for telemetry
				span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, check.Passed))
				telemetry.EndWithCause(span, &rerr)
			}()
			return check.run(ctx, dag, clientMD.EnableCloudScaleOut)
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
	cp.Module = r.Module.Clone()
	return &cp
}

func (c *Check) Name() string {
	return strings.Join(c.Path, ":")
}

func (c *Check) Clone() *Check {
	cp := *c
	cp.Module = c.Module.Clone()
	cp.Source = c.Source.Clone()
	return &cp
}

func (c *Check) Query() []dagql.Selector {
	var q []dagql.Selector
	for _, field := range c.Path {
		q = append(q, dagql.Selector{Field: gqlFieldName(field)})
	}
	return q
}

func (c *Check) Run(ctx context.Context) (*Check, error) {
	c = c.Clone()

	dag, err := dagForCheck(ctx, c.Module)
	if err != nil {
		return nil, err
	}

	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var span trace.Span
	if clientMD.CloudScaleOutEngineID != "" { // don't dupe telemetry if the client is an engine scaling out to us
		ctx, span = Tracer(ctx).Start(ctx, c.Name(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.CheckNameAttr, c.Name()),
			),
		)
	}

	// Reset output fields, in case we're re-running
	c.Completed = false
	c.Passed = false

	var checkErr error
	defer func() {
		c.Completed = true
		c.Passed = checkErr == nil

		if span != nil {
			// Set the passed attribute on the span for telemetry
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, c.Passed))
			telemetry.EndWithCause(span, &checkErr)
		}
	}()
	checkErr = c.run(ctx, dag, false)

	// We can't distinguish legitimate errors from failed checks, so we just discard.
	// Bubbling them up to here makes telemetry more useful (no green when a check failed)
	return c, nil
}

func (c *Check) run(
	ctx context.Context,
	dag *dagql.Server,
	tryScaleOut bool,
) (rerr error) {
	if tryScaleOut {
		if ok, err := c.tryScaleOut(ctx); ok {
			return err
		}
	}

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
		defer telemetry.EndWithCause(span, &rerr)
		return dag.Select(ctx, dag.Root(), &checkParent, selectPath[:len(selectPath)-1]...)
	})(ctx); err != nil {
		return err
	}

	// Call the check
	var status dagql.AnyResult
	if err := dag.Select(dagql.WithNonInternalTelemetry(ctx), checkParent, &status, selectPath[len(selectPath)-1]); err != nil {
		return err
	}

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status); ok {
		// If the check returns a syncable type, sync it
		if syncField, has := obj.ObjectType().FieldSpec("sync", dag.View); has {
			if !syncField.Args.HasRequired(dag.View) {
				if err := dag.Select(
					dagql.WithNonInternalTelemetry(ctx),
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
}

func (c *Check) tryScaleOut(ctx context.Context) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}

	cloudEngineClient, useCloudEngine, err := q.CloudEngineClient(ctx,
		c.Module.Source.Value.Self().AsString(),
		// FIXME: we're saying the "function" is the check and no execCmd,
		// which works with cloud but is weird
		c.Name(),
		nil,
	)
	if err != nil {
		return true, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloudEngine {
		// just run locally
		return false, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudEngineClient.Close())
	}()

	query := cloudEngineClient.Dagger().QueryBuilder()

	//
	// construct a query to run this check on the cloud engine
	//

	// load the module, depending on its kind
	switch c.Module.Source.Value.Self().Kind {
	case ModuleSourceKindLocal:
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				c.Module.Source.Value.Self().Local.ContextDirectoryPath,
				c.Module.Source.Value.Self().SourceRootSubpath,
			))
	case ModuleSourceKindGit:
		query = query.Select("moduleSource").
			Arg("refString", c.Module.Source.Value.Self().AsString()).
			Arg("refPin", c.Module.Source.Value.Self().Git.Commit).
			Arg("requireKind", c.Module.Source.Value.Self().Kind)
	case ModuleSourceKindDir:
		// FIXME: whether this actually works or not depends on whether the dir is reproducible. For simplicity,
		// we just assume it is and will error out later if not. Would be better to explicitly check though.
		dirID := c.Module.Source.Value.Self().DirSrc.OriginalContextDir.ID()
		dirIDEnc, err := dirID.Encode()
		if err != nil {
			return true, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").
			Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", c.Module.Source.Value.Self().DirSrc.OriginalSourceRootSubpath)
	}
	query = query.Select("asModule")

	// run the check

	query = query.Select("check").
		Arg("name", c.Name())

	query = query.Select("run")

	query = query.SelectMultiple("completed", "passed")

	// execute the query against the remote engine

	var res struct {
		Completed bool
		Passed    bool
	}
	err = query.Bind(&res).Execute(ctx)
	if err != nil {
		return true, err
	}

	return true, nil
}

// Prepare a dagql.Server for running checks on the given module
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

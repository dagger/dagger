package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Check represents a validation check with its result
type Check struct {
	Node      *FnTreeNode `json:"node"`
	Completed bool        `field:"true" doc:"Whether the check completed"`
	Passed    bool        `field:"true" doc:"Whether the check passed"`
}

type CheckGroup struct {
	Node   *FnTreeNode `json:"node"`
	Checks []*Check    `json:"checks"`
}

func NewCheckGroup(ctx context.Context, mod *Module, include []string) (*CheckGroup, error) {
	rootNode, err := NewFnTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	var exclude []string
	for toolchainName, toolchainIgnorePatterns := range mod.ToolchainIgnoreChecks {
		for _, ignorePattern := range toolchainIgnorePatterns {
			exclude = append(exclude, toolchainName+":"+ignorePattern)
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

	dag := r.Node.DagqlServer
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
	cp.Node = cp.Node.Clone()
	for i := range cp.Checks {
		cp.Checks[i] = cp.Checks[i].Clone()
	}
	return &cp
}

func (c *Check) Path() []string {
	return c.Node.Path
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
	return c.Node.Name()
}

func (c *Check) Clone() *Check {
	cp := *c
	cp.Node = c.Node.Clone()
	return &cp
}

func (c *Check) Run(ctx context.Context) (*Check, error) {
	c = c.Clone()

	dag := c.Node.DagqlServer
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
	// FIXME: re-implement the "tuck away" trick using 2 distinct selects
	var status dagql.AnyResult
	if err := c.Node.DagqlValue(ctx, &status); err != nil {
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
		c.Node.RootAddress(),
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
	mod := c.Node.DagqlRoot
	switch mod.Source.Value.Self().Kind {
	case ModuleSourceKindLocal:
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				mod.Source.Value.Self().Local.ContextDirectoryPath,
				mod.Source.Value.Self().SourceRootSubpath,
			))
	case ModuleSourceKindGit:
		query = query.Select("moduleSource").
			Arg("refString", mod.Source.Value.Self().AsString()).
			Arg("refPin", mod.Source.Value.Self().Git.Commit).
			Arg("requireKind", mod.Source.Value.Self().Kind)
	case ModuleSourceKindDir:
		// FIXME: whether this actually works or not depends on whether the dir is reproducible. For simplicity,
		// we just assume it is and will error out later if not. Would be better to explicitly check though.
		dirID := mod.Source.Value.Self().DirSrc.OriginalContextDir.ID()
		dirIDEnc, err := dirID.Encode()
		if err != nil {
			return true, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").
			Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", mod.Source.Value.Self().DirSrc.OriginalSourceRootSubpath)
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

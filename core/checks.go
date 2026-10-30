package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// Check is a deferred assertion and its outcome.
type Check struct {
	Assertion       dagql.Nullable[dagql.String]                   `field:"true" doc:"The assertion that is false when this check fails."`
	Report          dagql.Nullable[dagql.ObjectResult[*Directory]] `field:"true" doc:"An optional report produced by the check."`
	RemoteArtifact  dagql.ObjectResult[*Artifact]
	RemoteArguments JSON
	Unaddressed     dagql.ObjectResult[*Check]
	Receiver        dagql.ObjectResult[*ModuleObject]
	Workspace       dagql.ObjectResult[*Workspace]
	Function        string
	Address         string
	Inputs          []CallInput
	Generator       dagql.ObjectResult[*Generator]
	CachePolicy     FunctionCachePolicy
	CacheTTL        int64
	Failure         string
	Completed       bool
	Passed          bool
	Error           dagql.Nullable[dagql.ObjectResult[*Error]]
	StaleGenerator  string
	StalePatch      string
}

func (c *Check) ResultScope() dagql.ResultScope {
	if c.Receiver.Self() != nil {
		return c.Receiver.Self().ResultScope()
	}
	return nil
}

func (*Check) Type() *ast.Type { return &ast.Type{NamedType: "Check", NonNull: true} }
func (*Check) TypeDescription() string {
	return "One deferred check. Reading pass, error, or sync runs it."
}

func (c *Check) Clone() *Check {
	copy := *c
	return &copy
}

func (c *Check) Run(ctx context.Context) (*Check, error) {
	c = c.Clone()
	if c.Completed {
		return c, nil
	}
	var failure error
	switch {
	case c.RemoteArtifact.Self() != nil:
		failure = c.runRemote(ctx)
	case c.Failure != "":
		failure = fmt.Errorf("%s", c.Failure)
	case c.Receiver.Self() != nil:
		failure = c.runFunction(ctx)
	case c.Generator.Self() != nil:
		var changes dagql.ObjectResult[*Changeset]
		var srv *dagql.Server
		srv, failure = CurrentDagqlServer(ctx)
		if failure == nil {
			failure = srv.Select(ctx, c.Generator, &changes, dagql.Selector{Field: "changeset"})
		}
		if failure == nil {
			var empty bool
			empty, failure = changes.Self().IsEmpty(ctx)
			if failure == nil && !empty {
				// Stale: running the generator is what fixes this check. Not
				// when the generator itself failed, which it would just repeat.
				if uri, err := c.Generator.Self().Artifact.URI(ArtifactURIOpts{DimensionKeys: true}); err == nil {
					c.StaleGenerator = uri
				}
				c.StalePatch = summarizePatch(ctx, srv, changes, stalePatchLimits)
				failure = fmt.Errorf("generated files are not up to date")
			}
		}
	default:
		failure = fmt.Errorf("check has no assertion")
	}
	c.Completed, c.Passed = true, failure == nil
	if failure != nil {
		errObj, err := NewErrorFromErr(ctx, failure)
		if err != nil {
			return nil, err
		}
		c.Error = dagql.NonNull(errObj)
	}
	return c, nil
}

// ReportResult emits the retained outcome on this execution request's span.
// The cached __run call can have a different trace context. Keep its bounded
// patch text, rather than the changeset or workspace, for this report.
func (c *Check) ReportResult(ctx context.Context) {
	if c.StaleGenerator != "" {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String(telemetryattrs.CheckGeneratorAttr, c.StaleGenerator))
	}
	preview := c.StalePatch
	if preview == "" || ctx.Err() != nil {
		// Canceled (e.g. by fail-fast): the preview is at best a warning about
		// the cancellation, not the changes.
		return
	}
	if !strings.HasSuffix(preview, "\n") {
		preview += "\n"
	}
	var attrs []log.KeyValue
	if contentType := toolResultContentType(preview); contentType != "" {
		attrs = append(attrs, log.String(telemetry.ContentTypeAttr, contentType))
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, attrs...)
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, preview)
}

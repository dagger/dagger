package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
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
	CollectionItem  bool
	Inputs          []CallInput
	Generator       dagql.ObjectResult[*Generator]
	CacheTTL        int64
	Failure         string
	Completed       bool
	Passed          bool
	Error           dagql.Nullable[dagql.ObjectResult[*Error]]
	StaleGenerator  string
	StalePatch      string

	// Planned marks a check function's projection: a check whose function
	// hasn't run. The result is lazy (see LazyEvalFunc): forcing it, as
	// running the check does, runs the function through the cache's lazy
	// evaluation, so its work is reported as deferred work of the call that
	// planned it.
	Planned bool
	// ran is shared by the copies of a planned check: the function ran to
	// success, so nothing is deferred anymore.
	ran *atomic.Bool
}

// LazyEvalFunc runs a planned check's function. Its failure fails the
// evaluation: a failed attempt isn't consumed, so the next request to run the
// check runs it again, while requests that joined it share its failure.
func (c *Check) LazyEvalFunc() dagql.LazyEvalFunc {
	if c == nil || !c.Planned || c.Completed || c.ran == nil || c.ran.Load() {
		return nil
	}
	return func(ctx context.Context) error {
		if err := c.runFunction(ctx); err != nil {
			return err
		}
		c.ran.Store(true)
		return nil
	}
}

// NewPlannedCheck returns a check of a function that runs when the check's
// result is evaluated.
func NewPlannedCheck(c *Check) *Check {
	c.Planned = true
	c.ran = new(atomic.Bool)
	return c
}

// RunPlanned runs a planned check by evaluating its result, and returns its
// outcome.
func RunPlanned(ctx context.Context, planned dagql.ObjectResult[*Check]) (*Check, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	failure := cache.Evaluate(ctx, planned)
	c := planned.Self().Clone()
	c.Planned, c.ran = false, nil
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
			// Running the generator is the check's work, not engine
			// machinery: keep its span, and the function call beneath it,
			// visible.
			failure = srv.Select(dagql.WithNonInternalTelemetry(ctx), c.Generator, &changes, dagql.Selector{Field: "changeset"})
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
func (c *Check) ReportResult(ctx context.Context, cached bool) {
	if c.StaleGenerator != "" {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String(telemetryattrs.CheckGeneratorAttr, c.StaleGenerator))
	}
	if cached && c.Error.Valid && ctx.Err() == nil {
		// A fresh run's exec output rolls up into this span. A cached failure
		// has no exec spans here; replay the output its error retained.
		reportErrorOutput(ctx, c.Error.Value.Self())
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
	var attrs []attribute.KeyValue
	if contentType := toolResultContentType(preview); contentType != "" {
		attrs = append(attrs, attribute.String(telemetry.ContentTypeAttr, contentType))
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary, attrs...)
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, preview)
}

func reportErrorOutput(ctx context.Context, failure *Error) {
	var stdout, stderr string
	for _, value := range failure.Values {
		var output string
		if (value.Name != "stdout" && value.Name != "stderr") || json.Unmarshal(value.Value, &output) != nil {
			continue
		}
		if value.Name == "stdout" {
			stdout = output
		} else {
			stderr = output
		}
	}
	if stdout == "" && stderr == "" {
		return
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, stdout)
	fmt.Fprint(stdio.Stderr, stderr)
}

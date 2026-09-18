package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type checkNameKey struct{}

// WithCheckName supplies an artifact address for check telemetry without
// changing the check's execution identity.
func WithCheckName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, checkNameKey{}, name)
}

// Check is a deferred assertion and its outcome.
type Check struct {
	Assertion       dagql.Nullable[dagql.String]                   `field:"true" doc:"The assertion that is false when this check fails."`
	Report          dagql.Nullable[dagql.ObjectResult[*Directory]] `field:"true" doc:"An optional report produced by the check."`
	RemoteArtifact  *Artifact
	RemoteArguments JSON
	Receiver        dagql.ObjectResult[*ModuleObject]
	Workspace       dagql.ObjectResult[*Workspace]
	Function        string
	Inputs          []CallInput
	Changeset       dagql.ObjectResult[*Changeset]
	CacheTTL        int64
	Failure         string
	Completed       bool
	Passed          bool
	Error           dagql.Nullable[dagql.ObjectResult[*Error]]
}

func (*Check) Type() *ast.Type { return &ast.Type{NamedType: "Check", NonNull: true} }
func (*Check) TypeDescription() string {
	return "One deferred check. Reading pass, error, or sync runs it."
}
func (c *Check) Clone() *Check {
	copy := *c
	if c.RemoteArtifact != nil {
		copy.RemoteArtifact = c.RemoteArtifact.Clone()
	}
	return &copy
}

func (c *Check) Run(ctx context.Context) (_ *Check, rerr error) {
	c = c.Clone()
	if c.Completed {
		return c, nil
	}
	var failure error
	if c.RemoteArtifact == nil {
		name, _ := ctx.Value(checkNameKey{}).(string)
		if name == "" {
			name = c.Function
		}
		if name == "" {
			name = "changeset stale"
		}
		var span trace.Span
		ctx, span = Tracer(ctx).Start(ctx, name, trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.String(telemetry.CheckNameAttr, name),
		))
		defer func() {
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, failure == nil))
			telemetry.EndWithCause(span, &failure)
		}()
	}
	switch {
	case c.RemoteArtifact != nil:
		failure = c.runRemote(ctx)
	case c.Failure != "":
		failure = fmt.Errorf("%s", c.Failure)
	case c.Receiver.Self() != nil:
		failure = c.runFunction(ctx)
	case c.Changeset.Self() != nil:
		var empty bool
		empty, failure = c.Changeset.Self().IsEmpty(ctx)
		if failure == nil && !empty {
			failure = fmt.Errorf("generated files are not up to date")
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

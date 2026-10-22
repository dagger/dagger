package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type checksSchema struct{}

func (s checksSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.Check](srv).View(AfterVersion("v1.0.0-0")))
	run := dagql.NodeFuncWithDynamicInputs("__run", s.run, s.cachePolicy)
	run.Spec.NoTelemetry = true
	dagql.Fields[*core.Check]{
		dagql.NodeFunc("__withAddress", func(_ context.Context, check dagql.ObjectResult[*core.Check], args struct{ Address string }) (*core.Check, error) {
			copy := check.Self().Clone()
			copy.Address = args.Address
			if copy.Unaddressed.Self() == nil {
				copy.Unaddressed = check
			}
			return copy, nil
		}),
		dagql.NodeFunc("sync", s.sync).DoNotCache("Report each check execution request.").Doc("Run the check and retain its result."),
		run,
		dagql.NodeFunc("pass", s.pass).DoNotCache("Read the current check result.").Doc("Run the check and return whether it passes."),
		dagql.NodeFunc("error", s.error).DoNotCache("Read the current check result.").Doc("Run the check and return its failure, if any."),
	}.Install(srv)
}
func (s checksSchema) cachePolicy(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}, req *dagql.CallRequest) error {
	req.TTL = check.Self().CacheTTL
	req.DoNotCache = check.Self().Completed
	if !check.Self().Completed && (check.Self().RemoteArtifact.Self() != nil || check.Self().Generator.Self() != nil) {
		input, err := dagql.PerCallInput.Resolver(ctx, nil)
		if err != nil {
			return err
		}
		return req.SetImplicitInput(ctx, dagql.PerCallInput.Name, input)
	}
	return nil
}
func (s checksSchema) sync(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}) (dagql.ObjectResult[*core.Check], error) {
	if check.Self().Completed {
		return check, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Check]{}, err
	}
	var failure error
	if check.Self().RemoteArtifact.Self() == nil {
		name := check.Self().Address
		if name == "" {
			name = check.Self().Function
		}
		if name == "" {
			name = "generator stale"
		}
		var span trace.Span
		ctx, span = core.Tracer(ctx).Start(ctx, name, trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.String(telemetry.CheckNameAttr, name),
		))
		defer func() {
			span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, failure == nil))
			telemetry.EndWithCause(span, &failure)
		}()
	}
	var result dagql.ObjectResult[*core.Check]
	target := check
	if check.Self().Unaddressed.Self() != nil {
		target = check.Self().Unaddressed
	}
	if failure = srv.Select(ctx, target, &result, dagql.Selector{Field: "__run"}); failure != nil {
		return result, failure
	}
	if result.Self().Error.Valid {
		failure = result.Self().Error.Value.Self()
	}
	return result, nil
}

func (s checksSchema) run(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}) (dagql.ObjectResult[*core.Check], error) {
	result, err := check.Self().Run(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Check]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Check]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, result)
}
func (s checksSchema) result(ctx context.Context, check dagql.ObjectResult[*core.Check]) (*core.Check, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var result dagql.ObjectResult[*core.Check]
	if err := srv.Select(ctx, check, &result, dagql.Selector{Field: "sync"}); err != nil {
		return nil, err
	}
	return result.Self(), nil
}
func (s checksSchema) pass(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}) (bool, error) {
	result, err := s.result(ctx, check)
	if err != nil {
		return false, err
	}
	return result.Passed, nil
}
func (s checksSchema) error(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}) (dagql.Nullable[dagql.ObjectResult[*core.Error]], error) {
	result, err := s.result(ctx, check)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[*core.Error]](), err
	}
	return result.Error, nil
}

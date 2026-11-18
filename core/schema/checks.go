package schema

import (
	"context"
	"errors"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type checksSchema struct{}

// First API version with collection reporting metadata. Keep this boundary
// fixed once released so older callers retain their original schema.
const checkCollectionMetadataVersion = "v1.0.0-beta.26"

func (s checksSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.Check](srv).View(AfterVersion("v1.0.0-0")))
	run := keptFromContent(dagql.NodeFuncWithDynamicInputs("__run", s.run, s.cachePolicy), checkFromContent)
	run.Spec.NoTelemetry = true
	dagql.Fields[*core.Check]{
		keptFromContent(dagql.NodeFunc("__withAddress", func(_ context.Context, check dagql.ObjectResult[*core.Check], args struct {
			Address        string
			CollectionItem bool `default:"false"`
		}) (*core.Check, error) {
			copy := check.Self().Clone()
			copy.Address = args.Address
			// Keep the artifact's identity separate from the cached check body.
			copy.CollectionItem = args.CollectionItem
			if copy.Unaddressed.Self() == nil {
				copy.Unaddressed = check
			}
			return copy, nil
		}).View(AllVersion).Args(
			dagql.Arg("collectionItem").View(AfterVersion(checkCollectionMetadataVersion)),
		), checkFromContent),
		dagql.NodeFunc("sync", s.sync).DoNotCache("Report each check execution request.").Doc("Run the check and retain its result."),
		run,
		dagql.NodeFunc("pass", s.pass).DoNotCache("Read the current check result.").Doc("Run the check and return whether it passes."),
		dagql.NodeFunc("error", s.error).DoNotCache("Read the current check result.").Doc("Run the check and return its failure, if any."),
	}.Install(srv)
}
func (s checksSchema) cachePolicy(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}, req *dagql.CallRequest) error {
	req.TTL = check.Self().CacheTTL
	req.DoNotCache = check.Self().Completed
	if check.Self().Completed {
		return nil
	}
	// The check object carries its function's cache policy. Remote checks
	// and generator stale checks report fresh state on every run.
	if check.Self().RemoteArtifact.Self() == nil && check.Self().Generator.Self() == nil {
		return nil
	}
	input := dagql.PerCallInput
	value, err := input.Resolver(ctx, nil)
	if err != nil {
		return err
	}
	return req.SetImplicitInput(ctx, input.Name, value)
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
			attribute.Bool(telemetryattrs.CheckCollectionItemAttr, check.Self().CollectionItem),
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
	request := &checkRequest{}
	failure = srv.Select(context.WithValue(ctx, checkRequestKey{}, request), target, &result, dagql.Selector{Field: "__run"})
	var cached bool
	var failed *checkFailed
	if errors.As(failure, &failed) {
		result, failure = dagql.NewObjectResultForCurrentCall(ctx, srv, failed.check)
		// A request that joined another's run has none of its exec spans.
		cached = failed.request != request
	} else if failure == nil {
		cached = result.HitCache()
	}
	if failure != nil {
		return result, failure
	}
	result.Self().ReportResult(ctx, cached)
	if result.Self().Error.Valid {
		failure = result.Self().Error.Value.Self()
	}
	return result, nil
}

// checkFromContent reports whether a check runs in a workspace identified by
// its content and is planned the same way in a later session (see
// keptFromContent): not one whose function is cached per call or session. A
// generator's check reports the generator's state on every run, so it is
// never kept.
func checkFromContent(check dagql.ObjectResult[*core.Check]) bool {
	planned, err := check.ResultCall()
	return err == nil && !planned.SessionBound() &&
		workspaceFromContent(check.Self().Workspace) && check.Self().Generator.Self() == nil
}

func (s checksSchema) run(ctx context.Context, check dagql.ObjectResult[*core.Check], _ struct{}) (dagql.ObjectResult[*core.Check], error) {
	result, err := check.Self().Run(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Check]{}, err
	}
	if !result.Passed {
		// Not cached, like a failed call: the requests waiting on this run
		// share it, and a later request runs the check again.
		request, _ := ctx.Value(checkRequestKey{}).(*checkRequest)
		return dagql.ObjectResult[*core.Check]{}, &checkFailed{check: result, request: request}
	}
	// A run that is never reused isn't kept.
	if dagql.CurrentCall(ctx).SessionBound() {
		dagql.DeclinePersistence(ctx)
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Check]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, result)
}

// checkRequestKey carries the checkRequest of the sync that requests a run.
type checkRequestKey struct{}

// checkRequest identifies one sync's request for a check run. It isn't
// zero-sized, so each request's pointer is distinct.
type checkRequest struct{ _ byte }

// checkFailed is a failed check run, returned as __run's error.
type checkFailed struct {
	check *core.Check
	// request is the sync whose request ran the check.
	request *checkRequest
}

func (e *checkFailed) Error() string {
	if e.check.Error.Valid {
		return e.check.Error.Value.Self().Message
	}
	return "check failed"
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

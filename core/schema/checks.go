package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type checksSchema struct{}

func (s checksSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.Check](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.Check]{
		dagql.FuncWithDynamicInputs("sync", s.sync, s.cachePolicy).Doc("Run the check and retain its result."),
		dagql.NodeFunc("pass", s.pass).DoNotCache("Read the current check result.").Doc("Run the check and return whether it passes."),
		dagql.NodeFunc("error", s.error).DoNotCache("Read the current check result.").Doc("Run the check and return its failure, if any."),
	}.Install(srv)
}
func (s checksSchema) cachePolicy(_ context.Context, check dagql.ObjectResult[*core.Check], _ struct{}, req *dagql.CallRequest) error {
	req.TTL = check.Self().CacheTTL
	req.DoNotCache = check.Self().Completed || check.Self().RemoteArtifact != nil || check.Self().Generator.Self() != nil
	return nil
}
func (s checksSchema) sync(ctx context.Context, check *core.Check, _ struct{}) (*core.Check, error) {
	return check.Run(ctx)
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

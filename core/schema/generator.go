package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type generatorSchema struct{}

func (s generatorSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.Generator](srv).View(AfterVersion("v1.0.0-0")))
	stale := dagql.NodeFunc("stale", s.stale).Doc("A check that passes when this generator would produce no changes.")
	stale.Spec.Directives = append(stale.Spec.Directives, &ast.Directive{Name: "check"})
	dagql.Fields[*core.Generator]{
		dagql.NodeFunc("changeset", s.changeset).DoNotCache("Use the generation function cache policy.").Doc("Run the generator and return its changes."),
		// Each completed generator has its own identity. Its source function
		// still controls whether generation itself is cached.
		dagql.NodeFunc("sync", s.sync).WithInput(dagql.PerCallInput).Doc("Run the generator and retain its result."),
		stale,
	}.Install(srv)
}
func (generatorSchema) changeset(ctx context.Context, parent dagql.ObjectResult[*core.Generator], _ struct{}) (dagql.ObjectResult[*core.Changeset], error) {
	var result dagql.ObjectResult[*core.Changeset]
	if parent.Self().Result.Self() != nil {
		return parent.Self().Result, nil
	}
	inputs, err := artifactInputs(ctx, parent.Self().Artifact, parent.Self().Arguments)
	if err != nil {
		return result, err
	}
	err = evaluateArtifact(ctx, parent.Self().Artifact, &result, inputs...)
	return result, err
}
func (generatorSchema) stale(_ context.Context, parent dagql.ObjectResult[*core.Generator], _ struct{}) (*core.Check, error) {
	return &core.Check{Generator: parent, Assertion: dagql.NonNull(dagql.String("generated files are up to date"))}, nil
}
func (generatorSchema) sync(ctx context.Context, parent dagql.ObjectResult[*core.Generator], _ struct{}) (*core.Generator, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &changes, dagql.Selector{Field: "changeset"}); err != nil {
		return nil, err
	}
	var completed dagql.AnyResult
	if err := srv.Select(ctx, changes, &completed, dagql.Selector{Field: "sync"}); err != nil {
		return nil, err
	}
	result := parent.Self().Clone()
	result.Result = changes
	return result, nil
}

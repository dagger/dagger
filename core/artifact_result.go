package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// ArtifactResult keeps one failed value from aborting an entire selection.
type ArtifactResult struct {
	Artifact *Artifact `field:"true" doc:"The artifact that was evaluated."`
	Value    dagql.AnyObjectResult
	Error    dagql.Nullable[dagql.ObjectResult[*Error]] `field:"true" doc:"The evaluation failure, if any."`
}

func (*ArtifactResult) Type() *ast.Type { return &ast.Type{NamedType: "ArtifactResult", NonNull: true} }
func (r *ArtifactResult) Clone() *ArtifactResult {
	cp := *r
	cp.Artifact = r.Artifact.Clone()
	return &cp
}
func (r *ArtifactResult) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	owned, err := r.Artifact.AttachDependencyResults(ctx, nil, attach)
	if err != nil {
		return nil, err
	}
	if r.Value != nil {
		result, err := attach(r.Value)
		if err != nil {
			return nil, err
		}
		r.Value = result.(dagql.AnyObjectResult)
		owned = append(owned, result)
	}
	if r.Error.Valid {
		result, err := attach(r.Error.Value)
		if err != nil {
			return nil, err
		}
		r.Error.Value = result.(dagql.ObjectResult[*Error])
		owned = append(owned, result)
	}
	return owned, nil
}

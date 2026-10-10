package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type expertiseOwnerContextKey struct{}

type expertiseOwner struct {
	key   string
	valid bool
}

// WithExpertiseOwner binds the entry responsible for an expertise run. The
// outermost entry owns contributions made by nested composition and module calls.
func WithExpertiseOwner(ctx context.Context, key string) context.Context {
	if _, ok := ExpertiseOwner(ctx); ok {
		return ctx
	}
	return context.WithValue(ctx, expertiseOwnerContextKey{}, expertiseOwner{key: key, valid: true})
}

// ExpertiseOwner resolves the outermost expertise entry from the local context
// or the calling client's engine-side function-call record. Ordinary module
// calls outside an expertise run have no owner.
func ExpertiseOwner(ctx context.Context) (string, bool) {
	if owner, ok := ctx.Value(expertiseOwnerContextKey{}).(expertiseOwner); ok {
		return owner.key, owner.valid
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", false
	}
	fnCall, err := query.CurrentFunctionCall(ctx)
	if err != nil || fnCall == nil {
		return "", false
	}
	return fnCall.expertiseOwner.key, fnCall.expertiseOwner.valid
}

// This is a recorded argument, not an implicit input: cold recipe replay must
// restore it as well as partition cache lookup. It is never sent to a module SDK.
const expertiseOwnerArg = "_expertiseOwner"

func expertiseOwnerInputSpec() dagql.InputSpec {
	return dagql.InputSpec{
		Name:     expertiseOwnerArg,
		Type:     dagql.Optional[dagql.String]{},
		Internal: true,
	}
}

func stampExpertiseOwner(ctx context.Context, req *dagql.CallRequest) error {
	if owner := req.Arg(expertiseOwnerArg); owner != nil && owner.Value != nil && owner.Value.Kind != dagql.ResultCallLiteralKindNull {
		return nil
	}
	owner, _ := ExpertiseOwner(ctx)
	return req.SetArgInput(ctx, expertiseOwnerArg, dagql.Opt(dagql.String(owner)), false)
}

func expertiseCallContext(ctx context.Context, inputs []CallInput) context.Context {
	for _, input := range inputs {
		if input.Name != expertiseOwnerArg {
			continue
		}
		if owner, ok := input.Value.(dagql.Optional[dagql.String]); ok && owner.Valid {
			key := string(owner.Value)
			// Replay restores the recorded scope even when invoked under another
			// entry. An empty stamp masks the replay caller but leaves the call
			// free to start its own composition run.
			return context.WithValue(ctx, expertiseOwnerContextKey{}, expertiseOwner{key: key, valid: key != ""})
		}
	}
	return ctx
}

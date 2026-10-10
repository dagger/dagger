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

// StampExpertiseOwner records the current entry scope before cache lookup. On
// replay, even an absent scope is retained rather than inherited from the caller.
func StampExpertiseOwner(ctx context.Context, req *dagql.CallRequest) error {
	if req.Replay {
		// Absence is meaningful too: an unowned recipe must remain unowned.
		return nil
	}
	if owner := req.Arg(expertiseOwnerArg); owner != nil && owner.Value != nil && owner.Value.Kind != dagql.ResultCallLiteralKindNull {
		return nil
	}
	owner, _ := ExpertiseOwner(ctx)
	if owner == "" {
		// Leave ordinary module calls' existing IDs and cache keys unchanged.
		return nil
	}
	return req.SetArgInput(ctx, expertiseOwnerArg, dagql.Opt(dagql.String(owner)), false)
}

// RestoreExpertiseOwner restores a recorded call scope, overriding any ambient
// entry. An empty key masks the caller's entry but permits a new expertise run.
// Unlike WithExpertiseOwner, this is for replay/dispatch, not entering expertise.
func RestoreExpertiseOwner(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, expertiseOwnerContextKey{}, expertiseOwner{key: key, valid: key != ""})
}

func expertiseCallContext(ctx context.Context, opts *CallOpts) context.Context {
	if opts.useRecordedExpertiseOwner {
		// Schema calls have already stamped any fresh ambient owner. An absent
		// stamp therefore means unowned, including when replayed inside an entry.
		ctx = RestoreExpertiseOwner(ctx, "")
	}
	for _, input := range opts.Inputs {
		if input.Name != expertiseOwnerArg {
			continue
		}
		if owner, ok := input.Value.(dagql.Optional[dagql.String]); ok && owner.Valid {
			key := string(owner.Value)
			// Replay restores the recorded scope even when invoked under another
			// entry. An empty stamp masks the replay caller but leaves the call
			// free to start its own composition run.
			return RestoreExpertiseOwner(ctx, key)
		}
	}
	return ctx
}

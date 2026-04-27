package sdk

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
)

// Return true if the given module is a builtin SDK.
func IsModuleSDKBuiltin(module string) bool {
	return slices.Contains(validInbuiltSDKs, sdk(module))
}

func scopeSourceForSDKOperation(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	_ string,
	_ *dagql.Server,
) (inst dagql.ObjectResult[*core.ModuleSource], err error) {
	return core.ImplementationScopedModuleSource(ctx, src)
}

func ScopeModuleForSDKOperation(
	ctx context.Context,
	mod *core.Module,
	op string,
	dag *dagql.Server,
) (inst dagql.ObjectResult[*core.Module], err error) {
	if !mod.Source.Valid {
		return inst, fmt.Errorf("module has invalid source to scope for sdk operation %q", op)
	}

	sourceImplementationDigest, err := mod.Source.Value.Self().SourceImplementationDigest(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module source implementation digest for sdk operation %q: %w", op, err)
	}
	scopedMod := mod.Clone()
	scopedModInst, err := dagql.NewObjectResultForCall(scopedMod, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "sdk_scope_module",
		Type:        dagql.NewResultCallType(scopedMod.Type()),
		Args: []*dagql.ResultCallArg{
			{
				Name:  "op",
				Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: op},
			},
			{
				Name: "sourceImplementationDigest",
				Value: &dagql.ResultCallLiteral{
					Kind:        dagql.ResultCallLiteralKindString,
					StringValue: sourceImplementationDigest.String(),
				},
			},
		},
	})
	if err != nil {
		return inst, err
	}

	scopedDigest := hashutil.HashStrings("sdk_scope_module", op, sourceImplementationDigest.String())
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("scope module for sdk operation %q: current client metadata: %w", op, err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("scope module for sdk operation %q: engine cache: %w", op, err)
	}
	scopedModInst, err = scopedModInst.WithContentDigest(ctx, scopedDigest)
	if err != nil {
		return inst, fmt.Errorf("scope module for sdk operation %q: set content digest: %w", op, err)
	}
	attached, err := cache.AttachResult(ctx, clientMetadata.SessionID, dag, scopedModInst)
	if err != nil {
		return inst, err
	}
	typed, ok := attached.(dagql.ObjectResult[*core.Module])
	if !ok {
		return inst, fmt.Errorf("scope module for sdk operation %q: unexpected attached result %T", op, attached)
	}
	return typed, nil
}

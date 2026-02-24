package sdk

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
)

// Return true if the given module is a builtin SDK.
func IsModuleSDKBuiltin(module string) bool {
	return slices.Contains(validInbuiltSDKs, sdk(module))
}

func scopeSourceForSDKOperation(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	op string,
	dag *dagql.Server,
) (inst dagql.ObjectResult[*core.ModuleSource], err error) {
	srcContentDigestForSDK, err := src.Self().ContentDigestForSDK(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get source content digest for sdk operation %q: %w", op, err)
	}
	scopedID := src.ID().With(call.WithScopeToDigest(op, srcContentDigestForSDK))

	scopedSrc, err := dagql.NewObjectResultForID(src.Self(), dag, scopedID)
	if err != nil {
		return inst, err
	}
	_, err = dag.Cache.GetOrInitCall(ctx, dagql.CacheKey{
		ID: scopedSrc.ID(),
	}, dagql.ValueFunc(scopedSrc))
	if err != nil {
		return inst, err
	}

	return scopedSrc, nil
}

func ScopeModuleForSDKOperation(
	ctx context.Context,
	mod *core.Module,
	op string,
	dag *dagql.Server,
) (inst dagql.ObjectResult[*core.Module], err error) {
	if mod.ResultID == nil {
		return inst, fmt.Errorf("module has no ResultID to scope for sdk operation %s", op)
	}
	if !mod.Source.Valid {
		return inst, fmt.Errorf("module has invalid source to scope for sdk operation %s", op)
	}

	srcContentDigestForSDK, err := mod.Source.Value.Self().ContentDigestForSDK(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module source content digest for sdk operation %q: %w", op, err)
	}
	scopedID := mod.ResultID.With(call.WithScopeToDigest(op, hashutil.HashStrings(
		srcContentDigestForSDK.String(),
		"sdk-module-op",
	)))

	// ensure any extra digests are also scoped to this operation
	for _, extraDigest := range mod.ResultID.ExtraDigests() {
		extraDigest.Digest = hashutil.HashStrings(extraDigest.Digest.String(), op)
		scopedID = scopedID.With(call.WithReplacedExtraDigest(extraDigest))
	}

	scopedMod := mod.Clone()
	scopedMod.ResultID = scopedID

	scopedModInst, err := dagql.NewObjectResultForID(scopedMod, dag, scopedID)
	if err != nil {
		return inst, err
	}
	_, err = dag.Cache.GetOrInitCall(ctx, dagql.CacheKey{
		ID: scopedModInst.ID(),
	}, dagql.ValueFunc(scopedModInst))
	if err != nil {
		return inst, err
	}

	return scopedModInst, nil
}

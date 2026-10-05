package schema

import (
	"context"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// callerDeclaresGitClient reports whether the config governing the calling
// module declares a git client at the given address.
func callerDeclaresGitClient(
	ctx context.Context,
	dag *dagql.Server,
	caller dagql.ObjectResult[*core.Module],
	address string,
) (bool, error) {
	if !caller.Self().Source.Valid || caller.Self().Source.Value.Self() == nil {
		return false, nil
	}
	declared, err := core.ModuleDeclaredGitClients(ctx, dag, caller.Self().Source.Value.Self())
	if err != nil || len(declared) == 0 {
		return false, err
	}
	want := workspace.GitClientKey(address)
	return slices.ContainsFunc(declared, func(ref string) bool {
		return workspace.GitClientKey(ref) == want
	}), nil
}

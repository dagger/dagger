package schema

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/stretchr/testify/require"
)

func TestContainerExecArgsDigestIncludesCallID(t *testing.T) {
	newCallID := func(field string) *call.ID {
		return call.New().Append(&ast.Type{NamedType: "Query", NonNull: true}, field)
	}

	baseArgs := containerExecArgs{
	}
	baseArgs.ExecMD.Self = &buildkit.ExecutionMetadata{
		CacheMixin: digest.FromString("shared-cache-mixin"),
		CallID:     newCallID("firstCall"),
	}

	firstDigest, err := baseArgs.Digest()
	require.NoError(t, err)

	sameCallArgs := baseArgs
	sameCallArgs.ExecMD.Self = &buildkit.ExecutionMetadata{
		CacheMixin: digest.FromString("shared-cache-mixin"),
		CallID:     newCallID("firstCall"),
		ExecID:     "different-exec-id",
		ClientID:   "different-client-id",
	}

	sameCallDigest, err := sameCallArgs.Digest()
	require.NoError(t, err)
	require.Equal(t, firstDigest, sameCallDigest)

	secondCallArgs := baseArgs
	secondCallArgs.ExecMD.Self = &buildkit.ExecutionMetadata{
		CacheMixin: digest.FromString("shared-cache-mixin"),
		CallID:     newCallID("secondCall"),
	}

	secondDigest, err := secondCallArgs.Digest()
	require.NoError(t, err)
	require.NotEqual(t, firstDigest, secondDigest)
}

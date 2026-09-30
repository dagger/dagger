package dagql_test

import (
	"runtime"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/internal/points"
)

// Object results carry their class, and cached results can outlive the
// (typically per-client) forked server that wrapped them, so a class must not
// keep its server alive.
func TestClassesDoNotRetainServer(t *testing.T) {
	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	base := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](base)

	classes, fork := func() ([]dagql.ObjectType, weak.Pointer[dagql.Server]) {
		fork, err := base.Fork(ctx, Query{})
		require.NoError(t, err)
		var classes []dagql.ObjectType
		for _, name := range []string{"Query", "Point", "Line"} {
			class, ok := fork.ObjectType(name)
			require.True(t, ok)
			classes = append(classes, class)
		}
		classes = append(classes, dagql.NewClass[*points.Point](fork))
		// Build the schema so it is cached on the fork.
		require.NotNil(t, fork.Schema())
		return classes, weak.Make(fork)
	}()

	for range 3 {
		runtime.GC()
	}
	require.True(t, fork.Value() == nil, "forked server is still reachable from its classes")

	// Changing a class whose server is gone has nothing to invalidate.
	for _, class := range classes {
		class.Extend(dagql.FieldSpec{Name: "late", Type: dagql.String("")}, nil)
	}
}

// Class changes must still invalidate the schema of the server the class
// belongs to, and only that server.
func TestForkClassChangesInvalidateForkSchema(t *testing.T) {
	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	base := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](base)
	fork, err := base.Fork(ctx, Query{})
	require.NoError(t, err)

	hasField := func(srv *dagql.Server, name string) bool {
		return srv.Schema().Types["Point"].Fields.ForName(name) != nil
	}
	// Build and cache both schemas before changing the class.
	require.False(t, hasField(base, "forkOnly"))
	require.False(t, hasField(fork, "forkOnly"))

	class, ok := fork.ObjectType("Point")
	require.True(t, ok)
	class.Extend(dagql.FieldSpec{Name: "forkOnly", Type: dagql.String("")}, nil)

	require.True(t, hasField(fork, "forkOnly"))
	require.False(t, hasField(base, "forkOnly"))
}

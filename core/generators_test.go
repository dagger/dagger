package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleLoadFailureRegenerated(t *testing.T) {
	t.Parallel()

	changed := []string{".dagger/modules/stale/dagger.gen.go", "internal/dagger/client.go"}

	require.True(t, ModuleLoadFailure{Dir: ".dagger/modules/stale"}.Regenerated(changed))
	require.True(t, ModuleLoadFailure{Dir: ".dagger/modules/stale/"}.Regenerated(changed), "trailing slash")
	require.True(t, ModuleLoadFailure{Dir: "./.dagger/modules/stale"}.Regenerated(changed), "unclean dir")
	require.True(t, ModuleLoadFailure{Dir: "internal"}.Regenerated(changed), "nested change")
	require.True(t, ModuleLoadFailure{Dir: "."}.Regenerated(changed), "root module owns every path")

	require.False(t, ModuleLoadFailure{Dir: ".dagger/modules/stale-other"}.Regenerated(changed), "sibling prefix is not a parent")
	require.False(t, ModuleLoadFailure{Dir: ".dagger/modules/other"}.Regenerated(changed))
	require.False(t, ModuleLoadFailure{Dir: ""}.Regenerated(changed), "no directory (git source) is never regenerated")
	require.False(t, ModuleLoadFailure{Dir: "."}.Regenerated(nil), "no changes")
}

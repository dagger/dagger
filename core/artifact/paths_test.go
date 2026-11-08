package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var composePaths = Paths{
	{Type: "type:Service", Key: "docker/compose/projects/services/up", Module: "docker", Dimensions: []string{"/docker/compose/projects", "/docker/compose/projects/services"}},
	{Type: "type:Check", Key: "docker/compose/projects/lint", Module: "docker", Dimensions: []string{"/docker/compose/projects"}},
}

func TestTypeKeyProjection(t *testing.T) {
	project, service := "/docker/compose/projects", "/docker/compose/projects/services"
	item := composePaths.Candidates("type:Service", []string{project, service}, nil)
	require.Equal(t, []string{"docker/compose/projects/services/up"}, item)
	require.Equal(t, "up", ShortTypeKey(item, "docker/compose/projects/services/up"))

	paths := composePaths.CandidatePaths("type:Service", []string{project}, nil)
	for key, want := range map[string][]string{
		"services/up":                          {"docker/compose/projects/services/up"},
		"/docker/compose/projects/services/up": {"docker/compose/projects/services/up"},
		"down":                                 nil,
	} {
		got := ResolveTypeKey(paths, key)
		require.Equal(t, want, got, key)
	}
}

// Other paths form a set, even an operation on a nested collection's items.
func TestTypeKeySets(t *testing.T) {
	paths := Paths{
		{Type: "type:Check", Key: "items/verify", Dimensions: []string{"/app/items"}},
		{Type: "type:Check", Key: "items/parts/verify", Dimensions: []string{"/app/items", "/app/items/parts"}},
		{Type: "type:Check", Key: "other/verify", Dimensions: []string{"/app/other"}},
		{Type: "type:Check", Key: "docs/stale"},
		{Type: "type:Check", Key: "clients/stale"},
	}
	got := ResolveTypeKey(paths, "verify")
	require.Equal(t, []string{"items/parts/verify", "items/verify", "other/verify"}, got)
	got = ResolveTypeKey(paths, "stale")
	require.Equal(t, []string{"clients/stale", "docs/stale"}, got)
}

func TestTypeKeyFullPathSuffix(t *testing.T) {
	candidates := []string{"app/go/test", "go/test"} // sorted, as Candidates returns them
	require.Equal(t, []string{"app/go/test", "go/test"}, MatchTypeKey(candidates, "go/test"))
	require.Equal(t, []string{"go/test"}, MatchTypeKey(candidates, "/go/test"))
	// A full path that is another path's suffix needs the leading "/".
	require.Equal(t, "/go/test", ShortTypeKey(candidates, "go/test"))
	require.Equal(t, "app/go/test", ShortTypeKey(candidates, "app/go/test"))
	// A path outside the candidates keeps its full form.
	require.Equal(t, "unknown/lint", ShortTypeKey([]string{"go/lint"}, "unknown/lint"))
}

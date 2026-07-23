package workspace

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func mod(path string, deps ...string) SDKManagedModuleConfig {
	cfg := &modules.ModuleConfig{}
	for _, src := range deps {
		cfg.Dependencies = append(cfg.Dependencies, &modules.ModuleConfigDependency{
			Name:   src,
			Source: src,
		})
	}
	return SDKManagedModuleConfig{Path: path, Config: cfg}
}

func TestOrderSDKModulesLeafFirst(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst(nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("single leaf, no deps", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{mod("a")})
		require.NoError(t, err)
		require.Equal(t, []string{"a"}, got)
	})

	t.Run("linear chain c->b->a is emitted a,b,c", func(t *testing.T) {
		// declaration order deliberately reversed to prove reordering happens
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/c", "../b"),
			mod("mods/b", "../a"),
			mod("mods/a"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/a", "mods/b", "mods/c"}, got)
	})

	t.Run("diamond emits shared dep once, before both dependents", func(t *testing.T) {
		// a->b, a->c, b->d, c->d (siblings under mods/, referenced via ../)
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/a", "../b", "../c"),
			mod("mods/b", "../d"),
			mod("mods/c", "../d"),
			mod("mods/d"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/d", "mods/b", "mods/c", "mods/a"}, got)
		require.Len(t, got, 4, "shared dep d appears exactly once")
	})

	t.Run("independent roots keep declaration order", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("z"), mod("y"), mod("x"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"z", "y", "x"}, got)
	})

	t.Run("already-valid order returned unchanged", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/a"),
			mod("mods/b", "../a"),
			mod("mods/c", "../b"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/a", "mods/b", "mods/c"}, got)
	})

	t.Run("remote and pinned deps are external leaves (no edge)", func(t *testing.T) {
		cfg := &modules.ModuleConfig{Dependencies: []*modules.ModuleConfigDependency{
			{Name: "remote", Source: "github.com/acme/dep@main"},
			{Name: "pinned", Source: "b", Pin: "sha256:abc"},
		}}
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			{Path: "a", Config: cfg},
			mod("b"),
		})
		require.NoError(t, err)
		// no a->b edge from a pinned local-looking source; declaration order kept
		require.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("local dep outside managed set is a leaf (no edge)", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("a", "../unmanaged"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"a"}, got)
	})

	t.Run("../ dep inside managed set creates an edge", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/b", "../a"),
			mod("mods/a"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/a", "mods/b"}, got)
	})

	t.Run("dep escaping the root is a leaf (no edge)", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("a", "../../x"),
			mod("x"),
		})
		require.NoError(t, err)
		// a's ../../x resolves to ../x, above the root, never matching managed "x"
		require.Equal(t, []string{"a", "x"}, got)
	})

	t.Run("absolute dep source is ignored (no edge)", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("b", "/a"),
			mod("a"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"b", "a"}, got)
	})

	t.Run("self-reference is reported as a cycle", func(t *testing.T) {
		_, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("a", "."),
		})
		require.ErrorContains(t, err, "dependency cycle")
	})

	t.Run("duplicate paths with different spelling collapse to one", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/a"),
			mod("./mods/a"),
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/a"}, got)
	})

	t.Run("nil config is a leaf; dependents still ordered after it", func(t *testing.T) {
		got, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/b", "../a"),
			{Path: "mods/a", Config: nil},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"mods/a", "mods/b"}, got)
	})

	t.Run("direct cycle errors and names the members", func(t *testing.T) {
		_, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/a", "../b"),
			mod("mods/b", "../a"),
		})
		require.ErrorContains(t, err, "dependency cycle")
		require.ErrorContains(t, err, "mods/a")
		require.ErrorContains(t, err, "mods/b")
	})

	t.Run("longer cycle errors", func(t *testing.T) {
		_, err := OrderSDKModulesLeafFirst([]SDKManagedModuleConfig{
			mod("mods/a", "../b"),
			mod("mods/b", "../c"),
			mod("mods/c", "../a"),
		})
		require.ErrorContains(t, err, "dependency cycle")
	})
}

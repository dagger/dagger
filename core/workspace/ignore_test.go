package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIgnorePatterns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		configFile string
		ignore     []string
		want       []string
	}{
		{name: "empty", configFile: "dagger.toml"},
		{name: "root relative", configFile: "dagger.toml", ignore: []string{".delta", "pkg/vendor"}, want: []string{".delta", "pkg/vendor"}},
		{name: "leading slash", configFile: "dagger.toml", ignore: []string{"/dist"}, want: []string{"dist"}},
		{name: "negated leading slash", configFile: "dagger.toml", ignore: []string{"!/dist"}, want: []string{"!dist"}},
		{name: "parent escape", configFile: "dagger.toml", ignore: []string{"../etc", "dist"}, want: []string{"dist"}},
		{name: "dot dropped", configFile: "dagger.toml", ignore: []string{"."}, want: nil},
		{name: "nested config", configFile: "app/dagger.toml", ignore: []string{".delta", "/dist"}, want: []string{"app/.delta", "app/dist"}},
		{name: "nested escape", configFile: "app/dagger.toml", ignore: []string{"../outside"}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, IgnorePatterns(tc.configFile, tc.ignore))
		})
	}
}

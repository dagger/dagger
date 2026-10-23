package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveWorkspaceModuleLinks(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		links             map[string]string
		wantLinks         []string
		fail              bool
	}{
		{name: "chain", input: "alias/src", want: "vendor/real/src", links: map[string]string{"alias": "chain", "chain": "vendor/real"}, wantLinks: []string{"alias", "chain"}},
		{name: "relative parent", input: "nested/alias/src", want: "real/src", links: map[string]string{"nested/alias": "../real"}, wantLinks: []string{"nested/alias"}},
		{name: "absolute workspace link", input: "nested/alias", want: "real", links: map[string]string{"nested/alias": "/real"}, wantLinks: []string{"nested/alias"}},
		{name: "escape", input: "alias", links: map[string]string{"alias": "../outside"}, fail: true},
		{name: "cycle", input: "alias", links: map[string]string{"alias": "other", "other": "alias"}, fail: true},
		{name: "root", input: ".", want: "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, links, err := resolveWorkspaceModuleLinks(tc.input, func(p string) (string, error) { return tc.links[p], nil })
			if tc.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, resolved)
			require.Equal(t, tc.wantLinks, links)
		})
	}
}

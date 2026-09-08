package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGitHubRepo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		// already bare
		{"marcosnils/hello-dagger", "marcosnils/hello-dagger"},
		{"marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// host, no scheme
		{"github.com/marcosnils/hello-dagger", "marcosnils/hello-dagger"},
		// https / http
		{"https://github.com/marcosnils/hello-dagger", "marcosnils/hello-dagger"},
		{"https://github.com/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		{"http://github.com/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// https with userinfo (token)
		{"https://user:token@github.com/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// ssh URL form
		{"ssh://git@github.com/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// ssh URL form with port
		{"ssh://git@github.com:22/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// scp-like form
		{"git@github.com:marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		{"git@github.com:marcosnils/hello-dagger", "marcosnils/hello-dagger"},
		// git:// scheme
		{"git://github.com/marcosnils/hello-dagger.git", "marcosnils/hello-dagger"},
		// whitespace + trailing slash
		{"  https://github.com/marcosnils/hello-dagger/  ", "marcosnils/hello-dagger"},
	} {
		require.Equal(t, tc.want, normalizeGitHubRepo(tc.in), "normalizeGitHubRepo(%q)", tc.in)
	}
}

func TestIsAllDigits(t *testing.T) {
	require.True(t, isAllDigits("22"))
	require.True(t, isAllDigits("0"))
	require.False(t, isAllDigits(""))
	require.False(t, isAllDigits("2a"))
	require.False(t, isAllDigits("owner"))
}

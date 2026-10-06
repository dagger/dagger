package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsHTTPGitRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"https://github.com/dagger/dagger@main":     true,
		"http://10.88.0.1/repo.git@main":            true,
		"github.com/dagger/dagger@main":             true,
		"git@github.com:team/repo.git":              false,
		"alice@git.example.com:team/repo.git":       false,
		"alice@git.example.com:team/repo.git@main":  false,
		"git.example.com:team/repo.git":             false,
		"ssh://alice@git.example.com/team/repo.git": false,
		"git://git.example.com/team/repo.git":       false,
		"./workspace":                               false,
		"":                                          false,
	} {
		t.Run(ref, func(t *testing.T) {
			require.Equal(t, want, isHTTPGitRef(ref))
		})
	}
}

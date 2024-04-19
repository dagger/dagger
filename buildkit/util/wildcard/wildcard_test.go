package wildcard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWildcard(t *testing.T) {
	wildcardStr := "docker.io/*/alpine:*"
	wildcard, err := New(wildcardStr)
	assert.NoError(t, err)
	t.Run("Match", func(t *testing.T) {
		m := wildcard.Match("docker.io/library/alpine:latest")
		assert.Equal(t, []string{"docker.io/library/alpine:latest", "library", "latest"}, m.Submatches)
		s, err := m.Format("$1-${2}-$3-$$-$0")
		assert.NoError(t, err)
		// "$3" is replaced with an empty string without producing an error, because Format() internally uses regexp.*Regexp.Expand():
		// https://pkg.go.dev/regexp#Regexp.Expand
		assert.Equal(t, "library-latest--$-docker.io/library/alpine:latest", s)
	})
	t.Run("NoMatch", func(t *testing.T) {
		assert.Nil(t, wildcard.Match("docker.io/library/busybox:latest"))
		assert.Nil(t, wildcard.Match("alpine:latest"), "matcher must not be aware of the Docker Hub reference convention")
	})
}

func TestWildcardInvalid(t *testing.T) {
	wildcardStr := "docker.io/library/alpine:**"
	_, err := New(wildcardStr)
	assert.ErrorContains(t, err, "invalid wildcard: \"**\"")
}

func TestWildcardEscape(t *testing.T) {
	wildcardStr := "docker.io/library/alpine:\\*"
	wildcard, err := New(wildcardStr)
	assert.NoError(t, err)
	t.Run("NoMatch", func(t *testing.T) {
		assert.Nil(t, wildcard.Match("docker.io/library/alpine:latest"))
	})
}

func TestWildcardParentheses(t *testing.T) {
	wildcardStr := "docker.io/library/alpine:(*)"
	wildcard, err := New(wildcardStr)
	assert.NoError(t, err)
	t.Run("NoMatch", func(t *testing.T) {
		assert.Nil(t, wildcard.Match("docker.io/library/alpine:latest"))
	})
}

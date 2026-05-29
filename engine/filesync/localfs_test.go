package filesync

import (
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestLocalCopyOnlyPaths(t *testing.T) {
	t.Parallel()

	only := map[string]struct{}{
		"project":               {},
		"project/README.md":     {},
		"project/src/main.go":   {},
		"projector/unrelated":   {},
		"elsewhere/ignored.txt": {},
	}

	got := localCopyOnlyPaths(only, "project")

	assert.Assert(t, is.DeepEqual(map[string]struct{}{
		"":            {},
		"README.md":   {},
		"src/main.go": {},
	}, got))
}

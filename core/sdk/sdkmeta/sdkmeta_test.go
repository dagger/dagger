package sdkmeta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinSDKMetadataIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(Builtins))
	for _, name := range Builtins {
		_, duplicate := seen[name]
		require.False(t, duplicate, "duplicate built-in SDK %q", name)
		seen[name] = struct{}{}
	}
	require.Contains(t, seen, Rust)
}

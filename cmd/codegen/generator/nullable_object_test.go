package generator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupportsNullableObjects(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"", true},
		{"development", true},
		{"v0.21.0-dev", false},
		{"v1.0.0-beta.9-dev", false},
		{"v1.0.0-beta.10", true},
		{"v1.0.0-beta.10-dev", true},
		{"v1.0.0-rc.1", true},
		{"v1.0.0", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			require.Equal(t, test.want, SupportsNullableObjects(test.version))
		})
	}
}

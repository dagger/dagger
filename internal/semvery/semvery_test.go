package semvery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		version   string
		canonical string
		valid     bool
	}{
		{name: "canonical semver", version: "v1.2.3", canonical: "v1.2.3", valid: true},
		{name: "optional v prefix", version: "1.2.3", canonical: "v1.2.3", valid: true},
		{name: "incomplete minor", version: "v1.2", canonical: "v1.2.0", valid: true},
		{name: "incomplete major", version: "v1", canonical: "v1.0.0", valid: true},
		{name: "calver", version: "24.04", canonical: "v24.4.0", valid: true},
		{name: "zero-padded version", version: "v01.002.0003", canonical: "v1.2.3", valid: true},
		{name: "build metadata", version: "v1.2.3+linux-amd64", canonical: "v1.2.3", valid: true},
		{name: "prerelease", version: "v1.2-rc.1", canonical: "v1.2.0-rc.1", valid: true},
		{name: "empty component", version: "v1..2"},
		{name: "too many components", version: "v1.2.3.4"},
		{name: "empty prerelease", version: "v1.2.3-"},
		{name: "non-version", version: "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Parse(tc.version)
			require.Equal(t, tc.valid, ok)
			if ok {
				require.Equal(t, tc.canonical, got.Canonical)
			}
		})
	}
}

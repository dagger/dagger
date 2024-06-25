package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDaggerToken(t *testing.T) {
	tc := []struct {
		src      string
		ok       bool
		expected daggerToken
	}{
		{
			src:      "bad",
			ok:       false,
			expected: daggerToken{},
		},
		{
			src:      "dag_org_token",
			ok:       true,
			expected: daggerToken{orgName: "org", token: "token"},
		},
	}

	for _, tc := range tc {
		t.Run(tc.src, func(t *testing.T) {
			res, ok := parseDaggerToken(tc.src)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.expected, res)
		})
	}
}

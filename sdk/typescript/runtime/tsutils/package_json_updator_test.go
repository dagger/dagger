package tsutils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdatePackageJSON(t *testing.T) {
	type testCase struct {
		name        string
		packageJSON string
		expected    string
	}

	for _, tc := range []testCase{
		{
			name:        "empty package.json",
			packageJSON: `{}`,
			expected: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.3"
  }
}`,
		},
		{
			name: "package.json with local dagger dependency correctly switch to bundle mode",
			packageJSON: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts"
  }
}`,
			expected: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.3"
  }
}`,
		},
		{
			name: "package.json with comments",
			packageJSON: `{
  // Environment setup & latest features
  "type": "module",
  "dependencies": {
    // TypeScript
    "typescript": "5.9.3"
  }
} `,
			expected: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.3"
  }
}`,
		},
		{
			name: "package.json with existing typescript version",
			packageJSON: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.0",
		"@dagger.io/dagger": "./sdk/index.ts"
  }
}`,
			expected: `{
  "type": "module",
  "dependencies": {
    "typescript": "5.9.0"
  }
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc := tc

			res, err := UpdatePackageJSON(tc.packageJSON)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, res)
		})
	}
}

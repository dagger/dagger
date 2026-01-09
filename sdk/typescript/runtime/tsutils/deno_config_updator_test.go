package tsutils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateDenoConfigForModule(t *testing.T) {
	type testCase struct {
		name       string
		denoConfig string
		expected   string
	}

	for _, tc := range []testCase{
		{
			name:       "empty deno.json",
			denoConfig: `{}`,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
		{
			name: "deno.json with imports already set",
			denoConfig: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts"
  }
}`,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
		{
			name: "deno.json with comments",
			denoConfig: `{
  // Environment setup & latest features
  "imports": {
    "typescript": "npm:typescript@5.9.3" // A typescript version
  },
  "url": "https://foo/bar/baz.html" // A URL
} `,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ],
  "url": "https://foo/bar/baz.html"
}`,
		},
		{
			name: "deno.json with unstable flags already set",
			denoConfig: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3"
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
		{
			name: "deno.json with existing typescript version",
			denoConfig: `{
  "imports": {
    "typescript": "npm:typescript@5.9.0",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  }
}`,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.0",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
		{
			name: "deno.json from `deno init`",
			denoConfig: `{
  "tasks": {
    "dev": "deno run --watch main.ts"
  },
  "imports": {
    "@std/assert": "jsr:@std/assert@1"
  }
}
`,
			expected: `{
  "tasks": {
    "dev": "deno run --watch main.ts"
  },
  "imports": {
    "@std/assert": "jsr:@std/assert@1",
    "typescript": "npm:typescript@5.9.3",
    "@dagger.io/dagger": "./sdk/index.ts",
    "@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ],
  "compilerOptions": {
    "experimentalDecorators": true
  },
  "nodeModulesDir": "auto"
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc := tc

			res, err := UpdateDenoConfigForModule(tc.denoConfig)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, res)
		})
	}
}

func TestUpdateDenoConfigForClient(t *testing.T) {
	type testCase struct {
		name       string
		isRemote   bool
		denoConfig string
		expected   string
	}

	for _, tc := range []testCase{
		{
			name:       "empty deno.json",
			denoConfig: `{}`,
			isRemote:   false,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "./sdk/index.ts",
		"@dagger.io/dagger/telemetry": "./sdk/telemetry.ts"
  },
  "nodeModulesDir": "auto",
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
		{
			name:     "deno.json with remote lib already set",
			isRemote: true,
			denoConfig: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "npm:@dagger.io/dagger@0.18.0"
  }
}`,
			expected: `{
  "imports": {
    "typescript": "npm:typescript@5.9.3",
		"@dagger.io/dagger": "npm:@dagger.io/dagger@0.18.0"
  },
  "nodeModulesDir": "auto",
  "unstable": [
    "bare-node-builtins",
    "sloppy-imports",
    "node-globals",
    "byonm"
  ]
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc := tc

			res, err := UpdateDenoConfigForClient(tc.denoConfig, tc.isRemote)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, res)
		})
	}
}

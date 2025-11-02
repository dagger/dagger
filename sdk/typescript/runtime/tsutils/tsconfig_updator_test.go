package tsutils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultTsConfigForModule(t *testing.T) {
	defaultTSConfig := []byte(`{
  "compilerOptions": {
		"strict": true,
    "skipLibCheck": true,
		"target": "ES2022",
  	"experimentalDecorators": true,
		"moduleResolution": "Node",
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`)

	res := DefaultTSConfigForModule()
	require.JSONEq(t, string(defaultTSConfig), res)
}

func TestDefaultTSConfigForClient(t *testing.T) {
	defaultTSConfig := []byte(`{
  "compilerOptions": {
		"strict": true,
    "skipLibCheck": true,
		"target": "ES2022",
  	"experimentalDecorators": true,
		"moduleResolution": "Node",
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ],
      "@dagger.io/client": [
        "./dagger/client.gen.ts"
      ]
    }
  }
}`)

	res, err := DefaultTSConfigForClient("dagger")
	require.NoError(t, err)

	require.JSONEq(t, string(defaultTSConfig), res)
}

func TestUpdateTSConfigForModule(t *testing.T) {
	type testCase struct {
		name     string
		tsConfig string
		expected string
	}

	for _, tc := range []testCase{
		{
			name:     "empty tsconfig",
			tsConfig: `{}`,
			expected: `{
  "compilerOptions": {
		"experimentalDecorators": true,
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
		},
		{
			name: "tsconfig with paths already set",
			tsConfig: `{
  "compilerOptions": {
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
			expected: `{
  "compilerOptions": {
		"experimentalDecorators": true,
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
		},
		{
			name: "tsconfig with comments",
			tsConfig: `{
  "compilerOptions": {
    // Environment setup & latest features
    "lib": ["ESNext"],
    "target": "ESNext",
    "module": "Preserve",
    "moduleDetection": "force", // A module detection
    "jsx": "react-jsx",
    "allowJs": true,

    // Bundler mode
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "noEmit": true,

    // Best practices
    "strict": true,
    "skipLibCheck": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedIndexedAccess": true, // A no unchecked indexed access
    "noImplicitOverride": true,

    // Some stricter flags (disabled by default)
    "noUnusedLocals": false,
    "noUnusedParameters": false,
    "noPropertyAccessFromIndexSignature": false
  }
}`, expected: `{
  "compilerOptions": {
    "lib": ["ESNext"],
    "target": "ESNext",
    "module": "Preserve",
    "moduleDetection": "force",
    "jsx": "react-jsx",
    "allowJs": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "noEmit": true,
    "strict": true,
    "skipLibCheck": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "noUnusedLocals": false,
    "noUnusedParameters": false,
    "noPropertyAccessFromIndexSignature": false,
		"experimentalDecorators": true,
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
		},
		{
			name: "tsconfig with existing paths",
			tsConfig: `{
  "compilerOptions": {
    "paths": {
		  "custom-path": ["./foo.ts"],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
			expected: `{
  "compilerOptions": {
		"experimentalDecorators": true,
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
			"custom-path": ["./foo.ts"],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ]
    }
  }
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc := tc

			res, err := UpdateTSConfigForModule(tc.tsConfig)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, res)
		})
	}
}

func TestUpdateTSConfigForClient(t *testing.T) {
	type testCase struct {
		name      string
		clientDir string
		tsConfig  string
		isRemote  bool
		expected  string
	}

	for _, tc := range []testCase{
		{
			name:      "empty tsconfig",
			clientDir: "./dagger",
			tsConfig:  `{}`,
			isRemote:  false,
			expected: `{
  "compilerOptions": {
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ],
      "@dagger.io/client": [
        "./dagger/client.gen.ts"
      ]
    }
  }
}`,
		},
		{
			name:      "tsconfig with remote dagger library",
			clientDir: "example/foo",
			isRemote:  true,
			tsConfig:  `{}`,
			expected: `{
  "compilerOptions": {
    "paths": {
      "@dagger.io/client": [
        "./example/foo/client.gen.ts"
      ]
    }
  }
}`,
		},
		{
			name:      "tsconfig with paths already set",
			clientDir: "example/foo",
			isRemote:  false,
			tsConfig: `{
  "compilerOptions": {
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ],
      "@dagger.io/client": [
        "./example/foo/client.gen.ts"
      ]
    }
  }
}`,
			expected: `{
  "compilerOptions": {
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ],
      "@dagger.io/client": [
        "./example/foo/client.gen.ts"
      ]
    }
  }
}`,
		},
		{
			name:      "tsconfig with comments",
			clientDir: ".",
			isRemote:  false,
			tsConfig: `{
  "compilerOptions": {
    // Environment setup & latest features
    "lib": ["ESNext"],
    "target": "ESNext",
    "module": "Preserve",
    "moduleDetection": "force",
    "jsx": "react-jsx",
    "allowJs": true,

    // Bundler mode
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "noEmit": true,

    // Best practices
    "strict": true,
    "skipLibCheck": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,

    // Some stricter flags (disabled by default)
    "noUnusedLocals": false,
    "noUnusedParameters": false,
    "noPropertyAccessFromIndexSignature": false
  }
}`, expected: `{
  "compilerOptions": {
    "lib": ["ESNext"],
    "target": "ESNext",
    "module": "Preserve",
    "moduleDetection": "force",
    "jsx": "react-jsx",
    "allowJs": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "noEmit": true,
    "strict": true,
    "skipLibCheck": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "noUnusedLocals": false,
    "noUnusedParameters": false,
    "noPropertyAccessFromIndexSignature": false,
    "paths": {
      "@dagger.io/dagger": [
        "./sdk/index.ts"
      ],
      "@dagger.io/dagger/telemetry": [
        "./sdk/telemetry.ts"
      ],
      "@dagger.io/client": [
        "./client.gen.ts"
      ]
    }
  }
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc := tc

			res, err := UpdateTSConfigForClient(tc.tsConfig, tc.clientDir, tc.isRemote)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, res)
		})
	}
}

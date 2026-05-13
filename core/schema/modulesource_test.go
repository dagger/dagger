package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestModuleLocaLSource(t *testing.T) {
	schema := &moduleSourceSchema{}
	ctx := context.Background()

	src := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/home/user/dagger-test-modules",
		},
	}

	for _, tc := range []struct {
		name           string
		source         *core.ModuleSource
		expectedResult string
		expectError    bool
		fn             func(ctx context.Context, source *core.ModuleSource, args struct{}) (string, error)
	}{
		{
			name:           "Local module source commit",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceCommit,
		},
		{
			name:           "Local module source html repo url",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceHTMLRepoURL,
		},
		{
			name:           "Local module source version",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(ctx, tc.source, struct{}{})
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

func TestLoadModuleSourceConfigBuiltinDependency(t *testing.T) {
	schema := &moduleSourceSchema{}
	parent := &core.ModuleSource{
		Kind:               core.ModuleSourceKindLocal,
		Local:              &core.LocalModuleSource{ContextDirectoryPath: "/src"},
		SourceRootSubpath:  "parent",
		ModuleOriginalName: "parent",
		EngineVersion:      engine.Version,
	}
	parent.Dependencies = []dagql.ObjectResult[*core.ModuleSource]{
		moduleSourceResult(t, &core.ModuleSource{
			Kind:               core.ModuleSourceKindBuiltin,
			ModuleName:         "python-sdk",
			ModuleOriginalName: "python-sdk",
			Builtin: &core.BuiltinModuleSource{
				Name: "python-runtime",
			},
		}),
	}

	cfg, err := schema.loadModuleSourceConfig(parent)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 1)
	require.Equal(t, "python-sdk", cfg.Dependencies[0].Name)
	require.Equal(t, "python-runtime", cfg.Dependencies[0].Source)
	require.Empty(t, cfg.Dependencies[0].Pin)
}

func moduleSourceResult(t *testing.T, src *core.ModuleSource) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()

	res, err := dagql.NewResultForCall(src, &dagql.ResultCall{
		Type: dagql.NewResultCallType(src.Type()),
	})
	require.NoError(t, err)
	return dagql.ObjectResult[*core.ModuleSource]{Result: res}
}

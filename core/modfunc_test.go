package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestModuleFunctionCacheImplicitInputs(t *testing.T) {
	sessionCtx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "client-1",
		SessionID: "session-1",
	})

	for _, tc := range []struct {
		name               string
		mod                *Module
		fn                 *Function
		expectSessionScope bool
	}{
		{
			name: "default policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: false,
		},
		{
			name: "explicit per-session policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name:        "fn",
				CachePolicy: FunctionCachePolicyPerSession,
			},
			expectSessionScope: true,
		},
		{
			name: "default policy with module disable-default-caching",
			mod: &Module{
				NameField:                     "test",
				DisableDefaultFunctionCaching: true,
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modRes, err := dagql.NewObjectResultForCall(
				tc.mod,
				func() *dagql.Server {
					dag := newCoreDagqlServerForTest(t, &Query{})
					dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
					return dag
				}(),
				&dagql.ResultCall{
					Kind:        dagql.ResultCallKindSynthetic,
					SyntheticOp: "modfunc_test_module",
					Type:        dagql.NewResultCallType((&Module{}).Type()),
				},
			)
			require.NoError(t, err)

			modFn := &ModuleFunction{
				mod:      modRes,
				metadata: tc.fn,
			}

			inputs := modFn.cacheImplicitInputs()
			byName := mapImplicitInputsByName(inputs)
			_, hasLegacyScopeInput := byName["moduleFunctionScope"]
			require.False(t, hasLegacyScopeInput, "legacy module scope implicit input should not be present")

			sessionInput, hasSessionInput := byName[dagql.PerSessionInput.Name]
			require.Equal(t, tc.expectSessionScope, hasSessionInput)
			if tc.expectSessionScope {
				require.Equal(
					t,
					"session-1",
					resolveImplicitInputString(t, sessionInput, sessionCtx),
				)
			}
		})
	}
}

func TestModuleFunctionCacheImplicitInputsPerModuleVariant(t *testing.T) {
	for _, tc := range []struct {
		name           string
		variantDigest  string
		expectVariant  bool
		expectedDigest string
	}{
		{
			name:          "no variant digest: non-customized modules emit no salt",
			variantDigest: "",
			expectVariant: false,
		},
		{
			name:           "variant digest set: calls from this module instance get salted",
			variantDigest:  "variant-abc123",
			expectVariant:  true,
			expectedDigest: "variant-abc123",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := &Module{
				NameField:             "test",
				AsModuleVariantDigest: tc.variantDigest,
			}
			modRes, err := dagql.NewObjectResultForCall(
				mod,
				func() *dagql.Server {
					dag := newCoreDagqlServerForTest(t, &Query{})
					dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
					return dag
				}(),
				&dagql.ResultCall{
					Kind:        dagql.ResultCallKindSynthetic,
					SyntheticOp: "modfunc_test_variant_module",
					Type:        dagql.NewResultCallType((&Module{}).Type()),
				},
			)
			require.NoError(t, err)

			modFn := &ModuleFunction{
				mod:      modRes,
				metadata: &Function{Name: "fn"},
			}

			byName := mapImplicitInputsByName(modFn.cacheImplicitInputs())
			variantInput, hasVariantInput := byName["cachePerModuleVariant"]
			require.Equal(t, tc.expectVariant, hasVariantInput,
				"cachePerModuleVariant presence must track Module.AsModuleVariantDigest")
			if tc.expectVariant {
				require.Equal(t, tc.expectedDigest,
					resolveImplicitInputString(t, variantInput, context.Background()),
					"resolver must surface the exact digest so two variants hash differently")
			}
		})
	}
}

func TestModuleFunctionCacheImplicitInputsNilSafe(t *testing.T) {
	require.Nil(t, (*ModuleFunction)(nil).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{}).cacheImplicitInputs())
	dag := newCoreDagqlServerForTest(t, &Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
	modRes, err := dagql.NewObjectResultForCall(
		&Module{},
		dag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "modfunc_test_nil_safe_module",
			Type:        dagql.NewResultCallType((&Module{}).Type()),
		},
	)
	require.NoError(t, err)
	require.Nil(t, (&ModuleFunction{
		mod: modRes,
	}).cacheImplicitInputs())
}

func mapImplicitInputsByName(inputs []dagql.ImplicitInput) map[string]dagql.ImplicitInput {
	byName := make(map[string]dagql.ImplicitInput, len(inputs))
	for _, input := range inputs {
		byName[input.Name] = input
	}
	return byName
}

func resolveImplicitInputString(t *testing.T, input dagql.ImplicitInput, ctx context.Context) string {
	t.Helper()

	value, err := input.Resolver(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, value)

	strVal, ok := value.(dagql.String)
	require.True(t, ok, "expected dagql.String implicit input value, got %T", value)
	return strVal.String()
}

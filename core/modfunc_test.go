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
			modFn := &ModuleFunction{
				mod:      tc.mod,
				metadata: tc.fn,
			}

			inputs := modFn.cacheImplicitInputs()
			byName := mapImplicitInputsByName(inputs)
			_, hasLegacyScopeInput := byName["moduleFunctionScope"]
			require.False(t, hasLegacyScopeInput, "legacy module scope implicit input should not be present")

			sessionInput, hasSessionInput := byName[dagql.CachePerSession.Name]
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

func TestModuleFunctionCacheImplicitInputsNilSafe(t *testing.T) {
	require.Nil(t, (*ModuleFunction)(nil).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{}).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{
		mod: &Module{},
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

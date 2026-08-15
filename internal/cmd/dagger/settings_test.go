package daggercmd

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"

	workspacepkg "github.com/dagger/dagger/core/workspace"
)

func TestWorkspaceSettingWriteValue(t *testing.T) {
	listSetting := workspaceSetting{Module: "vitest", Key: "tags", IsList: true}
	scalarSetting := workspaceSetting{Module: "aws", Key: "region"}

	t.Run("a single value passes through unchanged", func(t *testing.T) {
		for _, setting := range []workspaceSetting{listSetting, scalarSetting} {
			for _, value := range []string{"plain", "a,b", "[abc]*", ""} {
				got, values, err := workspaceSettingWriteValue(setting, []string{value})
				require.NoError(t, err)
				require.Equal(t, value, got)
				require.Nil(t, values)
			}
		}
	})

	t.Run("multiple values for a list setting pass as an explicit list", func(t *testing.T) {
		value, values, err := workspaceSettingWriteValue(listSetting, []string{"docs", "sdk/go"})
		require.NoError(t, err)
		require.Empty(t, value)
		require.Equal(t, []string{"docs", "sdk/go"}, values)
	})

	t.Run("elements with commas or brackets stay verbatim", func(t *testing.T) {
		value, values, err := workspaceSettingWriteValue(listSetting, []string{"a,b", `["c"]`})
		require.NoError(t, err)
		require.Empty(t, value)
		require.Equal(t, []string{"a,b", `["c"]`}, values)
	})

	t.Run("multiple values for a scalar setting fail", func(t *testing.T) {
		_, _, err := workspaceSettingWriteValue(scalarSetting, []string{"one", "two"})
		require.ErrorContains(t, err, `setting "region" of module "aws" is not a list and accepts a single value`)
	})

	t.Run("multiple values fail when isList is unset", func(t *testing.T) {
		_, _, err := workspaceSettingWriteValue(workspaceSetting{Module: "m", Key: "k"}, []string{"one", "two"})
		require.ErrorContains(t, err, "is not a list")
	})
}

func TestIsUndefinedEnvError(t *testing.T) {
	structured := &gqlerror.Error{
		Message: "something wrapped beyond recognition",
		Extensions: map[string]any{
			"_type": workspacepkg.UndefinedEnvErrorType,
			"env":   "dev",
		},
	}
	require.True(t, isUndefinedEnvError(fmt.Errorf("wrap: %w", structured), "dev"))
	// The structured match is authoritative: same _type, different env.
	require.False(t, isUndefinedEnvError(structured, "prod"))

	// Fallback for engines that don't attach extensions.
	plain := fmt.Errorf(`connect: workspace env "dev" is not defined (no envs defined)`)
	require.True(t, isUndefinedEnvError(plain, "dev"))
	require.False(t, isUndefinedEnvError(plain, "prod"))
	require.False(t, isUndefinedEnvError(nil, "dev"))
}

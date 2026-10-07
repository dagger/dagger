package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLINaming(t *testing.T) {
	require.Equal(t, "e2e-test", cliName("e2eTest"))
	require.Equal(t, "http-client", cliName("HTTPClient"))
	require.Equal(t, "user-ids", cliName("userIDs"))
	require.Equal(t, "docker-config", cliName("dockerConfig"))

	require.Equal(t, []string{"myLLMMod", "myLlmMod"}, fieldNameCandidates("my-llm-mod"))
	require.Equal(t, []string{"myMod"}, fieldNameCandidates("my-mod"))

	require.True(t, sameObjectName("MyModHTTPClient", "MyModHTTPClient"))
	require.True(t, sameObjectName("MyModHttpclient", "MyModHTTPClient"))
	require.True(t, sameObjectName("MyLlmMod", "my-llm-mod"))
}

func TestCLIFlagNormalization(t *testing.T) {
	require.Equal(t, "e2e-test", registerFlagName("e2eTest"))
	for _, typed := range []string{"e2e-test", "e2eTest", "E2ETest", "e-2-e-test", "e2e_test"} {
		require.Equal(t, "e2e-test", normalizeFlagName(typed), typed)
	}
	require.Equal(t, "docker-config", normalizeFlagName("dockerConfig"))
	require.Equal(t, "json", normalizeFlagName("json"))
	require.Equal(t, "shell-on-error", normalizeFlagName("shell-on-error"))
}

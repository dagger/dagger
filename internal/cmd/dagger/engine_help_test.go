package daggercmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client/drivers"
)

func TestEngineHelpTopic(t *testing.T) {
	root := testRootCommand()

	topic, _, err := root.Find([]string{"engine"})
	require.NoError(t, err)
	require.Equal(t, engineHelpCmd, topic)

	// A help topic is not a command: Cobra lists it under "Additional help
	// topics", not in the command list, and it declares no capabilities, so
	// its usage stays free of the global flags.
	require.False(t, topic.Hidden)
	require.False(t, topic.Runnable())
	require.True(t, topic.IsAdditionalHelpTopicCommand())
	require.False(t, topic.IsAvailableCommand())
	require.False(t, commandHasCapability(topic, mayCallEngine))
	require.False(t, commandHasCapability(topic, mayRenderPipeline))

	rootHelp := renderHelp(t, root)
	require.Contains(t, rootHelp, "ADDITIONAL HELP TOPICS")
	require.Contains(t, rootHelp, "dagger engine")
	// The catalog itself must not reach the front-door usage message.
	require.NotContains(t, rootHelp, "image+nerdctl://IMAGE")

	help := renderHelp(t, topic)
	// The topic carries the full catalog, including the variants and the
	// legacy schemes that the flag usage leaves out.
	for _, scheme := range drivers.SchemeCatalog() {
		require.Contains(t, help, scheme.Value, scheme.Scheme)
	}
	require.Contains(t, help, "tcp://HOST:PORT (no authentication)")
	require.Contains(t, help, RunnerHostEnv)
	require.Contains(t, help, cloudEngineEnv)
	// Both environment variables are soft-deprecated in favour of --engine.
	require.Contains(t, help, cloudEngineEnv+" (deprecated; any value selects Dagger Cloud)")
	require.Contains(t, help, RunnerHostEnv+" (deprecated)")
	require.Contains(t, help, "--engine=cloud  instead of "+cloudEngineEnv)
	require.Contains(t, help, "--engine=URI    instead of "+RunnerHostEnv)
	require.Contains(t, help, "dagger call --engine=cloud build")
}

func TestEngineFlagCompletion(t *testing.T) {
	root := testRootCommand()

	fn, ok := root.GetFlagCompletionFunc("engine")
	require.True(t, ok)

	values, directive := fn(root, nil, "")
	require.Equal(t, drivers.SchemeCompletions(), values)
	// The values end in "://", so the shell must not append a space, and a
	// path is never a valid completion.
	require.Equal(t, cobra.ShellCompDirectiveNoSpace|cobra.ShellCompDirectiveNoFileComp, directive)
	require.Contains(t, values, "cloud")
	require.Contains(t, values, "container+podman://")
	require.NotContains(t, values, "docker-container://")
}

// A completion request names the command being completed, not the command that
// runs, so the capability gate must let it through. Completion still hides the
// flags that the target command does not support.
func TestShellCompletionSkipsCapabilityValidation(t *testing.T) {
	root := testRootCommand()

	for _, request := range []string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd} {
		require.NoError(t, validateFlagCapabilities(root, []string{request, "check", "--engine", ""}), request)
		require.NoError(t, validateFlagCapabilities(root, []string{request, "trace", "--engine", ""}), request)
	}
	require.False(t, isShellCompletionRequest(nil))
	require.False(t, isShellCompletionRequest([]string{"check", "--engine", "cloud"}))
}

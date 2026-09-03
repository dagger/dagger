package daggercmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/cobradocs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestCommandCapabilityInheritanceSkipsRoot(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	parent := &cobra.Command{Use: "parent"}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(parent)
	parent.AddCommand(child)

	setCommandCapabilities(root, mayRenderPipeline)
	require.True(t, commandHasCapability(root, mayRenderPipeline))
	require.False(t, commandHasCapability(parent, mayRenderPipeline))
	require.False(t, commandHasCapability(child, mayRenderPipeline))

	setCommandCapabilities(parent, mayRenderPipeline)
	require.True(t, commandHasCapability(parent, mayRenderPipeline))
	require.True(t, commandHasCapability(child, mayRenderPipeline))
}

func TestMayRenderPipelineFlags(t *testing.T) {
	oldQuiet := quiet
	oldSilent := silent
	oldProgress := progress
	oldWeb := web
	oldNoExit := noExit
	oldDotOutput := dotOutputFilePath
	oldDotFocus := dotFocusField
	oldDotInternal := dotShowInternal
	t.Cleanup(func() {
		quiet = oldQuiet
		silent = oldSilent
		progress = oldProgress
		web = oldWeb
		noExit = oldNoExit
		dotOutputFilePath = oldDotOutput
		dotFocusField = oldDotFocus
		dotShowInternal = oldDotInternal
	})

	flags := pflag.NewFlagSet("render", pflag.ContinueOnError)
	installMayRenderPipelineFlags(flags)

	expected := []string{
		"quiet",
		"silent",
		"progress",
		"web",
		"no-exit",
		"dot-output",
		"dot-focus-field",
		"dot-show-internal",
	}
	var count int
	flags.VisitAll(func(*pflag.Flag) { count++ })
	require.Equal(t, len(expected), count)
	for _, name := range expected {
		flag := flags.Lookup(name)
		require.NotNil(t, flag, name)
		require.Equal(t, []string{string(mayRenderPipeline)}, flag.Annotations[flagCapabilitiesAnnotation], name)
	}
	for _, name := range []string{"dot-output", "dot-focus-field", "dot-show-internal"} {
		require.True(t, flags.Lookup(name).Hidden, name)
	}
}

func TestMayRenderPipelineCommands(t *testing.T) {
	expected := []string{
		"dagger",
		"dagger agent",
		"dagger api call",
		"dagger api listen",
		"dagger api query",
		"dagger api session",
		"dagger api with-session",
		"dagger call",
		"dagger check",
		"dagger core",
		"dagger generate",
		"dagger listen",
		"dagger mcp",
		"dagger module sdk",
		"dagger query",
		"dagger run",
		"dagger session",
		"dagger shell",
		"dagger terminal",
		"dagger trace",
		"dagger up",
	}
	var actual []string
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if slices.Contains(strings.Fields(cmd.Annotations[commandCapabilitiesAnnotation]), string(mayRenderPipeline)) {
			actual = append(actual, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(rootCmd)
	require.ElementsMatch(t, expected, actual)

	for name, cmd := range map[string]*cobra.Command{
		"settings":  settingsCmd,
		"setup":     setupCmd,
		"installed": installedCmd,
	} {
		require.False(t, commandHasCapability(cmd, mayRenderPipeline), name)
	}
}

func TestCapabilityScopedFlagHelp(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("progress", "auto", "Progress output format")
	setFlagSetCapabilities(root.PersistentFlags(), mayRenderPipeline)

	render := &cobra.Command{Use: "render"}
	setCommandCapabilities(render, mayRenderPipeline)
	plain := &cobra.Command{Use: "plain"}
	root.AddCommand(render, plain)

	renderHelp := commandUsage(t, render)
	require.Contains(t, renderHelp, "--progress")

	plainHelp := commandUsage(t, plain)
	require.NotContains(t, plainHelp, "--progress")
}

func TestCapabilityScopedFlagValidation(t *testing.T) {
	newCommand := func(render, shadow bool) *cobra.Command {
		root := &cobra.Command{Use: "root"}
		root.PersistentFlags().String("progress", "auto", "Progress output format")
		setFlagSetCapabilities(root.PersistentFlags(), mayRenderPipeline)
		child := &cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}}
		if shadow {
			child.Flags().String("progress", "local", "Local progress value")
		}
		if render {
			setCommandCapabilities(child, mayRenderPipeline)
		}
		root.AddCommand(child)
		return root
	}

	render := newCommand(true, false)
	require.NoError(t, validateFlagCapabilities(render, []string{"--progress=plain", "child"}))

	plain := newCommand(false, false)
	require.EqualError(t, validateFlagCapabilities(plain, []string{"--progress=plain", "child"}), `flag --progress is not supported by command "root child"`)

	// Cobra resolves a command-local flag even when it appears before the
	// subcommand. This preserves forms such as `dagger -q version`, where
	// version has its own unrelated -q flag.
	shadowed := newCommand(false, true)
	require.NoError(t, validateFlagCapabilities(shadowed, []string{"--progress=local", "child"}))

	local := newCommand(false, true)
	require.NoError(t, validateFlagCapabilities(local, []string{"child", "--progress=local"}))

	shortRoot := &cobra.Command{Use: "root"}
	shortRoot.PersistentFlags().BoolP("quiet", "q", false, "Quiet pipeline output")
	setFlagSetCapabilities(shortRoot.PersistentFlags(), mayRenderPipeline)
	shortChild := &cobra.Command{Use: "child"}
	shortChild.Flags().BoolP("quiet", "q", false, "Print only the result")
	shortRoot.AddCommand(shortChild)
	require.NoError(t, validateFlagCapabilities(shortRoot, []string{"-q", "child"}))

	disabledRoot := newCommand(false, false)
	disabledChild, _, err := disabledRoot.Find([]string{"child"})
	require.NoError(t, err)
	disabledChild.DisableFlagParsing = true
	require.EqualError(t, validateFlagCapabilities(disabledRoot, []string{"child", "--progress=plain"}), `flag --progress is not supported by command "root child"`)
	setCommandCapabilities(disabledChild, mayRenderPipeline)
	require.NoError(t, validateFlagCapabilities(disabledRoot, []string{"child", "--progress=plain"}))

	// Validation must not apply a flag value before the command is accepted.
	var value string
	sideEffect := &cobra.Command{Use: "root"}
	sideEffect.PersistentFlags().StringVar(&value, "progress", "auto", "Progress output format")
	setFlagSetCapabilities(sideEffect.PersistentFlags(), mayRenderPipeline)
	sideEffect.AddCommand(&cobra.Command{Use: "child"})
	require.EqualError(t, validateFlagCapabilities(sideEffect, []string{"child", "--progress=bogus"}), `flag --progress is not supported by command "root child"`)
	require.Equal(t, "auto", value)
}

func TestGlobalFlagParsingRespectsLocalShadow(t *testing.T) {
	oldQuiet := quiet
	oldXRelease := xRelease
	t.Cleanup(func() {
		quiet = oldQuiet
		xRelease = oldXRelease
	})
	quiet = 0

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().CountVarP(&quiet, "quiet", "q", "Quiet pipeline output")
	setFlagSetCapabilities(root.PersistentFlags(), mayRenderPipeline)
	root.PersistentFlags().StringVar(&xRelease, "x-release", "", "Experimental release")
	child := &cobra.Command{Use: "child"}
	var localQuiet bool
	child.Flags().BoolVarP(&localQuiet, "quiet", "q", false, "Print only the result")
	root.AddCommand(child)

	parseGlobalFlags(root, []string{"-q", "child"})
	require.Zero(t, quiet)
	require.False(t, localQuiet)
}

func TestCapabilityScopedFlagCompletion(t *testing.T) {
	complete := func(render bool) string {
		t.Helper()
		root := &cobra.Command{
			Use:           "root",
			SilenceErrors: true,
			SilenceUsage:  true,
			PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
				if restore := hideUnavailableCompletionFlags(cmd, args); restore != nil {
					cobra.OnFinalize(restore)
				}
				return nil
			},
		}
		root.PersistentFlags().String("progress", "auto", "Progress output format")
		setFlagSetCapabilities(root.PersistentFlags(), mayRenderPipeline)
		child := &cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}}
		if render {
			setCommandCapabilities(child, mayRenderPipeline)
		}
		root.AddCommand(child)

		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs([]string{cobra.ShellCompRequestCmd, "child", "--pr"})
		require.NoError(t, root.Execute())
		require.False(t, root.PersistentFlags().Lookup("progress").Hidden)
		return output.String()
	}

	require.NotContains(t, complete(false), "--progress")
	require.Contains(t, complete(true), "--progress")
}

func TestCapabilityScopedFlagMarkdown(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("progress", "auto", "Progress output format")
	setFlagSetCapabilities(root.PersistentFlags(), mayRenderPipeline)
	plain := &cobra.Command{Use: "plain"}
	render := &cobra.Command{Use: "render"}
	setCommandCapabilities(render, mayRenderPipeline)
	root.AddCommand(plain, render)

	markdown := func(cmd *cobra.Command) string {
		t.Helper()
		var output bytes.Buffer
		require.NoError(t, cobradocs.Markdown(cmd, &output, cobradocs.MarkdownOptions{
			IncludeFlag: FlagAvailableForCommand,
		}))
		return output.String()
	}

	require.NotContains(t, markdown(plain), "--progress")
	require.False(t, root.PersistentFlags().Lookup("progress").Hidden)
	require.Contains(t, markdown(render), "--progress")
}

func commandUsage(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetUsageTemplate(usageTemplate)
	require.NoError(t, cmd.Usage())
	return output.String()
}

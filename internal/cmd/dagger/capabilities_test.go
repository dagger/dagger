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

func TestCommandCapabilityInheritance(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	parent := &cobra.Command{Use: "parent"}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(parent)
	parent.AddCommand(child)

	setLocalCommandCapabilities(root, mayRenderPipeline)
	require.True(t, commandHasCapability(root, mayRenderPipeline))
	require.False(t, commandHasCapability(parent, mayRenderPipeline))
	require.False(t, commandHasCapability(child, mayRenderPipeline))

	setCommandCapabilities(parent, mayRenderPipeline)
	require.True(t, commandHasCapability(parent, mayRenderPipeline))
	require.True(t, commandHasCapability(child, mayRenderPipeline))

	setLocalCommandCapabilities(parent, mayCallEngine)
	require.True(t, commandHasCapability(parent, mayCallEngine))
	require.False(t, commandHasCapability(child, mayCallEngine))
}

func TestMaySelectWorkspaceFlags(t *testing.T) {
	oldWorkspace := workspaceRef
	t.Cleanup(func() { workspaceRef = oldWorkspace })

	flags := pflag.NewFlagSet("workspace", pflag.ContinueOnError)
	installGlobalFlags(flags)

	flag := flags.Lookup("workspace")
	require.NotNil(t, flag)
	require.Equal(t, "W", flag.Shorthand)
	require.False(t, flag.Hidden)
	require.Equal(t, []string{string(maySelectWorkspace)}, flag.Annotations[flagCapabilitiesAnnotation])
}

func TestMayProduceOutputFlags(t *testing.T) {
	oldAutoApply := autoApply
	t.Cleanup(func() { autoApply = oldAutoApply })

	flags := pflag.NewFlagSet("output", pflag.ContinueOnError)
	installGlobalFlags(flags)

	flag := flags.Lookup("auto-apply")
	require.NotNil(t, flag)
	require.Equal(t, "y", flag.Shorthand)
	require.False(t, flag.Hidden)
	require.Equal(t, []string{string(mayProduceOutput)}, flag.Annotations[flagCapabilitiesAnnotation])
}

func TestDebugFlags(t *testing.T) {
	oldDebug := debugFlag
	t.Cleanup(func() { debugFlag = oldDebug })

	flags := pflag.NewFlagSet("debug", pflag.ContinueOnError)
	installGlobalFlags(flags)

	flag := flags.Lookup("debug")
	require.NotNil(t, flag)
	require.Equal(t, "d", flag.Shorthand)
	require.False(t, flag.Hidden)
	require.Equal(t, []string{
		string(mayCallEngine),
		string(mayRenderPipeline),
	}, flag.Annotations[flagAnyCapabilitiesAnnotation])

	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		expected := commandHasCapability(cmd, mayCallEngine) || commandHasCapability(cmd, mayRenderPipeline)
		require.Equal(t, expected, FlagAvailableForCommand(cmd, flag), cmd.CommandPath())
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(rootCmd)

	require.True(t, FlagAvailableForCommand(settingsCmd, flag))
	require.True(t, FlagAvailableForCommand(traceCmd, flag))
	require.False(t, FlagAvailableForCommand(activityCmd, flag))
	require.False(t, FlagAvailableForCommand(sdkInstalledCmd, flag))
}

func TestMayCallEngineFlags(t *testing.T) {
	oldCloud := useCloudEngine
	oldEngine := engineFlag
	oldProfile := profileFlag
	oldShellOnError := shellOnError
	oldShellCommandOnError := shellCommandOnError
	t.Cleanup(func() {
		useCloudEngine = oldCloud
		engineFlag = oldEngine
		profileFlag = oldProfile
		shellOnError = oldShellOnError
		shellCommandOnError = oldShellCommandOnError
	})
	useCloudEngine = false
	engineFlag = ""

	flags := pflag.NewFlagSet("engine", pflag.ContinueOnError)
	installMayCallEngineFlags(flags)

	expected := []string{"cloud", "engine", "interactive", "interactive-command", "profile", "shell-command-on-error", "shell-on-error"}
	var count int
	flags.VisitAll(func(*pflag.Flag) { count++ })
	require.Equal(t, len(expected), count)
	for _, name := range expected {
		flag := flags.Lookup(name)
		require.NotNil(t, flag, name)
		require.Equal(t, []string{string(mayCallEngine)}, flag.Annotations[flagCapabilitiesAnnotation], name)
	}
	require.Equal(t, "i", flags.Lookup("shell-on-error").Shorthand)
	require.True(t, flags.Lookup("cloud").Hidden)
	require.Contains(t, flags.Lookup("cloud").Deprecated, "--engine=cloud")
	require.False(t, flags.Lookup("engine").Hidden)
	require.False(t, flags.Lookup("shell-on-error").Hidden)
	require.True(t, flags.Lookup("interactive").Hidden)
	require.Contains(t, flags.Lookup("interactive").Deprecated, "--shell-on-error")
	require.True(t, flags.Lookup("shell-command-on-error").Hidden)
	require.True(t, flags.Lookup("interactive-command").Hidden)
	require.Contains(t, flags.Lookup("interactive-command").Deprecated, "--shell-command-on-error")
	require.True(t, flags.Lookup("profile").Hidden)
	require.NoError(t, flags.Set("cloud", "true"))
	require.True(t, useCloudEngine)
	require.NoError(t, flags.Set("engine", "tcp://engine.example.com:1234"))
	require.Equal(t, "tcp://engine.example.com:1234", engineFlag)

	// Every deprecated alias still drives the same variables.
	for _, name := range []string{"interactive", "shell-on-error"} {
		shellOnError = false
		require.NoError(t, flags.Set(name, "true"))
		require.True(t, shellOnError, name)
	}
	for _, name := range []string{"interactive-command", "shell-command-on-error"} {
		shellCommandOnError = ""
		require.NoError(t, flags.Set(name, "/bin/bash"))
		require.Equal(t, "/bin/bash", shellCommandOnError, name)
	}

	for _, name := range expected {
		flag := flags.Lookup(name)
		var visit func(*cobra.Command)
		visit = func(cmd *cobra.Command) {
			require.Equal(t, commandHasCapability(cmd, mayCallEngine), FlagAvailableForCommand(cmd, flag), "%s: --%s", cmd.CommandPath(), name)
			for _, child := range cmd.Commands() {
				visit(child)
			}
		}
		visit(rootCmd)
	}

	require.True(t, FlagAvailableForCommand(settingsCmd, flags.Lookup("shell-on-error")))
	require.False(t, FlagAvailableForCommand(traceCmd, flags.Lookup("shell-on-error")))
	require.False(t, FlagAvailableForCommand(activityCmd, flags.Lookup("shell-on-error")))
	require.False(t, FlagAvailableForCommand(sdkInstalledCmd, flags.Lookup("shell-on-error")))
}

func TestWorkspaceConfigFlags(t *testing.T) {
	oldEnv := workspaceEnv
	t.Cleanup(func() { workspaceEnv = oldEnv })

	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	installGlobalFlags(flags)

	flag := flags.Lookup("env")
	require.NotNil(t, flag)
	require.Equal(t, []string{
		string(mayReadWorkspaceConfig),
		string(mayWriteWorkspaceConfig),
	}, flag.Annotations[flagAnyCapabilitiesAnnotation])
	require.False(t, flag.Hidden)

	read := &cobra.Command{Use: "read"}
	setCommandCapabilities(read, mayReadWorkspaceConfig)
	require.True(t, FlagAvailableForCommand(read, flag))
	write := &cobra.Command{Use: "write"}
	setCommandCapabilities(write, mayWriteWorkspaceConfig)
	require.True(t, FlagAvailableForCommand(write, flag))
	require.False(t, FlagAvailableForCommand(&cobra.Command{Use: "plain"}, flag))
}

func TestMayCallEngineCommands(t *testing.T) {
	expected := []string{
		"dagger",
		"dagger agent",
		"dagger api call",
		"dagger api client init",
		"dagger api client list",
		"dagger api functions",
		"dagger api listen",
		"dagger api query",
		"dagger api session",
		"dagger api with-session",
		"dagger call",
		"dagger check",
		"dagger core",
		"dagger functions",
		"dagger generate",
		"dagger install",
		"dagger installed",
		"dagger listen",
		"dagger mcp",
		"dagger module deps add",
		"dagger module deps list",
		"dagger module deps rm",
		"dagger module deps update",
		"dagger module engine require",
		"dagger module engine require-current",
		"dagger module engine require-latest",
		"dagger module engine required",
		"dagger module init",
		"dagger module sdk",
		"dagger query",
		"dagger run",
		"dagger sdk client-options",
		"dagger sdk install",
		"dagger sdk module-options",
		"dagger sdk uninstall",
		"dagger session",
		"dagger settings",
		"dagger setup",
		"dagger shell",
		"dagger terminal",
		"dagger uninstall",
		"dagger up",
		"dagger update",
		"dagger workspace",
		"dagger workspace config",
		"dagger workspace config-file",
		"dagger workspace cwd",
		"dagger workspace remotes",
		"dagger workspace root",
		"dagger workspace settings",
	}
	require.ElementsMatch(t, expected, commandsDeclaringCapability(rootCmd, mayCallEngine))

	for name, cmd := range map[string]*cobra.Command{
		"activity":         activityCmd,
		"cloud rerun":      cloudRerunCmd,
		"sdk installed":    sdkInstalledCmd,
		"trace":            traceCmd,
		"workspace remote": workspaceRemoteCmd,
	} {
		require.False(t, commandHasCapability(cmd, mayCallEngine), name)
	}
}

func TestMaySelectWorkspaceCommands(t *testing.T) {
	expected := append(commandsDeclaringCapability(rootCmd, mayCallEngine),
		"dagger activity",
		"dagger cloud check",
		"dagger cloud rerun",
		"dagger workspace remote",
	)
	require.ElementsMatch(t, expected, commandsDeclaringCapability(rootCmd, maySelectWorkspace))

	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if commandHasCapability(cmd, mayCallEngine) {
			require.True(t, commandHasCapability(cmd, maySelectWorkspace), cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(rootCmd)

	for name, cmd := range map[string]*cobra.Command{
		"activity":           activityCmd,
		"cloud check list":   cloudCheckListCmd,
		"cloud check status": cloudCheckStatusCmd,
		"cloud rerun":        cloudRerunCmd,
		"workspace remote":   workspaceRemoteCmd,
	} {
		require.True(t, commandHasCapability(cmd, maySelectWorkspace), name)
		require.False(t, commandHasCapability(cmd, mayCallEngine), name)
	}
	for name, cmd := range map[string]*cobra.Command{
		"cloud login":   cloudLoginCmd,
		"sdk installed": sdkInstalledCmd,
		"sdk search":    sdkSearchCmd,
		"trace":         traceCmd,
	} {
		require.False(t, commandHasCapability(cmd, maySelectWorkspace), name)
	}
}

func TestMayProduceOutputCommands(t *testing.T) {
	expected := []string{
		"dagger api call",
		"dagger api client init",
		"dagger call",
		"dagger core",
		"dagger generate",
		"dagger module init",
		"dagger setup",
	}
	require.ElementsMatch(t, expected, commandsDeclaringCapability(rootCmd, mayProduceOutput))

	oldAutoApply := autoApply
	t.Cleanup(func() { autoApply = oldAutoApply })
	flags := pflag.NewFlagSet("output", pflag.ContinueOnError)
	installGlobalFlags(flags)
	autoApplyFlag := flags.Lookup("auto-apply")
	require.NotNil(t, autoApplyFlag)
	for name, cmd := range map[string]*cobra.Command{
		"api call":        apiCallCmd.Command(),
		"api client init": apiClientInitCmd,
		"call":            callModCmd.Command(),
		"core":            callCoreCmd.Command(),
		"generate":        generateCmd,
		"module init":     moduleInitCmd,
		"setup":           setupCmd,
	} {
		require.True(t, FlagAvailableForCommand(cmd, autoApplyFlag), name)
	}
	for name, cmd := range map[string]*cobra.Command{
		"api functions": apiFunctionsCmd,
		"check":         checksCmd,
		"sdk installed": sdkInstalledCmd,
		"trace":         traceCmd,
	} {
		require.False(t, commandHasCapability(cmd, mayProduceOutput), name)
		require.False(t, FlagAvailableForCommand(cmd, autoApplyFlag), name)
	}

	for name, cmd := range map[string]*cobra.Command{
		"api call": apiCallCmd.Command(),
		"call":     callModCmd.Command(),
		"core":     callCoreCmd.Command(),
	} {
		flag := cmd.PersistentFlags().Lookup("output")
		require.NotNil(t, flag, name)
		require.Equal(t, "o", flag.Shorthand, name)
		require.Equal(t, []string{string(mayProduceOutput)}, flag.Annotations[flagCapabilitiesAnnotation], name)
		require.True(t, FlagAvailableForCommand(cmd, flag), name)
	}
	for name, cmd := range map[string]*cobra.Command{
		"api client init": apiClientInitCmd,
		"generate":        generateCmd,
		"module init":     moduleInitCmd,
		"setup":           setupCmd,
	} {
		require.Nil(t, cmd.Flags().Lookup("output"), name)
	}
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

func TestWorkspaceConfigCommands(t *testing.T) {
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	installGlobalFlags(flags)
	envFlag := flags.Lookup("env")
	require.NotNil(t, envFlag)
	require.True(t, FlagAvailableForCommand(moduleInitCmd, envFlag))
	require.True(t, FlagAvailableForCommand(apiClientInitCmd, envFlag))
	require.True(t, FlagAvailableForCommand(sdkInstalledCmd, envFlag))
	require.False(t, FlagAvailableForCommand(setupCmd, envFlag))
	require.False(t, FlagAvailableForCommand(sdkInstallCmd, envFlag))
	require.False(t, FlagAvailableForCommand(workspaceRootCmd, envFlag))

	readers := []string{
		"dagger",
		"dagger agent",
		"dagger api call",
		"dagger api client init",
		"dagger api client list",
		"dagger api functions",
		"dagger api listen",
		"dagger api query",
		"dagger api session",
		"dagger api with-session",
		"dagger call",
		"dagger check",
		"dagger core",
		"dagger functions",
		"dagger generate",
		"dagger install",
		"dagger installed",
		"dagger listen",
		"dagger mcp",
		"dagger module deps add",
		"dagger module deps list",
		"dagger module deps rm",
		"dagger module deps update",
		"dagger module engine require",
		"dagger module engine require-current",
		"dagger module engine require-latest",
		"dagger module engine required",
		"dagger module init",
		"dagger module sdk",
		"dagger query",
		"dagger run",
		"dagger sdk client-options",
		"dagger sdk installed",
		"dagger sdk module-options",
		"dagger session",
		"dagger settings",
		"dagger shell",
		"dagger terminal",
		"dagger uninstall",
		"dagger up",
		"dagger update",
		"dagger workspace",
		"dagger workspace config",
		"dagger workspace settings",
	}
	require.ElementsMatch(t, readers, commandsDeclaringCapability(rootCmd, mayReadWorkspaceConfig))

	writers := []string{
		"dagger",
		"dagger api client init",
		"dagger install",
		"dagger module init",
		"dagger settings",
		"dagger uninstall",
		"dagger workspace",
		"dagger workspace config",
		"dagger workspace settings",
	}
	require.ElementsMatch(t, writers, commandsDeclaringCapability(rootCmd, mayWriteWorkspaceConfig))

	for name, cmd := range map[string]*cobra.Command{
		"sdk install":      sdkInstallCmd,
		"setup":            setupCmd,
		"workspace root":   workspaceRootCmd,
		"workspace remote": workspaceRemoteCmd,
	} {
		require.False(t, commandHasCapability(cmd, mayReadWorkspaceConfig), name)
		require.False(t, commandHasCapability(cmd, mayWriteWorkspaceConfig), name)
	}
	require.True(t, commandHasCapability(sdkInstalledCmd, mayReadWorkspaceConfig))
	require.False(t, commandHasCapability(sdkInstalledCmd, mayWriteWorkspaceConfig))
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
	require.ElementsMatch(t, expected, commandsDeclaringCapability(rootCmd, mayRenderPipeline))

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
	require.NoError(t, validateFlagCapabilities(disabledRoot, []string{"child", "function", "--progress=plain"}))
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

func TestGlobalFlagParsingStopsAtDynamicArguments(t *testing.T) {
	oldXRelease := xRelease
	t.Cleanup(func() { xRelease = oldXRelease })

	root := &cobra.Command{Use: "root"}
	var cloud bool
	root.PersistentFlags().BoolVar(&cloud, "cloud", false, "Use a Cloud Engine")
	dynamic := &cobra.Command{Use: "dynamic", DisableFlagParsing: true}
	root.AddCommand(dynamic)

	parseGlobalFlags(root, []string{"dynamic", "function", "--cloud"})
	require.False(t, cloud)

	parseGlobalFlags(root, []string{"--cloud", "dynamic", "function"})
	require.True(t, cloud)
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

func commandsDeclaringCapability(root *cobra.Command, capability commandCapability) []string {
	var commands []string
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		name := string(capability)
		inherited := strings.Fields(cmd.Annotations[commandCapabilitiesAnnotation])
		local := strings.Fields(cmd.Annotations[localCommandCapabilitiesAnnotation])
		if slices.Contains(inherited, name) || slices.Contains(local, name) {
			commands = append(commands, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
	return commands
}

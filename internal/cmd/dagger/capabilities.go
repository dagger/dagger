package daggercmd

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dagger/dagger/internal/cobradocs"
)

type commandCapability string

const (
	mayCallEngine           commandCapability = "MayCallEngine"
	maySelectWorkspace      commandCapability = "MaySelectWorkspace"
	mayReadWorkspaceConfig  commandCapability = "MayReadWorkspaceConfig"
	mayWriteWorkspaceConfig commandCapability = "MayWriteWorkspaceConfig"
	mayRenderPipeline       commandCapability = "MayRenderPipeline"
	mayProduceOutput        commandCapability = "MayProduceOutput"

	commandCapabilitiesAnnotation      = "dagger.io/command-capabilities"
	localCommandCapabilitiesAnnotation = "dagger.io/local-command-capabilities"
	flagCapabilitiesAnnotation         = "dagger.io/flag-capabilities"
	flagAnyCapabilitiesAnnotation      = "dagger.io/flag-any-capabilities"
)

// setCommandCapabilities declares behavior that a command can expose. A
// capability declared by a parent applies to its subcommands.
func setCommandCapabilities(cmd *cobra.Command, capabilities ...commandCapability) {
	addCommandCapabilities(cmd, commandCapabilitiesAnnotation, capabilities...)
}

// setLocalCommandCapabilities declares behavior for only cmd, without making
// the capability available to its subcommands.
func setLocalCommandCapabilities(cmd *cobra.Command, capabilities ...commandCapability) {
	addCommandCapabilities(cmd, localCommandCapabilitiesAnnotation, capabilities...)
}

func addCommandCapabilities(cmd *cobra.Command, annotation string, capabilities ...commandCapability) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	declared := strings.Fields(cmd.Annotations[annotation])
	for _, capability := range capabilities {
		name := string(capability)
		if !slices.Contains(declared, name) {
			declared = append(declared, name)
		}
	}
	cmd.Annotations[annotation] = strings.Join(declared, " ")
}

func commandHasCapability(cmd *cobra.Command, capability commandCapability) bool {
	name := string(capability)
	if slices.Contains(strings.Fields(cmd.Annotations[localCommandCapabilitiesAnnotation]), name) {
		return true
	}
	for current := cmd; current != nil; current = current.Parent() {
		if slices.Contains(strings.Fields(current.Annotations[commandCapabilitiesAnnotation]), name) {
			return true
		}
	}
	return false
}

func setFlagSetCapabilities(flags *pflag.FlagSet, capabilities ...commandCapability) {
	flags.VisitAll(func(flag *pflag.Flag) {
		setFlagCapabilities(flag, capabilities...)
	})
}

// setFlagCapabilities declares the capabilities a command must all have for
// the flag to be available on it.
func setFlagCapabilities(flag *pflag.Flag, capabilities ...commandCapability) {
	addFlagCapabilities(flag, flagCapabilitiesAnnotation, capabilities...)
}

// setFlagAnyCapabilities declares the capabilities of which a command must
// have at least one for the flag to be available on it.
func setFlagAnyCapabilities(flag *pflag.Flag, capabilities ...commandCapability) {
	addFlagCapabilities(flag, flagAnyCapabilitiesAnnotation, capabilities...)
}

func addFlagCapabilities(flag *pflag.Flag, annotation string, capabilities ...commandCapability) {
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	for _, capability := range capabilities {
		value := string(capability)
		if !slices.Contains(flag.Annotations[annotation], value) {
			flag.Annotations[annotation] = append(flag.Annotations[annotation], value)
		}
	}
}

// FlagAvailableForCommand reports whether a flag's required capabilities are
// available on cmd. It is exported for CLI reference generation.
func FlagAvailableForCommand(cmd *cobra.Command, flag *pflag.Flag) bool {
	for _, required := range flag.Annotations[flagCapabilitiesAnnotation] {
		if !commandHasCapability(cmd, commandCapability(required)) {
			return false
		}
	}
	if any := flag.Annotations[flagAnyCapabilitiesAnnotation]; len(any) > 0 {
		for _, required := range any {
			if commandHasCapability(cmd, commandCapability(required)) {
				return true
			}
		}
		return false
	}
	return true
}

func availableFlagsForCommand(cmd *cobra.Command, flags *pflag.FlagSet) *pflag.FlagSet {
	available := pflag.NewFlagSet(flags.Name(), pflag.ContinueOnError)
	available.SortFlags = flags.SortFlags
	flags.VisitAll(func(flag *pflag.Flag) {
		if FlagAvailableForCommand(cmd, flag) {
			available.AddFlag(flag)
		}
	})
	return available
}

type ignoredFlagValue struct {
	pflag.Value
}

func (ignoredFlagValue) Set(string) error {
	return nil
}

func resolveCommand(root *cobra.Command, args []string) (*cobra.Command, []string) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd(args...)
	// Cobra reports resolution errors during Execute. Policy checks use the
	// best command match available here.
	cmd, commandArgs, _ := root.Find(args)
	return cmd, commandArgs
}

func copyCommandFlags(cmd *cobra.Command, name string) *pflag.FlagSet {
	flags := pflag.NewFlagSet(name, pflag.ContinueOnError)
	flags.ParseErrorsAllowlist.UnknownFlags = true
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		clone := *flag
		clone.Changed = false
		flags.AddFlag(&clone)
	})
	return flags
}

// validateFlagCapabilities resolves the selected command and its flags without
// changing flag values. It must run before parseGlobalFlags, because those
// values configure the frontend and can terminate the process.
func validateFlagCapabilities(root *cobra.Command, args []string) error {
	cmd, commandArgs := resolveCommand(root, args)
	if cmd == nil {
		return nil
	}

	parsed := copyCommandFlags(cmd, "capabilities")
	parsed.VisitAll(func(flag *pflag.Flag) {
		flag.Value = ignoredFlagValue{Value: flag.Value}
	})
	// The real parser reports syntax and value errors. This pass only records
	// the flags that it can parse without changing their values.
	_ = parsed.Parse(commandArgs)

	var unsupported []string
	parsed.Visit(func(flag *pflag.Flag) {
		if !FlagAvailableForCommand(cmd, flag) {
			unsupported = append(unsupported, "--"+flag.Name)
		}
	})
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	if len(unsupported) == 1 {
		return fmt.Errorf("flag %s is not supported by command %q", unsupported[0], cmd.CommandPath())
	}
	return fmt.Errorf("flags %s are not supported by command %q", strings.Join(unsupported, ", "), cmd.CommandPath())
}

func hideUnavailableCompletionFlags(cmd *cobra.Command, args []string) func() {
	if cmd.Name() != cobra.ShellCompRequestCmd || len(args) == 0 {
		return nil
	}

	target, _, err := cmd.Root().Find(args[:len(args)-1])
	if err != nil || target == nil {
		return nil
	}
	return cobradocs.HideFlags(target, func(flag *pflag.Flag) bool {
		return FlagAvailableForCommand(target, flag)
	})
}

func init() {
	setLocalCommandCapabilities(rootCmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig, mayWriteWorkspaceConfig, mayRenderPipeline)
	setLocalCommandCapabilities(workspaceCmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig, mayWriteWorkspaceConfig)

	for _, cmd := range []*cobra.Command{
		checksCmd,
		generateCmd,
		upCmd,
		agentCmd,
		apiCallCmd.Command(),
		callModCmd.Command(),
		callCoreCmd.Command(),
		apiQueryCmd,
		queryCmd,
		apiWithSessionCmd,
		runCmd,
		apiListenCmd,
		listenCmd,
		apiSessionCmd,
		sessionAliasCmd,
		shellCmd,
		terminalCmd,
		mcpCmd,
		moduleSdkCmd,
	} {
		setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig, mayRenderPipeline)
	}
	setCommandCapabilities(traceCmd, mayRenderPipeline)

	for _, cmd := range []*cobra.Command{
		moduleDepInstallCmd,
		moduleDepUninstallCmd,
		settingsCmd,
		workspaceSettingsCmd,
		workspaceConfigCmd,
		moduleInitCmd,
		apiClientInitCmd,
	} {
		setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig, mayWriteWorkspaceConfig)
	}

	for _, cmd := range []*cobra.Command{
		apiFunctionsCmd,
		functionsAliasCmd,
		moduleUpdateCmd,
		installedCmd,
		moduleDepsAddCmd,
		moduleDepsRmCmd,
		moduleDepsUpdateCmd,
		moduleDepsListCmd,
		moduleEngineRequiredCmd,
		moduleEngineRequireCmd,
		moduleEngineRequireLatestCmd,
		moduleEngineRequireCurrentCmd,
		apiClientListCmd,
		sdkModuleOptionsCmd,
		sdkClientOptionsCmd,
	} {
		setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	}
	setCommandCapabilities(sdkInstalledCmd, mayReadWorkspaceConfig)

	for _, cmd := range []*cobra.Command{
		workspaceRootCmd,
		workspaceCwdCmd,
		workspaceConfigFileCmd,
		workspaceRemotesCmd,
		sdkInstallCmd,
		sdkUninstallCmd,
		setupCmd,
	} {
		setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace)
	}

	for _, cmd := range []*cobra.Command{
		activityCmd,
		cloudCheckCmd,
		cloudRerunCmd,
		workspaceRemoteCmd,
	} {
		setCommandCapabilities(cmd, maySelectWorkspace)
	}

	for _, cmd := range []*cobra.Command{
		generateCmd,
		apiCallCmd.Command(),
		callModCmd.Command(),
		callCoreCmd.Command(),
		moduleInitCmd,
		apiClientInitCmd,
		setupCmd,
	} {
		setCommandCapabilities(cmd, mayProduceOutput)
	}
}

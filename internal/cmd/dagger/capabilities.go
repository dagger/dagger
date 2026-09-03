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
	mayRenderPipeline commandCapability = "MayRenderPipeline"

	commandCapabilitiesAnnotation = "dagger.io/command-capabilities"
	flagCapabilitiesAnnotation    = "dagger.io/flag-capabilities"
)

// setCommandCapabilities declares behavior that a command can expose. A
// capability declared by a non-root parent applies to its subcommands. A root
// capability applies only to the root command itself, so it does not make the
// capability global again.
func setCommandCapabilities(cmd *cobra.Command, capabilities ...commandCapability) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	declared := strings.Fields(cmd.Annotations[commandCapabilitiesAnnotation])
	for _, capability := range capabilities {
		name := string(capability)
		if !slices.Contains(declared, name) {
			declared = append(declared, name)
		}
	}
	cmd.Annotations[commandCapabilitiesAnnotation] = strings.Join(declared, " ")
}

func commandHasCapability(cmd *cobra.Command, capability commandCapability) bool {
	selected := cmd
	for current := cmd; current != nil; current = current.Parent() {
		// The root command can still run legacy shell input directly, but its
		// capability must not be inherited by every subcommand.
		if current != selected && current.Parent() == nil {
			continue
		}
		if slices.Contains(strings.Fields(current.Annotations[commandCapabilitiesAnnotation]), string(capability)) {
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

// setFlagCapabilities declares the capabilities a command must have for the
// flag to be available on it.
func setFlagCapabilities(flag *pflag.Flag, capabilities ...commandCapability) {
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	for _, capability := range capabilities {
		value := string(capability)
		if !slices.Contains(flag.Annotations[flagCapabilitiesAnnotation], value) {
			flag.Annotations[flagCapabilitiesAnnotation] = append(flag.Annotations[flagCapabilitiesAnnotation], value)
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
	for _, cmd := range []*cobra.Command{
		rootCmd,
		checksCmd,
		generateCmd,
		upCmd,
		agentCmd,
		traceCmd,
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
		setCommandCapabilities(cmd, mayRenderPipeline)
	}
}

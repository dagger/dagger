package daggercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/google/shlex"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

// prepareCommandExecution runs after Cobra handles help, flags, and arguments.
// Schema-driven commands still need their global flags checked before loading
// the schema; other commands use the flags Cobra has already parsed.
func prepareCommandExecution(cmd *cobra.Command, args []string) error {
	// Cobra has accepted the command line; runtime failures need no usage.
	cmd.SilenceUsage = true
	if err := changeWorkingDirectory(); err != nil {
		return err
	}
	err := validateParsedFlagCapabilities(cmd, cmd.Flags())
	if err == nil && cmd.DisableFlagParsing {
		err = validateFlagCapabilities(cmd, args)
	}
	if err != nil {
		return cmd.FlagErrorFunc()(cmd, err)
	}
	opts.Silent = silent                   // show no progress
	opts.Debug = debugFlag                 // show everything
	opts.RevealNoisySpans = reveal         // disable 'reveal: true' mechanic (for tests)
	opts.ExpandCompleted = expandCompleted // leave things expanded as they complete
	opts.OpenWeb = web
	opts.NoExit = noExit
	opts.DotOutputFilePath = dotOutputFilePath
	opts.DotFocusField = dotFocusField
	opts.DotShowInternal = dotShowInternal
	opts.UsingCloudEngine = strings.HasPrefix(configuredRunnerHost(), engine.CloudRunnerHostPrefix)
	if progress == "auto" {
		if env := os.Getenv("DAGGER_PROGRESS"); env != "" {
			progress = env
		} else if def := commandProgressDefault(os.Args[1:]); def != "" {
			// The command declares its own default (e.g. `dagger session`
			// keeps plain progress for its SDK consumers). Checked before
			// RunningInAgent: an agent-driven SDK program needs the stream
			// just as much.
			progress = def
		} else if idtui.RunningInAgent() {
			// An AI agent consumes the output as text; the report frontend's
			// single final render suits it better than the live TUI.
			progress = "report"
		} else if hasTTY {
			progress = "tty"
		} else {
			progress = "report"
		}
	}
	if silent {
		// if silent, don't even bother with the pretty frontend
		progress = "plain"
	}
	// DAGGER_TUI_CONSOLE=<addr> serves the pretty TUI over HTTP (headless), so
	// force it regardless of progress mode / tty (it doesn't need one).
	if os.Getenv("DAGGER_TUI_CONSOLE") != "" {
		progress = "tty"
		hasTTY = true
	}
	switch progress {
	case "plain":
		Frontend = idtui.NewPlain(stderr)
	case "tty":
		if !hasTTY {
			return fmt.Errorf("no tty available for progress %q", progress)
		}
		Frontend = idtui.NewPretty(stderr)
	case "dots":
		Frontend = idtui.NewDots(stderr)
	case "logs":
		Frontend = idtui.NewLogs(stderr)
	case "report":
		Frontend = idtui.NewReporter(stderr)
	default:
		return fmt.Errorf("unknown progress type %q", progress)
	}

	if shellOnError && !canOpenShellOnError(progress, stdinIsTTY) {
		return fmt.Errorf("--shell-on-error needs an interactive terminal, but none is available")
	}

	// Parse the shell command to support shell-like syntax.
	parsedCommand, err := shlex.Split(shellCommandOnError)
	if err != nil {
		return fmt.Errorf("cannot parse --shell-command-on-error: %w", err)
	}
	shellCommandOnErrorParsed = parsedCommand

	ctx := slog.ContextWithColorMode(cmd.Context(), termenv.EnvNoColor())
	ctx = slog.ContextWithDebugMode(ctx, debugFlag)

	cmd.SetContext(ctx)
	return nil
}

func changeWorkingDirectory() error {
	resolved, err := NormalizeWorkdir(workdir)
	if err != nil {
		return err
	}
	if err := os.Chdir(resolved); err != nil {
		return fmt.Errorf("change workdir: %w", err)
	}
	workdir = resolved
	return nil
}

const commandGroupAnnotation = "dagger.io/command-group"

// These groups have handlers that only show help. Mark them so they skip
// execution setup, just like groups that Cobra handles without a Run function.
func installCommandGroups() {
	for _, cmd := range []*cobra.Command{
		moduleCmd, moduleInitCmd, moduleClientCmd, sdkCmd, sdkScopeCmd,
		cloudBillingCmd, billingCmd, cloudCheckCmd, cloudIntegrationCmd, cloudOrgCmd, orgCmd,
	} {
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[commandGroupAnnotation] = "true"
	}
}

// Root positional arguments name a script file. Validate them before execution
// setup, resolving relative paths as they will be after --workdir is applied.
func validateRootArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	path := args[0]
	if !filepath.IsAbs(path) {
		dir, err := NormalizeWorkdir(workdir)
		if err != nil {
			return err
		}
		path = filepath.Join(dir, path)
	}
	if !isFile(path) {
		return fmt.Errorf("unknown command or file %q for %q%s", args[0], cmd.CommandPath(), findSuggestions(cmd, args[0]))
	}
	return nil
}

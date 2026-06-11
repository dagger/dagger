package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// moduleSdkCmd dispatches arbitrary subcommands to the current module's SDK.
//
// Form: `dagger module sdk <subcommand> [args...]`
//
// Locates the cwd module's dagger-module.toml (walking up to the workspace
// root), reads its SDK declaration, derives the workspace-installed name
// (last path segment, version-stripped), and runs
// `dagger call <sdk-name> <subcommand> [args...]` via exec.CommandContext.
// Stdin / stdout / stderr / env are inherited.
//
// The available subcommands depend entirely on what the SDK exposes —
// there's no CLI-side contract beyond "you're an installed module."
//
// Implementation note: this wrapper deliberately does NOT open its own
// engine session for the SDK lookup. The spawned `dagger call` opens its
// own session; opening a parallel one in the parent would double-bootstrap
// the engine and have two Frontends fighting over the same terminal. The
// SDK declaration is read directly from dagger-module.toml on disk.
var moduleSdkCmd = &cobra.Command{
	Use:   "sdk <subcommand> [args...]",
	Short: "Run SDK-specific commands against this module's SDK",
	Long: `Run SDK-specific commands against the current module's SDK.

Reads the SDK from the module's dagger-module.toml and dispatches
through "dagger call <sdk>". Available subcommands depend entirely on
the SDK in use — the wrapper is a thin forwarder.

Examples:
  dagger module sdk python-version 3.13
  dagger module sdk go-mod-tidy
  dagger module sdk python-version --help   # SDK function help (dispatched)
  dagger module sdk --help                  # this wrapper's help
`,
	// All args (and any flags within them) belong to the SDK function,
	// not to this command. Don't let cobra try to parse flags here.
	DisableFlagParsing: true,
	RunE:               runModuleSdk,
}

func runModuleSdk(cmd *cobra.Command, args []string) error {
	// DisableFlagParsing forwards ALL tokens (including parent persistent
	// flags like --load-module or --x-release) into args. The wrapper's
	// "help vs dispatch" decision should not depend on flag noise; key on
	// the first positional (non-flag) token instead. If there isn't one,
	// the user typed a bare `dagger module sdk` (maybe with --help) and
	// wants wrapper help.
	hasSubcommand := false
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			hasSubcommand = true
			break
		}
	}
	if !hasSubcommand {
		return cmd.Help()
	}

	sdkName, err := currentModuleSDKName()
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	// Re-emit the persistent flags the user supplied on this invocation so the
	// spawned `dagger call` runs against the same workspace, env, debug level,
	// progress format, etc. Only flags that were actually set are forwarded;
	// defaults are left implicit so environment variables can still apply.
	forwarded := forwardedPersistentFlags(cmd)
	fullArgs := append(append(forwarded, "call", sdkName), args...)

	// os.Executable resolves through any wrapper / symlink to the binary
	// currently running, so the child re-execs the same dagger build that
	// served the parent invocation. os.Args[0] would resolve via PATH and
	// could land on a different binary.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate dagger binary: %w", err)
	}
	sub := exec.CommandContext(ctx, self, fullArgs...)
	sub.Stdin = os.Stdin
	sub.Stdout = os.Stdout
	sub.Stderr = os.Stderr
	sub.Env = os.Environ()

	if err := sub.Run(); err != nil {
		// Propagate the child's exit code so CI / shell pipelines see
		// the real outcome instead of a flat 1.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return idtui.ExitError{OriginalCode: exitErr.ExitCode()}
		}
		if errors.Is(err, context.Canceled) {
			return idtui.ExitError{OriginalCode: 2}
		}
		return err
	}
	return nil
}

// currentModuleSDKName locates the dagger-module.toml (or legacy dagger.json)
// reachable from cwd and returns the workspace-installed name of its SDK.
// Reads from disk; no engine session needed.
func currentModuleSDKName() (string, error) {
	configPath, err := findModuleConfigUpward()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read module config %q: %w", configPath, err)
	}
	cfg, err := modules.ParseModuleConfigForFilename(data, configPath)
	if err != nil {
		return "", fmt.Errorf("parse module config %q: %w", configPath, err)
	}
	if cfg.SDK == nil || strings.TrimSpace(cfg.SDK.Source) == "" {
		return "", fmt.Errorf("module %q has no SDK declared in its config", configPath)
	}
	// FIXME: per the runtime/SDK split, the SDK association is no longer
	// stored in dagger-module.toml. This call site looks up the workspace
	// SDK that authors the current module by short-name derivation. Task
	// #108 will replace this with a proper [[sdks.*.modules]] lookup
	// against the workspace config.
	return workspace.ConventionalSDKShortName(cfg.SDK.Source), nil
}

// forwardedPersistentFlags returns the persistent flags (--workspace, --env,
// --debug, --progress, etc.) that were explicitly set on this invocation, in
// `--name=value` form suitable to splice in before `call` when re-executing
// the dagger binary. Skips the help flag (forwarding it would print help for
// the spawned `call`, not the wrapper).
func forwardedPersistentFlags(cmd *cobra.Command) []string {
	var out []string
	// InheritedFlags surfaces every persistent flag the parent commands
	// declared; Visit only fires for flags whose value was explicitly set.
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed || f.Name == "help" {
			return
		}
		// Slice flags expose their elements via SliceValue; emit one
		// --name=value pair per element so pflag re-parses them as a slice.
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				out = append(out, "--"+f.Name+"="+v)
			}
			return
		}
		out = append(out, "--"+f.Name+"="+f.Value.String())
	})
	return out
}

// findModuleConfigUpward walks from cwd toward the filesystem root looking
// for a dagger-module.toml or dagger.json. Stops at the workspace root (the
// nearest dagger.toml) so a stray module config in an ancestor of an
// unrelated workspace can't be picked up.
func findModuleConfigUpward() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	const workspaceMarker = "dagger.toml"
	dir := cwd
	for {
		for _, name := range modules.ConfigFilenames() {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
		// Stop at the workspace boundary, BEFORE ascending past it. If the
		// current dir is itself a workspace root, we've already checked it
		// for a module config above; an outer module would belong to a
		// different workspace.
		if _, err := os.Stat(filepath.Join(dir, workspaceMarker)); err == nil {
			return "", fmt.Errorf("no module config (%s) found from %q up to the workspace root %q; run `dagger module sdk` from inside a module", strings.Join(modules.ConfigFilenames(), " or "), cwd, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no module config (%s) found from %q upward, and no workspace root in any parent; run `dagger module sdk` from inside a module", strings.Join(modules.ConfigFilenames(), " or "), cwd)
}

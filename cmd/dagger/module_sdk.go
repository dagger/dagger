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

// currentModuleSDKName returns the workspace short name of the SDK that
// authors the module reachable from cwd.
//
// Lookup steps:
//  1. Walk up from cwd looking for dagger-module.toml (or the legacy
//     dagger.json). The first match defines the module directory.
//  2. Continue walking up to the workspace root (the nearest dagger.toml).
//  3. Read the workspace config and scan [[sdks.*.modules]] for a path
//     entry matching the module's workspace-relative directory.
//  4. Return the parent SDK short name.
//
// Per the runtime/SDK split, dagger-module.toml no longer records the SDK.
// The authoring relationship lives in workspace config. If the module
// isn't registered under any SDK in dagger.toml, the wrapper can't tell
// which SDK to dispatch to; that's an error the user resolves by
// installing/registering the module via `dagger module init`.
func currentModuleSDKName() (string, error) {
	moduleDir, workspaceRoot, err := locateModuleAndWorkspaceRoot()
	if err != nil {
		return "", err
	}

	modulePath, err := filepath.Rel(workspaceRoot, moduleDir)
	if err != nil {
		return "", fmt.Errorf("compute workspace-relative module path: %w", err)
	}
	modulePath = filepath.ToSlash(filepath.Clean(modulePath))

	wsConfigPath := filepath.Join(workspaceRoot, workspace.ConfigFileName)
	wsData, err := os.ReadFile(wsConfigPath)
	if err != nil {
		return "", fmt.Errorf("read workspace config %q: %w", wsConfigPath, err)
	}
	wsCfg, err := workspace.ParseConfig(wsData)
	if err != nil {
		return "", fmt.Errorf("parse workspace config %q: %w", wsConfigPath, err)
	}

	for sdkName, sdkEntry := range wsCfg.SDKs {
		for _, m := range sdkEntry.Modules {
			if filepath.ToSlash(filepath.Clean(m.Path)) == modulePath {
				return sdkName, nil
			}
		}
	}
	return "", fmt.Errorf("module at %q is not registered under any [[sdks.*.modules]] in %s; register it via `dagger module init` or add an entry by hand", modulePath, wsConfigPath)
}

// locateModuleAndWorkspaceRoot walks upward from cwd, returning the first
// directory that contains a dagger-module.toml (or legacy dagger.json) as
// the module root, plus the first directory that contains a dagger.toml
// at-or-above as the workspace root.
//
// The walker stops climbing once it has both. If the workspace root
// arrives before any module config, the cwd isn't inside a module.
func locateModuleAndWorkspaceRoot() (moduleDir, workspaceRoot string, _ error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}
	dir := cwd
	for {
		if moduleDir == "" {
			for _, name := range modules.ConfigFilenames() {
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					moduleDir = dir
					break
				}
			}
		}
		if _, err := os.Stat(filepath.Join(dir, workspace.ConfigFileName)); err == nil {
			if moduleDir == "" {
				return "", "", fmt.Errorf("no module config (%s) found between %q and the workspace root %q; run `dagger module sdk` from inside a module", strings.Join(modules.ConfigFilenames(), " or "), cwd, dir)
			}
			return moduleDir, dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if moduleDir != "" {
		return "", "", fmt.Errorf("module at %q has no workspace root (no dagger.toml in any parent); `dagger module sdk` requires a workspace", moduleDir)
	}
	return "", "", fmt.Errorf("no module config (%s) and no workspace root found from %q upward", strings.Join(modules.ConfigFilenames(), " or "), cwd)
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


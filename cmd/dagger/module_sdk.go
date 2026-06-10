package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// moduleSdkCmd dispatches arbitrary subcommands to the current module's SDK.
//
// Form: `dagger module sdk <subcommand> [args...]`
//
// Reads the SDK identifier from the cwd module's config (today: the SDK
// field on the ModuleSource). Translates that into the workspace-installed
// name of the SDK module (last path segment, version-stripped), and runs
// `dagger call <sdk-name> <subcommand> [args...]` via os/exec. Stdin /
// stdout / stderr are inherited.
//
// The available subcommands depend entirely on what the SDK exposes —
// there's no CLI-side contract beyond "you're an installed module."
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

	return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, ec *client.Client) error {
		sdkName, err := currentModuleSDKName(ctx, ec.Dagger())
		if err != nil {
			return err
		}

		// Dispatch as `dagger call <sdk-name> <args...>`. Forwarding through
		// the CLI binary keeps the engine session, flag plumbing, and
		// changeset apply flow consistent with what users would type
		// directly.
		fullArgs := append([]string{"call", sdkName}, args...)
		sub := exec.Command(os.Args[0], fullArgs...)
		sub.Stdin = os.Stdin
		sub.Stdout = os.Stdout
		sub.Stderr = os.Stderr
		sub.Env = os.Environ()
		return sub.Run()
	})
}

// currentModuleSDKName resolves the workspace-installed name of the SDK
// for the module reachable from cwd. It reads ModuleSource(".").SDK().Source(),
// which today returns either a short SDK identifier ("go") or a full module
// ref ("github.com/dagger/go-sdk"). Either way, the conventional install
// name is the final path segment (version stripped) — same convention
// `dagger module init` uses when auto-installing an SDK.
func currentModuleSDKName(ctx context.Context, dag *dagger.Client) (string, error) {
	modSrc := dag.ModuleSource(".")
	exists, err := modSrc.ConfigExists(ctx)
	if err != nil {
		return "", fmt.Errorf("load module from cwd: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("no dagger-module.toml found in current directory; run `dagger module sdk` from inside a module")
	}
	sdkSource, err := modSrc.SDK().Source(ctx)
	if err != nil {
		return "", fmt.Errorf("read module SDK: %w", err)
	}
	if strings.TrimSpace(sdkSource) == "" {
		return "", fmt.Errorf("this module has no SDK declared in dagger-module.toml")
	}
	return conventionalSDKModuleName(sdkSource), nil
}

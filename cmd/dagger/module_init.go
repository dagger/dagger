package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// --- private sdks.json registry ---
//
// This is an implementation detail of `dagger module init --sdk=<name>`.
// It is NOT a general-purpose alias registry. Only this file consumes it.
// Adding a new alias is a registry data change here; no other surface
// reaches for `sdks.json`.

//go:embed sdks.json
var embeddedSDKRegistry []byte

type sdkEntry struct {
	Name    string   `json:"name"`    // canonical short name (e.g., "go-sdk")
	Repo    string   `json:"repo"`    // canonical full ref (e.g., "github.com/dagger/go-sdk")
	Aliases []string `json:"aliases"` // user-facing shorthands (e.g., ["go", "golang"])
}

func loadSDKRegistry() ([]sdkEntry, error) {
	var entries []sdkEntry
	if err := json.Unmarshal(embeddedSDKRegistry, &entries); err != nil {
		return nil, fmt.Errorf("parse SDK registry: %w", err)
	}
	return entries, nil
}

// sdkResolve maps a user-supplied `--sdk` value to the canonical full ref
// that should flow downstream (into install, into dagger.toml, etc.).
//
// Resolution rules:
//   - If input contains `/` or `@`, treat as a full ref. Return as-is.
//   - Otherwise look up in sdks.json by name first, then by alias.
//   - 0 matches → error ("not found").
//   - 1 match  → return entry.Repo.
//   - N > 1   → error ("ambiguous"), with candidate names.
//
// Aliases never propagate downstream. Only canonical full refs flow past
// this function.
func sdkResolve(input string) (string, error) {
	if strings.ContainsAny(input, "/@") {
		return input, nil
	}
	entries, err := loadSDKRegistry()
	if err != nil {
		return "", err
	}
	var matches []sdkEntry
	for _, e := range entries {
		if e.Name == input {
			matches = append(matches, e)
			continue
		}
		for _, alias := range e.Aliases {
			if alias == input {
				matches = append(matches, e)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("--sdk=%q not found in registry; try `dagger search %s` or pass a full ref (e.g., github.com/dagger/go-sdk)", input, input)
	case 1:
		return matches[0].Repo, nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		sort.Strings(names)
		return "", fmt.Errorf("--sdk=%q is ambiguous: matches %s. Pick one.", input, strings.Join(names, ", "))
	}
}

// --- dagger module init ---

var (
	moduleInitSDK  string
	moduleInitPath string
)

var moduleInitCmd = &cobra.Command{
	Use:   "init <name> --sdk <sdk>",
	Short: "Initialize a new module in the current directory",
	Long: `Initialize a new module in the workspace.

Steps:
  1. Resolve --sdk via the embedded SDK registry (short names like "go"
     resolve to canonical refs like github.com/dagger/go-sdk; full refs
     pass through unchanged).
  2. Install the SDK module into the workspace if it isn't already.
  3. Create <path>/dagger-module.toml with the module name and SDK
     declaration.

Source scaffolding is NOT done here. After init, run codegen via
` + "`dagger generate`" + ` (the SDK's generate function picks up the
new module). Templates for SDK-specific starter code are a separate
follow-on.

--path defaults to .dagger/modules/<name>.`,
	Example: "dagger module init myapp --sdk go",
	Args:    cobra.ExactArgs(1),
	RunE:    runModuleInit,
}

func init() {
	moduleInitCmd.Flags().StringVar(&moduleInitSDK, "sdk", "", "SDK alias or full ref (e.g., 'go' or 'github.com/dagger/go-sdk')")
	moduleInitCmd.Flags().StringVar(&moduleInitPath, "path", "", "Module path relative to the workspace root (default: .dagger/modules/<name>)")
	_ = moduleInitCmd.MarkFlagRequired("sdk")
	moduleCmd.AddCommand(moduleInitCmd)
}

func runModuleInit(cmd *cobra.Command, args []string) error {
	name := args[0]

	sdkRef, err := sdkResolve(moduleInitSDK)
	if err != nil {
		return err
	}

	path := moduleInitPath
	if path == "" {
		path = filepath.Join(".dagger", "modules", name)
	}

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		out := cmd.OutOrStdout()

		// Step 1: ensure the SDK is installed in this workspace.
		if err := ensureSDKInstalled(ctx, out, dag, sdkRef); err != nil {
			return fmt.Errorf("install SDK: %w", err)
		}

		// Step 2: write dagger-module.toml at the requested path.
		if err := writeModuleConfig(out, path, name, sdkRef); err != nil {
			return fmt.Errorf("write module config: %w", err)
		}

		fmt.Fprintf(out,
			"\nNext: run `dagger generate` to scaffold %s source for %s.\n",
			sdkRef, name,
		)
		return nil
	})
}

// ensureSDKInstalled installs the SDK module into the current workspace if
// it isn't already present. Idempotent: if already installed, prints a
// confirmation and returns nil.
func ensureSDKInstalled(ctx context.Context, out io.Writer, dag *dagger.Client, sdkRef string) error {
	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return err
	}
	// Match by canonical short name (final path segment); this matches the
	// default naming the install path uses when no --name is supplied.
	conventional := conventionalSDKModuleName(sdkRef)
	if installed[conventional] {
		fmt.Fprintf(out, "Using already-installed SDK: %s (as %q)\n", sdkRef, conventional)
		return nil
	}
	fmt.Fprintf(out, "Installing SDK: %s ...\n", sdkRef)
	return installWorkspaceModule(ctx, out, dag, sdkRef, "", false)
}

// conventionalSDKModuleName derives the workspace-side install name from
// an SDK ref. By convention an SDK gets installed as the last segment of
// its repo path (e.g., github.com/dagger/go-sdk → go-sdk). This mirrors
// what `dagger install <ref>` does when no --name is supplied.
func conventionalSDKModuleName(sdkRef string) string {
	// Strip any version suffix.
	if i := strings.Index(sdkRef, "@"); i >= 0 {
		sdkRef = sdkRef[:i]
	}
	// Last path segment.
	if i := strings.LastIndex(sdkRef, "/"); i >= 0 {
		return sdkRef[i+1:]
	}
	return sdkRef
}

// writeModuleConfig creates the dagger-module.toml at <path>/dagger-module.toml.
// The file declares the module's name + SDK. Source scaffolding is left to
// `dagger generate` against the installed SDK.
//
// Today the schema uses `runtime.source` for the SDK identifier. When the
// schema splits runtime (engine) from sdk (tooling) into separate fields,
// this writer updates to match.
func writeModuleConfig(out io.Writer, path, name, sdkRef string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create module directory %q: %w", path, err)
	}
	configPath := filepath.Join(path, "dagger-module.toml")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("module config already exists at %q (refusing to overwrite)", configPath)
	}
	contents := fmt.Sprintf(`name = %q

[runtime]
source = %q
`, name, sdkRef)
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", configPath, err)
	}
	fmt.Fprintf(out, "Wrote %s\n", configPath)
	return nil
}

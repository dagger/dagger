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
	"github.com/dagger/dagger/core/modules"
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
  4. If --path was not set (i.e. using the default .dagger/modules/<name>
     layout), also install the new module into the workspace so it
     shows up in workspace config and is callable. Skipped when --path
     is explicit — that's the signal that the user is managing the
     layout themselves.

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

	// An empty --path means "use the default layout"; remember that distinction
	// so we can decide whether to auto-install the new module afterward.
	usingDefaultPath := moduleInitPath == ""
	relativePath := moduleInitPath
	if usingDefaultPath {
		relativePath = filepath.Join(".dagger", "modules", name)
	}

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		out := cmd.OutOrStdout()

		// Resolve --path relative to the workspace root (not the CLI's cwd).
		// Absolute --path values are used as-is.
		filesystemPath, err := resolveWorkspacePath(ctx, dag, relativePath)
		if err != nil {
			return err
		}

		// Step 1: ensure the SDK is installed in this workspace.
		if err := ensureSDKInstalled(ctx, out, dag, sdkRef); err != nil {
			return fmt.Errorf("install SDK: %w", err)
		}

		// Step 2: write dagger-module.toml at the workspace-resolved path.
		if err := writeModuleConfig(out, filesystemPath, name, sdkRef); err != nil {
			return fmt.Errorf("write module config: %w", err)
		}

		// Step 3: with the default layout, also install the new module into
		// the workspace so it's registered in dagger.toml. With a custom
		// --path, leave layout decisions to the user. The install ref is the
		// workspace-relative path so the engine resolves it correctly even
		// when the CLI's cwd is elsewhere in the workspace.
		if usingDefaultPath {
			fmt.Fprintf(out, "Installing module: %s ...\n", name)
			if err := installWorkspaceModule(ctx, out, dag, relativePath, name, false); err != nil {
				return fmt.Errorf("install new module: %w", err)
			}
		} else {
			fmt.Fprintln(out, "Skipping workspace install (custom --path; manage it yourself with `dagger install`).")
		}

		fmt.Fprintf(out,
			"\nNext: run `dagger generate` to scaffold %s source for %s.\n",
			sdkRef, name,
		)
		return nil
	})
}

// resolveWorkspacePath turns a CLI-supplied --path into a host filesystem
// path rooted at the workspace, not at the CLI's cwd. Absolute paths pass
// through unchanged. The dagger module commands are workspace-scoped: a
// user running `dagger module init foo --path=./things` from any
// subdirectory of the workspace expects `./things` to mean
// "<workspace-root>/things", not "<cwd>/things".
func resolveWorkspacePath(ctx context.Context, dag *dagger.Client, relPath string) (string, error) {
	wsRoot, err := currentWorkspaceExportPath(ctx, dag.CurrentWorkspace())
	if err != nil {
		return "", fmt.Errorf("locate workspace root: %w", err)
	}
	var resolved string
	if filepath.IsAbs(relPath) {
		resolved = filepath.Clean(relPath)
	} else {
		resolved = filepath.Clean(filepath.Join(wsRoot, relPath))
	}

	// Refuse paths that escape the workspace root. A module installed
	// outside the workspace would silently fail to be picked up later by
	// `dagger install` (which writes its entry into dagger.toml under a
	// workspace-relative key). Symlinks aren't followed here — workspaces
	// are normal source trees, not chroot jails.
	absRoot, err := filepath.Abs(wsRoot)
	if err != nil {
		return "", fmt.Errorf("workspace root absolute path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--path %q escapes workspace root %q; module paths must live inside the workspace", relPath, absRoot)
	}
	return resolved, nil
}

// ensureSDKInstalled installs the SDK module into the current workspace if
// it isn't already present. Idempotent: if already installed (matched by
// canonical source ref, not by short name), prints a confirmation and
// returns nil.
//
// Source-ref matching matters: a workspace could already have a module
// installed under the conventional name (e.g. "go-sdk") that points at a
// fork (github.com/myorg/go-sdk). Matching by basename would silently use
// the fork; the install loop wouldn't run. Match by repo URL instead so
// the caller gets the SDK they asked for or a clear conflict signal.
func ensureSDKInstalled(ctx context.Context, out io.Writer, dag *dagger.Client, sdkRef string) error {
	installedByName, installedBySource, err := installedModules(ctx, dag)
	if err != nil {
		return err
	}

	// Strip @version when comparing — installed modules typically carry the
	// resolved ref without the user-supplied version qualifier.
	wantSource := sdkRef
	if i := strings.Index(wantSource, "@"); i >= 0 {
		wantSource = wantSource[:i]
	}

	for source, name := range installedBySource {
		base := source
		if i := strings.Index(base, "@"); i >= 0 {
			base = base[:i]
		}
		if base == wantSource {
			fmt.Fprintf(out, "Using already-installed SDK: %s (as %q)\n", sdkRef, name)
			return nil
		}
	}

	// No source-ref match. If something is squatting the conventional name
	// from a different repo, refuse rather than silently shadow it.
	conventional := conventionalSDKModuleName(sdkRef)
	if existingSource, taken := installedByName[conventional]; taken {
		return fmt.Errorf(
			"module name %q is already taken in this workspace by %q (different from --sdk=%s); pick a different SDK or uninstall the existing module first",
			conventional, existingSource, sdkRef,
		)
	}

	fmt.Fprintf(out, "Installing SDK: %s ...\n", sdkRef)
	return installWorkspaceModule(ctx, out, dag, sdkRef, "", false)
}

// installedModules returns two views of the installed modules in the
// current workspace: name → source, and source → name. The maps share data;
// callers pick whichever direction they need.
func installedModules(ctx context.Context, dag *dagger.Client) (byName, bySource map[string]string, _ error) {
	var res struct {
		CurrentWorkspace struct {
			ModuleList []struct {
				Name   string
				Source string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { moduleList { name source } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, nil, fmt.Errorf("list installed modules: %w", err)
	}
	byName = make(map[string]string, len(res.CurrentWorkspace.ModuleList))
	bySource = make(map[string]string, len(res.CurrentWorkspace.ModuleList))
	for _, m := range res.CurrentWorkspace.ModuleList {
		byName[m.Name] = m.Source
		bySource[m.Source] = m.Name
	}
	return byName, bySource, nil
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
// Today the schema uses `runtime.source` for the SDK identifier (the SDK type
// marshals as "sdk" in legacy JSON and "runtime" in TOML — see
// core/modules/config.go). When the schema gains an explicit `sdk` field
// distinct from `runtime`, this writer updates to match.
//
// Refuses to overwrite an existing dagger-module.toml AND refuses to land
// alongside a legacy dagger.json — the user would silently end up with two
// configs at one path and `selectFoundModuleConfig`'s tie-breaker would hide
// the legacy file. In that case, point them at `dagger setup` to migrate.
//
// The write itself is atomic (tmp file + rename) so a crash mid-write
// doesn't leave a half-written TOML that blocks the next init.
func writeModuleConfig(out io.Writer, path, name, sdkRef string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create module directory %q: %w", path, err)
	}
	configPath := filepath.Join(path, modules.Filename)
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("module config already exists at %q (refusing to overwrite)", configPath)
	}
	legacyPath := filepath.Join(path, modules.LegacyFilename)
	if _, err := os.Stat(legacyPath); err == nil {
		return fmt.Errorf("a legacy %s exists at %q; run `dagger setup` to migrate it before adding a new module here", modules.LegacyFilename, legacyPath)
	}

	cfg := &modules.ModuleConfigWithUserFields{
		ModuleConfig: modules.ModuleConfig{
			Name: name,
			SDK:  &modules.SDK{Source: sdkRef},
		},
	}
	data, err := modules.MarshalModuleConfigForFilename(cfg, configPath)
	if err != nil {
		return fmt.Errorf("marshal module config: %w", err)
	}

	tmpFile, err := os.CreateTemp(path, ".dagger-module.toml.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		cleanup()
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, configPath, err)
	}
	fmt.Fprintf(out, "Wrote %s\n", configPath)
	return nil
}

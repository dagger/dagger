package daggercmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// --- private sdks.json registry ---
//
// This is an implementation detail of `dagger sdk install <name>`. It is NOT
// a general-purpose alias registry. Adding a new alias is a registry data
// change here; no other surface reaches for `sdks.json`.

//go:embed sdks.json
var embeddedSDKRegistry []byte

type sdkEntry struct {
	Name        string   `json:"name"`        // canonical user-facing SDK name (e.g., "go")
	Description string   `json:"description"` // user-facing search description
	Repo        string   `json:"repo"`        // canonical full ref (e.g., "github.com/dagger/go-sdk")
	Aliases     []string `json:"aliases"`     // user-facing shorthands (e.g., ["golang"])
}

func loadSDKRegistry() ([]sdkEntry, error) {
	return parseSDKRegistry(embeddedSDKRegistry)
}

func parseSDKRegistry(data []byte) ([]sdkEntry, error) {
	var entries []sdkEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse SDK registry: %w", err)
	}
	return entries, nil
}

// sdkResolve maps a user-supplied SDK install value to the canonical full ref
// that should flow downstream to the engine.
//
// Resolution rules:
//   - If input contains `/` or `@`, treat as a full ref. Return as-is.
//   - Otherwise look up in sdks.json by name first, then alias, then repo
//     basename as a compatibility fallback (`go-sdk`).
//   - 0 matches → error ("not found").
//   - 1 match  → return entry.Repo.
//   - N > 1   → error ("ambiguous"), with candidate names.
//
// Registry names and aliases affect the CLI-side default install name and the
// SDK alias persisted under [modules.<name>.as-sdk]. Only canonical full refs
// flow past this function.
func sdkResolve(input string) (string, error) {
	ref, _, _, err := sdkResolveInstall(input)
	return ref, err
}

// sdkResolveInstall maps an SDK install value to:
//   - the canonical full ref that should flow downstream to the engine; and
//   - the workspace install name to use when the user did not pass --name: the
//     registry repo basename with a "dagger-" prefix (e.g. "dagger-go-sdk"),
//     reducing the chance of colliding with an unrelated module; and
//   - the registry's canonical user-facing name to persist as as-sdk.name.
//
// Full refs return an empty install name so Workspace.withSDK keeps its normal
// basename-derived behavior for third-party SDK refs. They also return an
// empty SDK name so third-party refs can rely on the module entry name.
func sdkResolveInstall(input string) (ref string, installName string, asSDKName string, _ error) {
	if strings.ContainsAny(input, "/@") {
		return input, "", "", nil
	}
	entries, err := loadSDKRegistry()
	if err != nil {
		return "", "", "", err
	}
	var matches []sdkEntry
	for _, e := range entries {
		if e.Name == input {
			matches = append(matches, e)
			continue
		}
		matched := false
		for _, alias := range e.Aliases {
			if alias == input {
				matches = append(matches, e)
				matched = true
				break
			}
		}
		if !matched && sdkRegistryRepoBase(e.Repo) == input {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return "", "", "", fmt.Errorf("SDK %q not found in registry; try `dagger sdk search %s` or pass a full ref (e.g., github.com/dagger/go-sdk)", input, input)
	case 1:
		return matches[0].Repo, sdkmeta.InstallNamePrefix + sdkRegistryRepoBase(matches[0].Repo), matches[0].Name, nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		sort.Strings(names)
		return "", "", "", fmt.Errorf("SDK %q is ambiguous: matches %s; pick one", input, strings.Join(names, ", "))
	}
}

func sdkRegistryRepoBase(repo string) string {
	if idx := strings.LastIndex(repo, "/"); idx != -1 {
		return repo[idx+1:]
	}
	return repo
}

// --- dagger module init ---

var moduleInitPath string

var moduleInitCmd = &cobra.Command{
	Use:   "init <sdk> <name>",
	Short: "Initialize a new module in the current workspace",
	Long: `Initialize a new module in the workspace.

<sdk> is an SDK installed in this workspace. Run ` + "`dagger sdk install <sdk>`" + `
to add more choices.

For example, after ` + "`dagger sdk install go`" + `, run
` + "`dagger module init go myapp`" + `.

The CLI is a thin wrapper around the engine's Workspace.withInitModule. The
engine validates that <sdk> is installed as an SDK in dagger.toml and returns
an updated workspace that the CLI previews and exports.

What the engine does (atomically, in one Changeset):
  1. Resolves <sdk> to an installed SDK entry and requires its as-sdk marker.
  2. Generates the new module's dagger-module.toml + SDK-emitted source
     scaffold at <path>.
  3. Records [[modules.<sdk-module>.as-sdk.modules]] authoring entry for
     <path>.
  4. When --path is the default (.dagger/modules/<name>), also installs
     the new module as [modules.<name>] so it's callable here.

--path defaults to .dagger/modules/<name>. Custom paths skip the
[modules.<name>] install (the user is managing workspace layout
explicitly).`,
	Example: "dagger module init go myapp",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	moduleInitCmd.PersistentFlags().StringVar(&moduleInitPath, "path", "", "Module path relative to the workspace root (default: .dagger/modules/<name>)")
	moduleCmd.AddCommand(moduleInitCmd)
}

func runModuleInitWithSDK(cmd *cobra.Command, sdkName, name string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		sdkArgs, err := sdkInitArgsJSON(cmd)
		if err != nil {
			return err
		}
		opts := dagger.WorkspaceWithInitModuleOpts{
			Path: moduleInitPath,
		}
		if sdkArgs != "" {
			opts.Args = dagger.JSON(sdkArgs)
		}
		updated := dag.CurrentWorkspace().WithInitModule(name, sdkName, opts)
		_, err = handleWorkspaceResponse(ctx, dag, updated, autoApply)
		return err
	})
}

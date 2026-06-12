package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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
// that should flow downstream to the engine.
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
	Short: "Initialize a new module in the current workspace",
	Long: `Initialize a new module in the workspace.

The CLI is a thin wrapper around the engine's Workspace.moduleInit. It
resolves the --sdk alias against the embedded sdks.json registry, fills
in the default path when --path isn't supplied, and asks the engine to
plan the workspace changes. The engine returns a Changeset that the CLI
previews and applies through the standard changeset apply flow.

What the engine does (atomically, in one Changeset):
  1. Records the SDK install under [modules.<sdk-name>] in dagger.toml
     if it isn't already there.
  2. Generates the new module's dagger-module.toml + SDK-emitted source
     scaffold at <path>.
  3. Records [[modules.<sdk-name>.as-sdk.modules]] authoring entry for
     <path>.
  4. When --path is the default (.dagger/modules/<name>), also installs
     the new module as [modules.<name>] so it's callable here.

--path defaults to .dagger/modules/<name>. Custom paths skip the
[modules.<name>] install (the user is managing workspace layout
explicitly).`,
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

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()

		exportPath, err := currentWorkspaceExportPath(ctx, dag.CurrentWorkspace())
		if err != nil {
			return err
		}

		changesetID, err := callModuleInit(ctx, dag, name, sdkRef, moduleInitPath)
		if err != nil {
			return err
		}

		return handleChangesetResponseAt(ctx, dag, changesetID, autoApply, exportPath)
	})
}

// callModuleInit invokes the engine's Workspace.moduleInit via a raw
// GraphQL query so this code can ship ahead of an SDK regeneration. When
// the Go SDK is regenerated against the new schema, the body collapses
// to a single typed call:
//
//	dag.CurrentWorkspace().ModuleInit(ctx, name, dagger.WorkspaceModuleInitOpts{
//	    SDK: sdkRef, Path: path,
//	})
//
// until then we go directly through dag.Do.
func callModuleInit(ctx context.Context, dag *dagger.Client, name, sdkRef, path string) (string, error) {
	var res struct {
		CurrentWorkspace struct {
			ModuleInit struct {
				ID string `json:"id"`
			} `json:"moduleInit"`
		} `json:"currentWorkspace"`
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query ModuleInit($name: String!, $sdk: String!, $path: String) {
  currentWorkspace {
    moduleInit(name: $name, sdk: $sdk, path: $path) {
      id
    }
  }
}`,
		Variables: map[string]any{
			"name": name,
			"sdk":  sdkRef,
			"path": path,
		},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return "", fmt.Errorf("plan module init: %w", err)
	}
	if res.CurrentWorkspace.ModuleInit.ID == "" {
		return "", fmt.Errorf("module init returned an empty changeset id")
	}
	return res.CurrentWorkspace.ModuleInit.ID, nil
}

package daggercmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// moduleCmd is the dedicated module-authoring lane.
// Operates on the dagger-module.toml reachable from the current directory.
// Authoring commands take no module-targeting flag — cwd is the implicit subject.
var moduleCmd = &cobra.Command{
	Use:   "module",
	Short: "Author a module: edit dependencies, engine version, etc.",
	Long: `Author a module: edit dependencies, engine version, etc.

Operates on the dagger-module.toml reachable from the current directory.`,
}

// --- dagger module deps: manage the current module's dependencies ---

var moduleDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage this module's dependencies",
}

var moduleDepsAddCmd = &cobra.Command{
	Use:     "add <module>...",
	Short:   "Add one or more dependencies to the module",
	Example: "dagger module deps add github.com/dagger/dagger/modules/wolfi",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
			if err != nil {
				return err
			}

			deps := make([]*dagger.ModuleSource, len(args))
			for i, ref := range args {
				deps[i] = dag.ModuleSource(ref)
			}

			if _, err := modSrc.WithDependencies(deps).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
				return fmt.Errorf("add dependencies: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Added dependencies: %s\nRun 'dagger generate' to refresh generated bindings.\n",
				strings.Join(args, ", "))
			return err
		})
	},
}

var moduleDepsRmCmd = &cobra.Command{
	Use:     "rm <name>...",
	Short:   "Remove one or more dependencies from the module",
	Example: "dagger module deps rm wolfi",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
			if err != nil {
				return err
			}

			if _, err := modSrc.WithoutDependencies(args).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
				return fmt.Errorf("remove dependencies: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Removed dependencies: %s\nRun 'dagger generate' to refresh generated bindings.\n",
				strings.Join(args, ", "))
			return err
		})
	},
}

var moduleDepsUpdateCmd = &cobra.Command{
	Use:   "update [module]...",
	Short: "Update one or more module dependencies",
	Long: `Update one or more module dependencies.

With no arguments, updates all non-local dependencies.`,
	Example: "dagger module deps update wolfi@v0.20.2",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			modSrc, contextDir, err := currentModuleSourceForEdit(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}

			if _, err := modSrc.WithUpdateDependencies(args).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
				return fmt.Errorf("update dependencies: %w", err)
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Updated dependencies.\nRun 'dagger generate' to refresh generated bindings.")
			return err
		})
	},
}

var moduleDepsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the current module's dependencies",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return listModuleDependencies(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

func listModuleDependencies(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	ref, err := getModuleSourceRefWithDefault()
	if err != nil {
		return err
	}

	var res struct {
		ModuleSource struct {
			Dependencies []struct {
				Name   string
				Source string
				Pin    string
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($ref: String!) { moduleSource(refString: $ref) { dependencies { name: moduleName source: asString pin } } }`,
		Variables: map[string]any{"ref": ref},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return err
	}

	deps := res.ModuleSource.Dependencies
	if len(deps) == 0 {
		_, err := fmt.Fprintln(out, "No dependencies.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSOURCE\tPIN"); err != nil {
		return err
	}
	for _, d := range deps {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", d.Name, d.Source, d.Pin); err != nil {
			return err
		}
	}
	return w.Flush()
}

// --- dagger module engine: manage the module's required engine version ---

var moduleEngineCmd = &cobra.Command{
	Use:   "engine",
	Short: "Manage this module's required engine version",
}

var moduleEngineRequiredCmd = &cobra.Command{
	Use:   "required",
	Short: "Print the module's required engine version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ref, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			version, err := dag.ModuleSource(ref).EngineVersion(ctx)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		})
	},
}

var moduleEngineRequireCmd = &cobra.Command{
	Use:     "require <version>",
	Short:   "Set the module's required engine version",
	Example: "dagger module engine require v0.21.0",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), engineClient.Dagger(), args[0])
		})
	},
}

var moduleEngineRequireLatestCmd = &cobra.Command{
	Use:   "require-latest",
	Short: "Set the module's required engine version to the latest released version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		latest, err := latestVersion(cmd.Context())
		if err != nil {
			return fmt.Errorf("determine latest released version: %w", err)
		}
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), engineClient.Dagger(), latest)
		})
	},
}

var moduleEngineRequireCurrentCmd = &cobra.Command{
	Use:   "require-current",
	Short: "Set the module's required engine version to the currently running engine version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			current, err := dag.Version(ctx)
			if err != nil {
				return fmt.Errorf("determine current engine version: %w", err)
			}
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), dag, current)
		})
	},
}

func setModuleEngineVersion(ctx context.Context, out io.Writer, dag *dagger.Client, version string) error {
	modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
	if err != nil {
		return err
	}

	// UpdatedConfigDirectory writes dagger.json without running codegen and
	// without validating the engine version against the running engine, so a
	// requirement newer than the engine we're running can be declared.
	if _, err := modSrc.WithEngineVersion(version).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
		return fmt.Errorf("set engine version: %w", err)
	}

	_, err = fmt.Fprintf(out, "Set required engine version to %s\n", version)
	return err
}

// currentModuleSourceForEdit resolves the local module reachable from the
// current directory for in-place edits to its dagger.json, returning the
// source and the host context directory to export the result back to.
//
// Authoring commands take no module-targeting flag — cwd is the subject.
// (The global --load-module flag, if set, will still be honored via
// getModuleSourceRefWithDefault, but the design's intended pattern is cd-first.)
func currentModuleSourceForEdit(ctx context.Context, dag *dagger.Client) (*dagger.ModuleSource, string, error) {
	ref, err := getModuleSourceRefWithDefault()
	if err != nil {
		return nil, "", err
	}

	modSrc := dag.ModuleSource(ref)
	exists, err := modSrc.ConfigExists(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("load module %q: %w", ref, err)
	}
	if !exists {
		return nil, "", fmt.Errorf("no dagger.json found for module %q", ref)
	}

	contextDir, err := modSrc.LocalContextDirectoryPath(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("module %q must be a local module to edit: %w", ref, err)
	}
	return modSrc, contextDir, nil
}

func init() {
	moduleCmd.AddCommand(moduleDepsCmd, moduleEngineCmd, moduleSdkCmd)
	moduleDepsCmd.AddCommand(moduleDepsAddCmd, moduleDepsRmCmd, moduleDepsUpdateCmd, moduleDepsListCmd)
	moduleEngineCmd.AddCommand(
		moduleEngineRequiredCmd,
		moduleEngineRequireCmd,
		moduleEngineRequireLatestCmd,
		moduleEngineRequireCurrentCmd,
	)
}

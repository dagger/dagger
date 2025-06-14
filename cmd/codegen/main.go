package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

var (
	outputDir             string
	lang                  string
	introspectionJSONPath string

	modulePath string
	moduleName string

	outputSchema string
	merge        bool

	clientOnly bool

	isInit bool

	bundle bool

	moduleSourceID string

	//go:embed modsourcedeps.graphql
	loadModuleSourceDepsQuery string
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
}

var introspectCmd = &cobra.Command{
	Use:  "introspect",
	RunE: Introspect,
}

func init() {
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")

	rootCmd.Flags().StringVar(&modulePath, "module-source-path", "", "path to source subpath of the module")
	rootCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
	rootCmd.Flags().BoolVar(&merge, "merge", false, "merge module deps with project's existing go.mod in a parent directory")
	rootCmd.Flags().BoolVar(&isInit, "is-init", false, "whether this command is initializing a new module")
	rootCmd.Flags().BoolVar(&clientOnly, "client-only", false, "generate only client code")
	rootCmd.Flags().BoolVar(&bundle, "bundle", false, "generate the client in bundle mode")
	rootCmd.Flags().StringVar(&moduleSourceID, "module-source-id", "", "id of the module source to generate code for")

	introspectCmd.Flags().StringVarP(&outputSchema, "output", "o", "", "save introspection result to file")
	rootCmd.AddCommand(introspectCmd)
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)

	cfg := generator.Config{
		Lang:       generator.SDKLang(lang),
		OutputDir:  outputDir,
		Merge:      merge,
		IsInit:     isInit,
		ClientOnly: clientOnly,
		Bundle:     bundle,
	}

	// If a module source ID is provided or no introspection JSON is provided, we will query
	// the engine so we can create a connection here.
	if moduleSourceID != "" || introspectionJSONPath == "" {
		dag, err := dagger.Connect(ctx)
		if err != nil {
			return fmt.Errorf("failed to connect to engine: %w", err)
		}
		defer dag.Close()

		cfg.Dag = dag
	}

	if moduleName != "" {
		cfg.ModuleName = moduleName

		if modulePath == "" {
			return fmt.Errorf("--module-name requires --module-source-path")
		}
		modPath, err := relativeTo(outputDir, modulePath)
		if err != nil {
			return err
		}
		if part, _, _ := strings.Cut(modPath, string(filepath.Separator)); part == ".." {
			return fmt.Errorf("module path must be child of output directory")
		}
		cfg.ModuleSourcePath = modPath
		moduleParentPath, err := relativeTo(modulePath, outputDir)
		if err != nil {
			return err
		}
		cfg.ModuleParentPath = moduleParentPath
	}

	if introspectionJSONPath != "" {
		introspectionJSON, err := os.ReadFile(introspectionJSONPath)
		if err != nil {
			return fmt.Errorf("read introspection json: %w", err)
		}
		cfg.IntrospectionJSON = string(introspectionJSON)
	}

	if moduleSourceID != "" {
		var res struct {
			Source struct {
				Dependencies []generator.ModuleSourceDependencies
			}
		}

		err := cfg.Dag.Do(ctx,
			&dagger.Request{
				Query:  loadModuleSourceDepsQuery,
				OpName: "ModuleSourceDependencies",
				Variables: map[string]any{
					"source": dagger.ModuleSourceID(moduleSourceID),
				},
			},
			&dagger.Response{
				Data: &res,
			})
		if err != nil {
			return fmt.Errorf("failed to load module source dependencies: %w", err)
		}

		cfg.ModuleDependencies = res.Source.Dependencies
	}

	return Generate(ctx, cfg)
}

func Introspect(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	var data any
	err = dag.Do(ctx, &dagger.Request{
		Query: introspection.Query,
	}, &dagger.Response{
		Data: &data,
	})
	if err != nil {
		return fmt.Errorf("introspection query: %w", err)
	}
	if data != nil {
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal introspection json: %w", err)
		}
		if outputSchema != "" {
			return os.WriteFile(outputSchema, jsonData, 0o644) //nolint: gosec
		}
		cmd.Println(string(jsonData))
	}
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func relativeTo(basepath string, tarpath string) (string, error) {
	basepath, err := filepath.Abs(basepath)
	if err != nil {
		return "", err
	}
	tarpath, err = filepath.Abs(tarpath)
	if err != nil {
		return "", err
	}
	return filepath.Rel(basepath, tarpath)
}

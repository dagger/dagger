package main

import (
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

	rootCmd.Flags().StringVar(&modulePath, "module-context-path", "", "path to context directory of the module")
	rootCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
	rootCmd.Flags().BoolVar(&merge, "merge", false, "merge module deps with project's")

	introspectCmd.Flags().StringVarP(&outputSchema, "output", "o", "", "save introspection result to file")
	rootCmd.AddCommand(introspectCmd)
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	// we're checking for the flag existence here as not setting the flag and
	// setting it to false doesn't produce the same behavior.
	var mergePtr *bool
	if cmd.Flags().Changed("merge") {
		mergePtr = &merge
	}

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,

		Merge: mergePtr,
	}

	if moduleName != "" {
		cfg.ModuleName = moduleName

		if modulePath == "" {
			return fmt.Errorf("--module-name requires --module-context-path")
		}
		modPath, err := relativeTo(outputDir, modulePath)
		if err != nil {
			return err
		}
		if part, _, _ := strings.Cut(modPath, string(filepath.Separator)); part == ".." {
			return fmt.Errorf("module path must be child of output directory")
		}
		cfg.ModuleContextPath = modPath
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

	return Generate(ctx, cfg, dag)
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

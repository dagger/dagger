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

	clientOnly bool
	dependenciesRef []string

	dev    bool
	isInit bool
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
	rootCmd.Flags().BoolVar(&dev, "dev", false, "generate in dev mode")
	rootCmd.Flags().StringArrayVar(&dependenciesRef, "dependencies-ref", []string{}, "dependencies used by the module")

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
		Dev:        dev,
		DependenciesRef: dependenciesRef,
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

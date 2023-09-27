package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen"
	"dagger.io/dagger/codegen/generator"
)

var (
	outputDir string
	moduleDir string
	lang      string
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
}

func init() {
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate in")
	rootCmd.Flags().StringVar(&moduleDir, "module", "", "module to load and codegen dependencies for")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,

		// TODO: this should be a flag; we'll want it for codegen run by the user,
		// but not codegen run just-in-time
		AutomateVCS: false,
	}

	if moduleDir != "" {
		cfgBytes, err := os.ReadFile(filepath.Join(moduleDir, ConfigFile))
		if err != nil {
			return fmt.Errorf("read module config: %w", err)
		}

		var modCfg ModuleConfig
		if err := json.Unmarshal(cfgBytes, &modCfg); err != nil {
			return fmt.Errorf("unmarshal module config: %w", err)
		}

		cfg.Lang = generator.SDKLang(modCfg.SDK)
		cfg.ModuleName = modCfg.Name
		cfg.ModuleSourceDir = moduleDir
		cfg.ModuleRootDir = filepath.Join(moduleDir, modCfg.Root)
	}

	return codegen.Generate(ctx, cfg, dag)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

const ConfigFile = "dagger.json"

// TODO: move this + the resolver into dagger.io/dagger proper. Feels like a
// better alignment. Daggerverse could use the resolver too.
type ModuleConfig struct {
	Root         string   `json:"root"`
	Name         string   `json:"name"`
	SDK          string   `json:"sdk,omitempty"`
	Include      []string `json:"include,omitempty"`
	Exclude      []string `json:"exclude,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

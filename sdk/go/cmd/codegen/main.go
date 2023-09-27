package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen"
	"dagger.io/dagger/codegen/generator"
	"dagger.io/dagger/modules"
)

var (
	outputDir   string
	moduleRef   string
	lang        string
	automateVCS bool
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
}

func init() {
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&moduleRef, "module", "", "module to load and codegen dependency code")
	rootCmd.Flags().BoolVar(&automateVCS, "vcs", false, "automate VCS config (.gitignore, .gitattributes)")
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

		AutomateVCS: automateVCS,
	}

	if moduleRef != "" {
		ref, err := modules.ResolveMovingRef(ctx, dag, moduleRef)
		if err != nil {
			return fmt.Errorf("resolve module ref: %w", err)
		}

		modCfg, err := ref.Config(ctx, dag)
		if err != nil {
			return fmt.Errorf("load module config: %w", err)
		}

		cfg.ModuleRef = ref
		cfg.ModuleConfig = modCfg
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

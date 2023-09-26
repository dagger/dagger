package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen"
	"dagger.io/dagger/codegen/generator"
)

var (
	workdir   string
	outputDir string
	lang      string
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate in")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	return codegen.Generate(ctx, generator.Config{
		Lang:      generator.SDKLang(lang),
		OutputDir: outputDir,

		ModuleSourceDir: workdir,

		// we expressly don't want to .gitignore generated files for the
		// off-the-shelf SDK clients; the whole point is to commit + push'em
		AutomateVCS: false,
	}, dag)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/codegen"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/tracing"
)

var (
	workdir   string
	outputDir string
	lang      string
)

var rootCmd = &cobra.Command{
	Use:  "client-gen",
	RunE: ClientGen,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "The host workdir loaded into dagger")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory")
	rootCmd.Flags().StringVar(&lang, "lang", "", "language to generate in")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	return codegen.Generate(ctx, generator.Config{
		Lang:            generator.SDKLang(lang),
		ModuleSourceDir: workdir,
		OutputDir:       outputDir,

		// we expressly don't want to .gitignore generated files for the
		// off-the-shelf SDK clients; the whole point is to commit + push'em
		AutomateVCS: false,
	}, nil)
}

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}

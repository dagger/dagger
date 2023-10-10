package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vito/progrock"
	"github.com/vito/progrock/console"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen"
	"dagger.io/dagger/codegen/generator"
	"dagger.io/dagger/modules"
)

var (
	outputDir     string
	moduleRef     string
	lang          string
	propagateLogs bool
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
}

func init() {
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&moduleRef, "module", "", "module to load and codegen dependency code")
	rootCmd.Flags().BoolVar(&propagateLogs, "propagate-logs", false, "propagate logs directly to progrock.sock")
}

const nestedSock = "/.progrock.sock"

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	var progW progrock.Writer
	var dialErr error
	if propagateLogs {
		progW, dialErr = progrock.DialRPC(ctx, "unix://"+nestedSock)
		if err != nil {
			return fmt.Errorf("error connecting to progrock: %w", err)
		}
	} else {
		progW = console.NewWriter(os.Stderr, console.WithMessageLevel(progrock.MessageLevel_DEBUG))
	}

	rec := progrock.NewRecorder(progW)
	defer rec.Complete()

	if dialErr != nil {
		rec.Warn("could not dial progrock.sock; falling back to console output",
			progrock.ErrorLabel(dialErr))
	}

	ctx = progrock.ToContext(ctx, rec)

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,
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

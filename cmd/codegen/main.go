package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vito/progrock"
	"github.com/vito/progrock/console"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
)

var (
	outputDir             string
	lang                  string
	propagateLogs         bool
	introspectionJSONPath string

	moduleContextPath string
	moduleName        string
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

func init() {
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().BoolVar(&propagateLogs, "propagate-logs", false, "propagate logs directly to progrock.sock")
	rootCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")

	rootCmd.Flags().StringVar(&moduleContextPath, "module-context", "", "path to context directory of the module")
	rootCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
}

const nestedSock = "/.progrock.sock"

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx, dagger.WithSkipCompatibilityCheck())
	if err != nil {
		return err
	}

	var progW progrock.Writer
	var dialErr error
	if propagateLogs {
		progW, dialErr = progrock.DialRPC(ctx, "unix://"+nestedSock)
		if dialErr != nil {
			return fmt.Errorf("error connecting to progrock: %w; falling back to console output", dialErr)
		}
	} else {
		progW = console.NewWriter(os.Stderr, console.WithMessageLevel(progrock.MessageLevel_DEBUG))
	}

	var rec *progrock.Recorder
	if parent := os.Getenv("_DAGGER_PROGROCK_PARENT"); parent != "" {
		rec = progrock.NewSubRecorder(progW, parent)
	} else {
		rec = progrock.NewRecorder(progW)
	}
	defer rec.Complete()
	defer rec.Close()

	ctx = progrock.ToContext(ctx, rec)

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,
	}

	if moduleName != "" {
		cfg.ModuleName = moduleName

		if moduleContextPath == "" {
			return fmt.Errorf("--module-name requires --module-context")
		}
		cfg.ModuleContextPath = moduleContextPath
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

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

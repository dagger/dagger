package main

import (
	"fmt"
	"os"

	"github.com/dagger/cloak/tracing"
	"github.com/spf13/cobra"
)

var (
	projectFile    string
	projectContext string

	queryFile      string
	queryVarsInput []string
	localDirsInput []string
	secretsInput   []string

	generateOutputDir string
	sdkType           string // TODO: enum?

	devServerPort int
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&projectContext, "context", "c", ".", "project context")
	rootCmd.PersistentFlags().StringVarP(&projectFile, "project", "p", "./cloak.yaml", "project config file")
	rootCmd.AddCommand(
		doCmd,
		generateCmd,
		devCmd,
	)

	doCmd.Flags().StringVarP(&queryFile, "file", "f", "", "query file")
	doCmd.Flags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	doCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	doCmd.Flags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")

	generateCmd.Flags().StringVar(&generateOutputDir, "output-dir", "./", "output directory")
	generateCmd.Flags().StringVar(&sdkType, "sdk", "", "sdk type to generate code for ('go', 'ts', etc.)")

	devCmd.Flags().IntVar(&devServerPort, "port", 8080, "dev server port")
	devCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	devCmd.Flags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")
}

var rootCmd = &cobra.Command{
	Use: "cloak",
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

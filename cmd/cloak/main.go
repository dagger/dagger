package main

import (
	"fmt"
	"os"

	"github.com/dagger/cloak/tracing"
	"github.com/spf13/cobra"
)

var (
	configFile string

	queryFile      string
	operation      string
	queryVarsInput []string
	localDirsInput []string
	secretsInput   []string

	generateOutputDir string
	sdkType           string // TODO: enum?

	devServerPort int
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "./cloak.yaml", "config file")
	rootCmd.AddCommand(
		queryCmd,
		generateCmd,
		devCmd,
	)

	queryCmd.Flags().StringVarP(&queryFile, "file", "f", "", "query file")
	queryCmd.Flags().StringVarP(&operation, "op", "o", "", "operation to execute")
	queryCmd.Flags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	queryCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	queryCmd.Flags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")

	generateCmd.Flags().StringVar(&generateOutputDir, "output-dir", "./", "output directory")
	generateCmd.Flags().StringVar(&sdkType, "sdk", "", "sdk type to generate code for ('go', 'ts', etc.)")

	devCmd.Flags().IntVarP(&devServerPort, "port", "p", 8080, "dev server port")
}

var rootCmd = &cobra.Command{
	Use: "cloak",
}

func main() {
	closer := tracing.Init()
	defer closer.Close()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

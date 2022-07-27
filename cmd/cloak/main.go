package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configFile string

	queryFile      string
	operation      string
	queryVarsInput []string
	localDirsInput []string
	secretsInput   []string

	generateOutpuDir string

	devServerPort int
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "f", "./dagger.yaml", "config file")
	rootCmd.AddCommand(
		queryCmd,
		generateCmd,
		devCmd,
	)

	queryCmd.PersistentFlags().StringVarP(&queryFile, "query", "q", "", "query file")
	queryCmd.PersistentFlags().StringVarP(&operation, "op", "o", "", "operation to execute")
	queryCmd.PersistentFlags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	queryCmd.PersistentFlags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	queryCmd.PersistentFlags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")

	generateCmd.PersistentFlags().StringVar(&generateOutpuDir, "output-dir", "./", "output directory")

	devCmd.PersistentFlags().IntVarP(&devServerPort, "port", "p", 8080, "dev server port")
}

var rootCmd = &cobra.Command{
	Use: "cloak",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

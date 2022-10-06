package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.dagger.io/dagger/tracing"
)

var (
	configPath string
	workdir    string

	queryFile      string
	queryVarsInput []string
	localDirsInput []string
	secretsInput   []string

	devServerPort int
)

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "project config file")
	rootCmd.AddCommand(
		doCmd,
		generateCmd,
		devCmd,
		versionCmd,
	)

	doCmd.Flags().StringVarP(&queryFile, "file", "f", "", "query file")
	doCmd.Flags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	doCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	doCmd.Flags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")

	devCmd.Flags().IntVar(&devServerPort, "port", 8080, "dev server port")
	devCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	devCmd.Flags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")
}

var rootCmd = &cobra.Command{
	Use: "dagger",
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

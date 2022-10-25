package main

import (
	"fmt"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/tracing"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string

	queryFile      string
	queryVarsInput []string
	localDirsInput []string

	devServerPort int
)

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "project config file")
	rootCmd.AddCommand(
		doCmd,
		devCmd,
		versionCmd,
		clientGenCmd,
		projectCmd,
	)

	doCmd.Flags().StringVarP(&queryFile, "file", "f", "", "query file")
	doCmd.Flags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	doCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")

	devCmd.Flags().IntVar(&devServerPort, "port", 8080, "dev server port")
	devCmd.Flags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")

	projectCmd.AddCommand(
		initCmd,
		addCmd,
		rmCmd,
	)

	initCmd.Flags().StringVar(&initName, "name", "", "project name")
	initCmd.MarkFlagRequired("name")
	initCmd.Flags().StringVar(&initSDK, "sdk", "", "project sdk")

	addCmd.AddCommand(
		addLocalCmd,
		addGitCmd,
	)

	addLocalCmd.Flags().StringVar(&addLocalPath, "path", "", "path to dagger.json for the extension")
	addLocalCmd.MarkFlagRequired("path")

	addGitCmd.Flags().StringVar(&addGitRemote, "remote", "", "remote of the git repository containing the extension")
	addGitCmd.MarkFlagRequired("repo")
	addGitCmd.Flags().StringVar(&addGitRef, "ref", "main", "git ref to use from the remote repo")
	addGitCmd.Flags().StringVar(&addGitSubpath, "path", "./dagger.json", "subpath in the git repository to the dagger project config")

	rmCmd.Flags().StringVar(&rmName, "name", "", "name of the extension to remove")
	rmCmd.MarkFlagRequired("name")
}

var rootCmd = &cobra.Command{
	Use: "dagger",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		return err
	},
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

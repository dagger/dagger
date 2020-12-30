package cmd

import (
	"fmt"
	"io/ioutil"
	"os"

	"dagger.cloud/go/dagger/ui"
	"github.com/spf13/cobra"
)

var createCmd = &cobra.Command{
	Use:   "create ENV BASE",
	Short: "Create an env",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		envname := args[0]
		base := args[1]
		envdir := ".dagger/env/" + envname
		if info, err := os.Stat(envdir); err == nil {
			if info.IsDir() {
				ui.Fatalf("env already exists: %s", envname)
			}
		}
		if err := os.MkdirAll(envdir, 0755); err != nil {
			ui.Fatal(err)
		}
		baseCue := fmt.Sprintf("package env\nimport base \"%s\"\nbase\n", base)
		err := ioutil.WriteFile(envdir+"/base.cue", []byte(baseCue), 0644)
		if err != nil {
			ui.Fatal(err)
		}
		ui.Info("created environment %q with base %q", envname, base)
	},
}

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	DaggerFileName = "dagger.yaml"
)

var taskCmd = &cobra.Command{
	Use:   "task [TASK]",
	Short: "Run a task defined in a dagger.yaml",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTask(cmd.Context(), args)
	},
	Annotations: map[string]string{
		"experimental": "true",
	},
}

func runTask(ctx context.Context, args []string) error {
	daggerfile, err := readDaggerFile()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		fmt.Println("Available tasks:")
		for taskName := range daggerfile.Tasks {
			fmt.Println("- ", taskName)
		}
		return nil
	}

	if len(args) > 1 {
		return fmt.Errorf("too many arguments")
	}

	taskName := args[0]

	taskDef, err := daggerfile.FullDefinition(taskName)
	if err != nil {
		return err
	}

	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(binary, []string{binary, "-c", taskDef}, os.Environ())
}

type DaggerFile struct {
	Vars  map[string]string `yaml:"vars"`
	Tasks map[string]string `yaml:"tasks"`
}

func readDaggerFile() (*DaggerFile, error) {
	data, err := os.ReadFile(DaggerFileName)
	if err != nil {
		return nil, err
	}

	var daggerfile DaggerFile
	if err := yaml.Unmarshal(data, &daggerfile); err != nil {
		return nil, err
	}

	return &daggerfile, nil
}

func (d *DaggerFile) FullDefinition(task string) (string, error) {
	taskDef, ok := d.Tasks[task]
	if !ok {
		return "", fmt.Errorf("task %q not found", task)
	}

	var vars []string
	for k, v := range d.Vars {
		vars = append(vars, fmt.Sprintf("%s=%s", k, v))
	}

	fullDef := strings.Join(vars, "\n") + "\n" + taskDef
	return fullDef, nil
}

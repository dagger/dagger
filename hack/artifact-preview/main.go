package main

import (
	"fmt"
	"os"

	daggercmd "github.com/dagger/dagger/internal/cmd/dagger"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: artifact-preview INPUT.json check -l [FLAGS] | generate -l [FLAGS] | list TYPE [FLAGS]")
		os.Exit(2)
	}
	if err := preview(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func preview(path string, args []string) error {
	input := os.Stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}
	return daggercmd.PreviewArtifacts(input, os.Stdout, os.Stderr, args)
}

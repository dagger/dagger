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
	input := os.Stdin
	var err error
	if os.Args[1] != "-" {
		input, err = os.Open(os.Args[1])
	}
	if err == nil {
		defer input.Close()
		err = daggercmd.PreviewArtifacts(input, os.Stdout, os.Stderr, os.Args[2:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

const (
	stdoutPath   = "/dagger/stdout"
	stderrPath   = "/dagger/stderr"
	exitCodePath = "/dagger/exitCode"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path> [<args>]\n", os.Args[0])
		os.Exit(1)
	}
	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()

	cmd.Stdin = nil

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		panic(err)
	}
	defer stdoutFile.Close()
	cmd.Stdout = io.MultiWriter(stdoutFile, os.Stdout)

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		panic(err)
	}
	defer stderrFile.Close()
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitCode = 1
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
			os.Exit(exiterr.ExitCode())
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0644); err != nil {
		panic(err)
	}
	os.Exit(exitCode)
}

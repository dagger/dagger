package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/chzyer/readline"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	if err := shell("> "); err != nil {
		panic(err)
	}
}

func dagger(ctx context.Context, module string, args []string) error {
	if module != "" {
		args = append([]string{"-m", module}, args...)
	}
	cmd := exec.CommandContext(ctx, "dagger", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func dshExec(ctx context.Context, args []string) error {
	fmt.Fprintf(os.Stderr, "# %s\n", strings.Join(args, " "))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
		case "install":
			return dagger(ctx, "", args)
		default:
			module := args[0]
			args = append([]string{"call"}, args[1:]...)
			return dagger(ctx, module, args)
	}
	return nil // Returning nil to indicate successful execution; adjust as needed
}

func shell(prompt string) error {
	rl, err := readline.New(prompt)
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	runner, err := interp.New(
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.ExecHandler(dshExec),
		interp.Env(expand.ListEnviron("FOO=bar")),
	)
	if err != nil {
		return fmt.Errorf("Error setting up interpreter: %s", err)
	}
	for {
		line, err := rl.Readline()
		if err != nil { // EOF or Ctrl-D to exit
			break
		}
		parser := syntax.NewParser()
		file, err := parser.Parse(strings.NewReader(line), "")
		if err != nil {
			return fmt.Errorf("Error parsing command: %s", err)
		}
		// Run the parsed command file
		if err := runner.Run(context.Background(), file); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
		}
	}
	return nil
}

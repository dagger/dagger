package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/chzyer/readline"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Run an interactive dagger shell",
	RunE: func(c *cobra.Command, args []string) error {
		return withEngine(c.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return shell(ctx, engineClient, args)
		})
	},
}

// Interactive shell main loop
func shell(ctx context.Context, engineClient *client.Client, args []string) error {
	// FIXME 1: introspect all dependencies & types
	// FIXME 2: cool interactive repl
	prompt := "> "
	rl, err := readline.New(prompt)
	if err != nil {
		panic(err)
	}
	defer rl.Close()
	runner, err := interp.New(
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.ExecHandlers(shellDebug, shellBuiltin, shellCall),
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

func shellDebug(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		fmt.Fprintf(os.Stderr, "# %s\n", strings.Join(args, " "))
		return next(ctx, args)
	}
}

func shellCall(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		module := args[0]
		args = append([]string{"call"}, args[1:]...)
		return execDagger(ctx, module, args)
	}
}

func shellBuiltin(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		fmt.Fprintf(os.Stderr, "# %s\n", strings.Join(args, " "))
		if !strings.HasPrefix(args[0], ".") {
			return next(ctx, args)
		}
		args[0] = args[0][1:]
		switch args[0] {
		case "install":
			return execDagger(ctx, "", args)
		}
		return next(ctx, args)
	}
}

func execDagger(ctx context.Context, module string, args []string) error {
	if module != "" {
		args = append([]string{"-m", module}, args...)
	}
	cmd := exec.CommandContext(ctx, "dagger", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

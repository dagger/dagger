package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		interp.StdIO(nil, os.Stdout, os.Stderr),
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
		// hctx := interp.HandlerCtx(ctx)
		// fmt.Fprintf(hctx.Stderr, "[%s] stdin=%T %v\n", strings.Join(args, " "), hctx.Stdin, hctx.Stdin)
		return next(ctx, args)
	}
}

func readQuery(r io.Reader) ([][]string, error) {
	var q [][]string
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&q); err != nil {
		return nil, err
	}
	return q, nil
}

func writeQuery(q [][]string, w io.Writer) error {
	return json.NewEncoder(w).Encode(q)
}

func shellCall(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hctx := interp.HandlerCtx(ctx)
		var (
			query [][]string
			err   error
		)
		if fstdin, ok := hctx.Stdin.(*os.File); ok && fstdin == nil {
			fmt.Fprintf(hctx.Stderr, "ENTRYPOINT: %v\n", args)
		} else {
			fmt.Fprintf(hctx.Stderr, "CHAINED: %v\n", args)
			query, err = readQuery(hctx.Stdin)
			if (err != nil) && (err != io.EOF) {
				// This means no stdin allowed that doesn't decode to a state, ever.
				return fmt.Errorf("read state (%s): %s", args[0], err.Error())
			}
		}
		outQuery := append(query, args)
		fmt.Fprintf(hctx.Stderr, "%v --> (%s) --> %v\n", query, args[0], outQuery)
		return writeQuery(outQuery, hctx.Stdout)
	}
}

func shellBuiltin(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
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

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
	"github.com/spf13/pflag"
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

var shellModConf *moduleDef

// Interactive shell main loop
func shell(ctx context.Context, engineClient *client.Client, args []string) error {
	// FIXME 1: introspect all dependencies & types
	// FIXME 2: cool interactive repl
    dag := engineClient.Dagger()

	modDef, err := initializeModule(ctx, dag, true)
	if err != nil {
		return fmt.Errorf("error initializing module: %s", err)
	}
	shellModConf = modDef

	prompt := "> "
	rl, err := readline.New(prompt)
	if err != nil {
		panic(err)
	}
	defer rl.Close()

    handler := func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
        return func(ctx context.Context, args []string) error {
            return shellCall(ctx, dag, modDef, args, )
        }
    }

	runner, err := interp.New(
		interp.StdIO(nil, os.Stdout, os.Stderr),
		interp.ExecHandlers(shellDebug, shellBuiltin, handler),
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

func readObject(ctx context.Context) (*Object, error) {
	hctx := interp.HandlerCtx(ctx)
	if fstdin, ok := hctx.Stdin.(*os.File); ok && fstdin == nil {
		return nil, nil
	}
	decoder := json.NewDecoder(hctx.Stdin)
	var o Object
	err := decoder.Decode(&o)
	if err == io.EOF {
		// Empty input or non-json input: no input object
		return &Object{Type: "EOF"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

type Object struct {
	Type  string `json:"type"`
	Calls []Call `json:"calls"`
}

func (o Object) Write(ctx context.Context) error {
	htcx := interp.HandlerCtx(ctx)
	return json.NewEncoder(htcx.Stdout).Encode(o)
}

func (o Object) WithCall(call Call) Object {
	o.Calls = append(o.Calls, call)
	return o
}

type Call struct {
	Function  string                 `json:"function"`
	Arguments map[string]interface{} `json:"arguments"`
}

func shellLog(ctx context.Context, msg string, args ...interface{}) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprintf(hctx.Stderr, msg, args...)
}

func shellCall(ctx context.Context, dag *dagger.Client, modDef *moduleDef, args []string) error {
		o, err := readObject(ctx)
		if err != nil {
			return err
		}
		if o == nil {
			shellLog(ctx, "[%s] ENTRYPOINT!!\n", args[0])
			// You're the entrypoint
			// 1. Interpret args as same-module call (eg. 'build')
			// 2. If no match: interpret args as core function call (eg. 'git')
			// 3. If no match (to be done later): interpret args as dependency short name (eg. 'wolfi container')
			// --> craft the call

			o = &Object{
				Type: modDef.MainObject.AsObject.Name,
			}
		}

		objDef := shellModConf.GetObject(o.Type)
		if objDef == nil {
			return fmt.Errorf("could not find object type %q", o.Type)
		}

		fnDef, err := GetFunction(objDef, args[0])
		if err != nil {
			return fmt.Errorf("%q does not have a %q function", args[0], o.Type)
		}

		o.WithCall(newCall(ctx, fnDef, args[1:]))
		return o.Write(ctx)
	}
}

func newCall(ctx context.Context, modFunc *modFunction, args []string) (*Call, error) {
	flags := pflag.NewFlagSet(modFunc.Name, pflag.ContinueOnError)

	var reqs []*modFunctionArg

	for _, arg := range modFunc.Args {
		if !arg.IsRequired() {
			continue
		}

		shellModConf.LoadTypeDef(arg.TypeDef)

		if err := arg.AddFlag(flags); err != nil {
			return nil, fmt.Errorf("error addding flag: %w", err)
		}

		reqs = append(reqs, arg)
	}

	if len(reqs) > len(args) {
		return nil, fmt.Errorf("not enough arguments: expected %d, got %d", len(reqs), len(args))
	}

	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("error parsing flags: %w", err)
	}

	for i, arg := range reqs {
		argDef := reqs[i]

		flag := flags.Lookup(argDef.FlagName())
		val := flag.Value

		switch v := val.(type) {
		case DaggerValue:
			obj, err := v.Get(ctx, dag, shellModConf.Source, arg)
			if err != nil {
				return nil, fmt.Errorf("failed to get value for argument %q: %w", arg.FlagName(), err)
			}
			if obj == nil {
				return nil, fmt.Errorf("no value for argument: %s", arg.FlagName())
			}
			val = obj
		case pflag.SliceValue:
			val = v.GetSlice()
		}
	}

	return &Call{
		Function:  "",
		Arguments: make(map[string]interface{}),
	}, nil
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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
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

// Interactive shell main loop
func shell(ctx context.Context, engineClient *client.Client, args []string) error {
	// FIXME 1: introspect all dependencies & types
	// FIXME 2: cool interactive repl
	dag := engineClient.Dagger()

	modDef, err := initializeModule(ctx, dag, true)
	if err != nil {
		return fmt.Errorf("error initializing module: %s", err)
	}

	prompt := "> "
	rl, err := readline.New(prompt)
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	handler := func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			return shellCall(ctx, dag, modDef, args)
		}
	}

	runner, err := interp.New(
		interp.StdIO(nil, os.Stdout, os.Stderr),
		interp.ExecHandlers(shellDebug, handler),
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
		return nil, nil
		// Empty input or non-json input: no input object
		//return &Object{Type: "EOF"}, nil
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
		if strings.HasPrefix(args[0], ".") {
			return shellBuiltin(ctx, dag, modDef, args)
		}
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

	objDef := modDef.GetObject(o.Type)
	if objDef == nil {
		return fmt.Errorf("could not find object type %q", o.Type)
	}
	// TODO: modDef.LoadTypeDef(objDef)

	fnDef, err := GetFunction(objDef, args[0])
	if err != nil {
		return fmt.Errorf("%q does not have a %q function", args[0], o.Type)
	}

	call, err := newCall(ctx, dag, modDef, fnDef, args[1:])
	if err != nil {
		return fmt.Errorf("error creating call: %w", err)
	}

	o.WithCall(*call)
	return o.Write(ctx)
}

func newCall(ctx context.Context, dag *dagger.Client, modDef *moduleDef, modFunc *modFunction, args []string) (*Call, error) {
	flags := pflag.NewFlagSet(modFunc.Name, pflag.ContinueOnError)

	var reqs []*modFunctionArg

	for _, arg := range modFunc.Args {
		if !arg.IsRequired() {
			continue
		}

		modDef.LoadTypeDef(arg.TypeDef)

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

	margs := make(map[string]interface{})

	for i, arg := range reqs {
		var val any
		argDef := reqs[i]

		flag := flags.Lookup(argDef.FlagName())
		val = flag.Value

		switch v := val.(type) {
		case DaggerValue:
			obj, err := v.Get(ctx, dag, modDef.Source, arg)
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

		margs[arg.Name] = val
	}

	return &Call{
		Function:  modFunc.Name,
		Arguments: margs,
	}, nil
}

// Re-execute the dagger command (hack)
func reexec(ctx context.Context, args []string) error {
	hctx := interp.HandlerCtx(ctx)
	cmd := exec.CommandContext(ctx, "dagger", args...)
	cmd.Stdout = hctx.Stdout
	cmd.Stderr = hctx.Stderr
	cmd.Stdin = hctx.Stdin
	return cmd.Run()
}

func shellBuiltin(ctx context.Context, dag *dagger.Client, modDef *moduleDef, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no specified builtin")
	}
	switch args[0] {
	case ".help":
		shellLog(ctx, `
.functions    list available functions
.help         print this help message
.install	  install a dependency
.deps         list dependencies
.uninstall    uninstall a dependency
.login        login to Dagger Cloud
.logout       logout from Dagger Cloud
.core         load a core Dagger type
`)
		return nil
	case ".install":
		if len(args) < 1 {
			return fmt.Errorf("usage: .install MODULE")
		}
		return reexec(ctx, []string{"install", args[0]})
	case ".deps":
	case ".uninstall":
	case ".login":
		return reexec(ctx, append([]string{"login"}, args...))
	case ".logout":
	case ".core":
	case ".config":
	case ".functions":
		functions := modDef.MainObject.AsFunctionProvider().GetFunctions()
		w := tabwriter.NewWriter(interp.HandlerCtx(ctx).Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Function\tDescription\tReturn type")
		fmt.Fprintln(w, "---\t---\t---")
		for _, fn := range functions {
			fmt.Fprintf(w, "%s\t%s\t%s\n", fn.Name, fn.Description, fn.ReturnType.Name())
		}
		return w.Flush()
	default:
		return fmt.Errorf("no such command: %s", args[0])
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/chzyer/readline"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
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
		handler := func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return func(ctx context.Context, args []string) error {
				return shellCall(ctx, dag, modDef, args)
			}
		}
		r, w := io.Pipe()
		runner, err := interp.New(
			interp.StdIO(nil, w, os.Stderr),
			interp.ExecHandlers(shellDebug, handler),
		)
		eg, ctx := errgroup.WithContext(ctx)
		eg.Go(func() error {
			o, err := readObj(r)
			if err != nil {
				if o != nil {
					fmt.Printf("%s\n", o.Data)
					return nil
				}
				return err
			}
			return shellRequest(ctx, dag, o)
		})
		eg.Go(func() error {
			err := runner.Run(ctx, file)
			w.Close()
			return err
		})
		if err := eg.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err.Error())
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
	return readObj(interp.HandlerCtx(ctx).Stdin)
}

func readObj(r io.Reader) (*Object, error) {
	if f, ok := r.(*os.File); ok && f == nil {
		return nil, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var o Object
	err = json.Unmarshal(b, &o)
	o.Data = string(b)
	return &o, err
}

type Object struct {
	Type  string `json:"type"`
	Calls []Call `json:"calls"`
	Data  string `json:"data"`
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
	// shellLog(ctx, "[DBG] input: %v; args: %v\n", o, args)
	if o == nil {
		if strings.HasPrefix(args[0], ".") {
			return shellBuiltin(ctx, dag, modDef, args)
		}
		// shellLog(ctx, "[%s] ENTRYPOINT!!\n", args[0])
		// You're the entrypoint
		// 1. Interpret args as same-module call (eg. 'build')
		// 2. If no match: interpret args as core function call (eg. 'git')
		// 3. If no match (to be done later): interpret args as dependency short name (eg. 'wolfi container')
		// --> craft the call

		first, err := newCall(ctx, dag, modDef, modDef.MainObject.AsObject.Constructor, nil)
		if err != nil {
			return err
		}

		o = &Object{
			Type: modDef.MainObject.AsObject.Name,
		}
		o.Calls = append(o.Calls, *first)
	}

	objDef := modDef.GetObject(o.Type)
	if objDef == nil {
		return fmt.Errorf("could not find object type %q", o.Type)
	}
	fnDef, err := GetFunction(objDef, args[0])
	if err != nil {
		return fmt.Errorf("%q does not have a %q function", o.Type, args[0])
	}

	modDef.LoadTypeDef(fnDef.ReturnType)
	// shellLog(ctx, "[DBG] fn: %s; retType: %v; retTypeName: %s\n", fnDef.Name, fnDef.ReturnType, fnDef.ReturnType.Name())

	fnProv := fnDef.ReturnType

	if fnProv == nil {
		return fmt.Errorf("function %q does not return a function provider", fnDef.Name)
	}
	o.Type = fnProv.Name()

	call, err := newCall(ctx, dag, modDef, fnDef, args[1:])
	if err != nil {
		return fmt.Errorf("error creating call: %w", err)
	}

	o.Calls = append(o.Calls, *call)

	// shellLog(ctx, "[DBG] output: %v\n", o)

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

	fargs := make([]string, 0, len(args)*2)
	for i, arg := range reqs {
		fargs = append(fargs, "--"+arg.flagName, args[i])
	}

	if err := flags.Parse(fargs); err != nil {
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

func shellRequest(ctx context.Context, dag *dagger.Client, obj *Object) error {
	q := querybuilder.Query().Client(dag.GraphQLClient())
	for _, call := range obj.Calls {
		q = q.Select(call.Function)
		for n, v := range call.Arguments {
			q = q.Arg(n, v)
		}
	}

	var response any
	// query, err := q.Build(ctx)
	// if err != nil {
	// 	return err
	// }

	q = q.Bind(&response)

	if err := q.Execute(ctx); err != nil {
		return fmt.Errorf("response from query: %w", err)
	}
	printFunctionResult(os.Stdout, response)
	// fmt.Printf("%s\n", response)
	return nil
}

func shellBuiltin(ctx context.Context, dag *dagger.Client, modDef *moduleDef, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no specified builtin")
	}
	if strings.HasPrefix(args[0], ".") {
		args[0] = args[0][1:]
	}
	switch args[0] {
	case "help":
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
	case "git":
		if len(args) < 2 {
			return fmt.Errorf("usage: .git URL")
		}
		gitUrl, err := parseGitURL(args[1])
		if err != nil {
			return err
		}
		ref := gitUrl.Ref
		if ref == "" {
			ref = "main"
		}
		subdir := gitUrl.Path
		gitUrl.Ref = ""
		gitUrl.Path = ""
		o := &Object{
			Type: "Directory",
			Calls: []Call{
				{
					Function: "git",
					Arguments: map[string]interface{}{
						"url": gitUrl.String(),
					},
				},
				{
					Function: "ref",
					Arguments: map[string]interface{}{
						"name": ref,
					},
				},
				{
					Function: "tree",
				},
				{
					Function: "directory",
					Arguments: map[string]interface{}{
						"path": subdir,
					},
				},
			},
		}
		return o.Write(ctx)
	case "install":
		if len(args) < 1 {
			return fmt.Errorf("usage: .install MODULE")
		}
		return reexec(ctx, args)
	case "deps":
		deps, err := modDef.Source.AsModule().Dependencies(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(interp.HandlerCtx(ctx).Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION")
		fmt.Fprintln(w, " \t ")
		for _, dep := range deps {
			name, err := dep.Name(ctx)
			if err != nil {
				return err
			}
			desc, err := dep.Description(ctx)
			if err != nil {
				return err
			}
			shortDesc := strings.SplitN(desc, "\n", 2)[0]
			fmt.Fprintf(w, "%s\t%s\n", name, shortDesc)
		}
		return w.Flush()

	case "uninstall":
	case "login":
		return reexec(ctx, args)
	case "logout":
		return reexec(ctx, args)
	case "core":
		return fmt.Errorf("FIXME: not yet implemented")
	case "config":
	case "functions":
		functions := modDef.MainObject.AsFunctionProvider().GetFunctions()
		w := tabwriter.NewWriter(interp.HandlerCtx(ctx).Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION\tRETURN TYPEs")
		fmt.Fprintln(w, "---\t---\t---")
		for _, fn := range functions {
			fmt.Fprintf(w, "%s\t%s\t%s\n", fn.CmdName(), fn.Description, fn.ReturnType.Name())
		}
		return w.Flush()
	default:
		return fmt.Errorf("no such command: %s", args[0])
	}
	return nil
}

// GitURL represents the different parts of a git-style URL.
type GitURL struct {
	Scheme string
	Host   string
	Owner  string
	Repo   string
	Ref    string
	Path   string
}

// ParseGitURL parses a git-style URL into its components.
func parseGitURL(gitURL string) (*GitURL, error) {
	u, err := url.Parse(gitURL)
	if err != nil {
		return nil, err
	}

	// Splitting the path part to extract owner and repo
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repository path: %s", u.Path)
	}
	owner := parts[0]
	repo := parts[1]

	// Check if there is a fragment (ref and path)
	var ref, path string
	if u.Fragment != "" {
		fragmentParts := strings.SplitN(u.Fragment, "/", 2)
		ref = fragmentParts[0]
		if len(fragmentParts) > 1 {
			path = fragmentParts[1]
		}
	}

	return &GitURL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Owner:  owner,
		Repo:   repo,
		Ref:    ref,
		Path:   path,
	}, nil
}

// String reconstructs the git-style URL from its components.
func (p GitURL) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s://%s/%s/%s", p.Scheme, p.Host, p.Owner, p.Repo))

	// Append branch and path if present
	if p.Ref != "" {
		sb.WriteString(fmt.Sprintf("#%s", p.Ref))
		if p.Path != "" {
			sb.WriteString(fmt.Sprintf("/%s", p.Path))
		}
	}
	return sb.String()
}

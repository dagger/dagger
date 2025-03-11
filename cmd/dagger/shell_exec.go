package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/spf13/pflag"
	"mvdan.cc/sh/v3/interp"
)

const (
	shellHandlerExit = 200

	// shellInternalCmd is the command that is used internally to avoid conflicts
	// with interpreter builtins. For example when `echo` is used, the command becomes
	// `__dag echo`. Otherwise we can't have a function named `echo`.
	shellInternalCmd = "__dag"

	// shellInterpBuiltinPrefix is the prefix that users should add to an
	// interpreter builtin command to force running it.
	shellInterpBuiltinPrefix = "_"
)

func isInterpBuiltin(name string) bool {
	// See https://github.com/mvdan/sh/blob/v3.11.0/interp/builtin.go#L27
	// Allow the following:
	//  - invalid function/module names: "[", ":"
	//  - unlikely to conflict: "true", "false"
	switch name {
	case "exit", "set", "shift", "unset",
		"echo", "printf", "break", "continue", "pwd", "cd",
		"wait", "builtin", "trap", "type", "source", ".", "command",
		"dirs", "pushd", "popd", "alias", "unalias",
		"getopts", "eval", "test", "exec",
		"return", "read", "mapfile", "readarray", "shopt",
		//  not implemented
		"umask", "fg", "bg":
		return true
	}
	return false
}

// Call is a handler which runs on every [syntax.CallExpr].
//
// It is called once variable assignments and field expansion have occurred.
// The call's arguments are replaced by what the handler returns,
// and then the call is executed by the Runner as usual.
//
// This handler is similar to [Exec], but has two major differences:
//
// First, it runs for all simple commands, including function calls and builtins.
//
// Second, it is not expected to execute the simple command, but instead to
// allow running custom code which allows replacing the argument list.
//
// This is used mainly to resolve  conflicts. For example, "echo" is an
// interpreter builtin but can also be a Dagger function.
func (h *shellCallHandler) Call(ctx context.Context, args []string) ([]string, error) {
	if args[0] == shellInternalCmd {
		return args, fmt.Errorf("command %q is reserved for internal use", shellInternalCmd)
	}

	// If command has an interpolated state token, make sure to resolve it.
	// If it's a single token let it pass through so the handler just pipes it.
	// Example: `.$FOO | .help`
	if HasState(args[0]) && GetStateKey(args[0]) == "" {
		r, err := h.resolveResult(ctx, args[0])
		if err != nil {
			return args, err
		}
		if r != args[0] {
			args[0] = r
		}
	}

	// When there's a Dagger function with a name that conflicts
	// with an interpreter builtin, the Dagger function is favored.
	// To force the builtin to execute instead, prefix the command
	// with "_". For example: "container | from $(_echo alpine)".
	if strings.HasPrefix(args[0], shellInterpBuiltinPrefix) {
		args[0] = strings.TrimPrefix(args[0], shellInterpBuiltinPrefix)
		return args, nil
	}

	// We may allow some interpreter builtins to be used as dagger shell
	// builtins, but there's no way to directly call the interpreter
	// command from there so we use ShellCommand just for the documentation
	// (.help) but strip the builtin prefix here ('.') when executing.
	if cmd, _ := h.BuiltinCommand(args[0]); cmd != nil && cmd.Run == nil {
		if name := strings.TrimPrefix(args[0], "."); isInterpBuiltin(name) {
			args[0] = name
			return args, nil
		}
	}

	// If the command is an interpreter builtin, bypass the interpreter
	// builtins to ensure the exec handler is executed.
	if isInterpBuiltin(args[0]) {
		return append([]string{shellInternalCmd}, args...), nil
	}

	return args, nil
}

// Exec is the main handler for executing simple commands.
//
// It is called for all [syntax.CallExpr] nodes
// where the first argument is neither a declared shell function nor a builtin.
//
// This handler is responsible to interpreting functions and module references
// as commands that can be executed, and wraps any returned errors.
func (h *shellCallHandler) Exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		// This avoids interpreter builtins running first, which would make it
		// impossible to have a function named "echo", for example. We can
		// remove `__dag` from this point onward.
		if args[0] == shellInternalCmd {
			args = args[1:]
		}

		// If argument is a state value, just pass it on to stdout.
		// Example: `$FOO` or `$FOO | bar`
		if GetStateKey(args[0]) != "" {
			hctx := interp.HandlerCtx(ctx)
			fmt.Fprint(hctx.Stdout, args[0])
			return nil
		}

		st, err := h.cmd(ctx, args)
		if err == nil && st != nil {
			if h.debug {
				shellDebug(ctx, "Stdout", args[0], args[1:], st)
			}
			err = h.Save(ctx, *st)
		}
		if err != nil {
			if h.debug {
				shellDebug(ctx, "Error", err, args)
			}

			if st == nil {
				st = &ShellState{}
			}

			st.Error = err

			// Ensure any error from the handler is written to stdout so that
			// the next command in the chain knows about it.
			if e := h.Save(ctx, *st); e != nil {
				return e
			}

			// There's a bug in the library where a handler that does `return err`
			// is fatal but NewExitStatus` is not. With a fatal error, if this
			// is in a command substitution, the parent command won't even
			// execute, but the next command in the pipeline will, and with.
			// an empty stdin. This way we pass the error state as an argument
			// to the parent command and fail there when parsing the arguments.
			return interp.NewExitStatus(shellHandlerExit)
		}

		return nil
	}
}

// cmd is the main logic for executing simple commands
func (h *shellCallHandler) cmd(ctx context.Context, args []string) (*ShellState, error) {
	c, a := args[0], args[1:]

	stdin := interp.HandlerCtx(ctx).Stdin

	// First command in pipeline: e.g., `cmd1 | cmd2 | cmd3`
	if stdin == nil {
		return h.entrypointCall(ctx, c, a)
	}

	b, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	s := string(b)

	// Stdin expects a single state
	st, err := h.state.Load(GetStateKey(s))
	if err != nil {
		// should pass st around to cleanup from state store in case the
		// error came from the state
		return st, err
	}
	if st == nil {
		if h.debug {
			shellDebug(ctx, "InvalidStdin", args, s)
		}
		return nil, fmt.Errorf("unexpected input for command %q", c)
	}
	if h.debug {
		shellDebug(ctx, "Stdin", c, a, st)
	}

	builtin, err := h.BuiltinCommand(c)
	if err != nil {
		return nil, err
	}
	if builtin != nil {
		return nil, builtin.Execute(ctx, h, a, st)
	}

	if st.IsCommandRoot() {
		switch {
		case st.IsStdlib():
			// Example: .stdlib | <command>`
			stdlib, err := h.StdlibCommand(c)
			if err != nil {
				return nil, err
			}
			return nil, stdlib.Execute(ctx, h, a, nil)

		case st.IsDeps():
			// Example: `.deps | <dependency>`
			st, def, err := h.GetDependency(ctx, c)
			if err != nil {
				return nil, err
			}
			return h.constructorCall(ctx, def, st, a)

		case st.IsCore():
			// Example: `.core | <function>`
			def := h.GetDef(st)
			if !def.HasCoreFunction(c) {
				return nil, fmt.Errorf("core function %q not found", c)
			}
			// an empty state's first object is Query by default so
			// functionCall already handles it
		}
	}

	// module or core function call
	return h.functionCall(ctx, st, c, a)
}

// entrypointCall is executed when it's the first command in a pipeline
func (h *shellCallHandler) entrypointCall(ctx context.Context, cmd string, args []string) (*ShellState, error) {
	if cmd, _ := h.BuiltinCommand(cmd); cmd != nil {
		return nil, cmd.Execute(ctx, h, args, nil)
	}

	st, err := h.StateLookup(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if h.debug {
		shellDebug(ctx, "Entrypoint", cmd, args, st)
	}

	if st.IsStdlib() {
		cmd, err := h.StdlibCommand(cmd)
		if err != nil {
			return nil, err
		}
		return nil, cmd.Execute(ctx, h, args, nil)
	}

	if md, _ := h.GetModuleDef(st); md != nil {
		// Command is a function in current context
		if h.isCurrentContextFunction(cmd) {
			// We need to assume a constructor call without arguments
			st, err := h.constructorCall(ctx, md, st, nil)
			if err != nil {
				return nil, err
			}
			return h.functionCall(ctx, st, cmd, args)
		}

		// Command is a dependency or module ref, so this is the constructor call
		if st.IsEmpty() {
			return h.constructorCall(ctx, md, st, args)
		}
	}

	return st, nil
}

func (h *shellCallHandler) isCurrentContextFunction(name string) bool {
	md, _ := h.GetModuleDef(nil)
	return md != nil && md.HasMainFunction(name)
}

func (h *shellCallHandler) StateLookup(ctx context.Context, name string) (*ShellState, error) {
	if h.debug {
		shellDebug(ctx, "StateLookup", name)
	}

	// Is current context a loaded module?
	if md, _ := h.GetModuleDef(nil); md != nil {
		// 1. Function in current context
		if md.HasMainFunction(name) {
			st := h.NewState()
			return &st, nil
		}

		// 2. Dependency short name
		if dep := md.GetDependency(name); dep != nil {
			depSt, _, err := h.GetDependency(ctx, name)
			return depSt, err
		}

		// 3. Is it the current module's name?
		if md.Name == name {
			st := h.newModState(md.SourceDigest)
			return &st, nil
		}
	}

	// 4. Standard library command
	if cmd, _ := h.StdlibCommand(name); cmd != nil {
		st := h.NewStdlibState()
		return &st, nil
	}

	// 5. Path to local or remote module source
	def, _, err := h.maybeLoadModule(ctx, name)
	if err != nil {
		return nil, err
	}
	if def != nil {
		st := h.newModState(def.SourceDigest)
		return &st, nil
	}

	return nil, fmt.Errorf("function or module %q not found", name)
}

func (h *shellCallHandler) constructorCall(ctx context.Context, md *moduleDef, st *ShellState, args []string) (*ShellState, error) {
	fn := md.MainObject.AsObject.Constructor

	values, err := h.parseArgumentValues(ctx, md, fn, args)
	if err != nil {
		return nil, fmt.Errorf("%q constructor: %w", md.Name, err)
	}

	newSt := st.WithCall(fn, values)

	return &newSt, nil
}

// functionCall is executed for every command that the exec handler processes
func (h *shellCallHandler) functionCall(ctx context.Context, st *ShellState, name string, args []string) (*ShellState, error) {
	def := h.GetDef(st)
	call := st.Function()

	fn, err := call.GetNextDef(def, name)
	if err != nil {
		return st, err
	}

	argValues, err := h.parseArgumentValues(ctx, def, fn, args)
	if err != nil {
		return st, fmt.Errorf("function %q: %w", fn.CmdName(), err)
	}

	newSt := st.WithCall(fn, argValues)

	return &newSt, nil
}

// shellPreprocessArgs converts positional arguments to flag arguments
//
// Values are not processed. This function is used to leverage pflags to parse
// flags interspersed with positional arguments, so a function's required
// arguments can be placed anywhere. Then we get the unprocessed flags in
// order to validate if the remaining number of positional arguments match
// the number of required arguments.
//
// Required args in dagger shell are positional but we have a lot of power
// in custom flags that we want to reuse, so just add the corresponding
// `--flag-name` args in order for pflags to be able to parse them later.
//
// Additionally, if there's only one required argument that is a list of strings,
// all positional arguments are used as elements of that list.
func shellPreprocessArgs(ctx context.Context, fn *modFunction, args []string) ([]string, error) {
	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)
	flags.SetOutput(io.MultiWriter(
		interp.HandlerCtx(ctx).Stderr,
		telemetry.SpanStdio(ctx, InstrumentationLibrary).Stderr,
	))

	opts := fn.OptionalArgs()

	// All CLI arguments are strings at first, but booleans can be omitted.
	// We don't wan't to process values yet, just validate and consume the flags
	// so we get the remaining positional args.
	for _, arg := range opts {
		name := arg.FlagName()

		switch arg.TypeDef.Kind {
		case dagger.TypeDefKindListKind:
			switch arg.TypeDef.AsList.ElementTypeDef.Kind {
			case dagger.TypeDefKindBooleanKind:
				flags.BoolSlice(name, nil, "")
			default:
				flags.StringSlice(name, nil, "")
			}
		case dagger.TypeDefKindBooleanKind:
			flags.Bool(name, false, "")
		default:
			flags.String(name, "", "")
		}
	}

	if err := flags.Parse(args); err != nil {
		return args, checkErrHelp(err, args)
	}

	reqs := fn.RequiredArgs()

	// A command for with-exec could include a `--`, but it's only if it's
	// the first positional argument that means we've stopped processing our
	// flags. So these are equivalent:
	// - with-exec --redirect-stdout /out git checkout -- file
	// - with-exec --redirect-stdout /out -- git checkout -- file
	pos := flags.Args()
	if flags.ArgsLenAtDash() == 1 {
		pos = pos[1:]
	}

	// Final processed arguments that will be parsed in the second phase later on.
	var a []string

	// Convenience for a single required argument of type [String!]!
	// All positional arguments become elements in the list.
	if len(reqs) == 1 && len(pos) > 1 && reqs[0].TypeDef.String() == "[]string" {
		name := reqs[0].FlagName()
		a = make([]string, 0, len(opts)+len(pos))

		for _, value := range pos {
			// Instead of creating a CSV value here, repeat the flag for each
			// one so that pflags is the only one dealing with CSVs.
			a = append(a, fmt.Sprintf("--%s=%v", name, value))
		}
	} else {
		// Normal use case. Positional arguments should match number of required function arguments
		if err := ExactArgs(len(reqs))(pos); err != nil {
			return args, err
		}
		a = make([]string, 0, len(fn.Args))
		// Use the `=` syntax so that each element in the args list corresponds
		// to a single argument instead of two.
		for i, arg := range reqs {
			a = append(a, fmt.Sprintf("--%s=%v", arg.FlagName(), pos[i]))
		}
	}

	// Add all the optional flags
	flags.Visit(func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		switch val := f.Value.(type) {
		case pflag.SliceValue:
			// Repeat the flag for each value so we don't have to deal with CSV.
			for _, v := range val.GetSlice() {
				a = append(a, fmt.Sprintf("--%s=%v", f.Name, v))
			}
		default:
			a = append(a, fmt.Sprintf("--%s=%v", f.Name, f.Value.String()))
		}
	})

	return a, nil
}

// checkErrHelp circumvents pflag's special cases for -h and --help
//
// This returns the same error as any other unknown flag.
func checkErrHelp(err error, args []string) error {
	if !errors.Is(err, pflag.ErrHelp) {
		return err
	}
	// Avoid considering flags from a with-exec for example, although if this
	// error is raised it's surely defined before `--`.
	if i := slices.Index(args, "--"); i > 0 {
		args = args[:i]
	}
	// Shorthand flags are parsed first
	if slices.Contains(args, "-h") {
		return errors.New(`unknown shorthand flag: "-h"`)
	}
	return errors.New(`unknown flag: "--help"`)
}

// parseArgumentValues returns a map of argument names and their parsed values
func (h *shellCallHandler) parseArgumentValues(
	ctx context.Context,
	md *moduleDef,
	fn *modFunction,
	args []string,
) (rargs map[string]any, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = fmt.Errorf("%w\n\nUsage: %s", rerr, h.FunctionFullUseLine(md, fn))
		}
	}()

	newArgs, err := shellPreprocessArgs(ctx, fn, args)
	if err != nil {
		return nil, err
	}

	if len(newArgs) == 0 {
		return nil, nil
	}

	if h.debug {
		defer func() {
			shellDebug(ctx, "Arguments", fn.CmdName(), args, newArgs, rargs)
		}()
	}

	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)
	flags.SetOutput(io.MultiWriter(
		interp.HandlerCtx(ctx).Stderr,
		telemetry.SpanStdio(ctx, InstrumentationLibrary).Stderr,
	))

	// Add flags for each argument, including unsupported ones, which we
	// assume it's being supported through some other means, so we just
	// bypass the flags. This how we pass ID values to flag parsing, without
	// having support for it with a custom flag.
	// TODO: Create an "ID" or "Raw" type flag and validate appropriately
	for _, a := range fn.Args {
		err := a.AddFlag(flags)
		var e *UnsupportedFlagError
		if errors.As(err, &e) {
			// This is just enough to trigger passing the value to ParseAll,
			// but will only be used for getting the value if it doesn't
			// originate from a command expansion (subshell).
			// TODO: This will likely fail if value doesn't come from command
			// expansion because the value that is passed goes directly to the
			// API. We should validate this more, or refactor.
			flags.String(a.FlagName(), "", a.Description)
			flags.MarkHidden(a.FlagName())
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("error addding flag: %w", err)
		}
	}

	// Final map of resolved argument values
	values := make(map[string]any, len(fn.Args))

	// Parse arguments using flags to get the values matched with the right
	// argument definition. Bypass the flag if the argument value comes from
	// a command expansion, otherwise set the flag value.
	f := func(flag *pflag.Flag, value string) error {
		a, err := fn.GetArg(flag.Name)
		if err != nil {
			return err
		}
		v, bypass, err := h.parseFlagValue(ctx, value, a)
		if err != nil {
			return fmt.Errorf("cannot expand function argument %q: %w", a.FlagName(), err)
		}
		if v == nil {
			return fmt.Errorf("unexpected nil value while expanding function argument %q", a.FlagName())
		}
		// Flags only support setting their values from strings, so if
		// anything else is returned, we just ignore it.
		// TODO: try to validate this more to avoid surprises
		if sval, ok := v.(string); ok && !bypass {
			return flags.Set(flag.Name, sval)
		}
		// This will bypass using a flag for this argument since we're
		// saying it's a final value already.
		if bypass {
			values[a.Name] = v
		}
		return nil
	}
	if err := flags.ParseAll(newArgs, f); err != nil {
		return nil, checkErrHelp(err, newArgs)
	}

	// Finally, get the values from the flags that haven't been resolved yet.
	for _, a := range fn.Args {
		if _, exists := values[a.Name]; exists {
			continue
		}
		flag, err := a.GetFlag(flags)
		if err != nil {
			return nil, err
		}
		if !flag.Changed {
			continue
		}
		v, err := a.GetFlagValue(ctx, flag, h.dag, md)
		if err != nil {
			return nil, err
		}
		values[a.Name] = v
	}

	return values, nil
}

// parseFlagValue ensures that a flag value with state gets resolved
//
// This happens most commonly when argument is the result of command expansion
// from a sub-shell.
func (h *shellCallHandler) parseFlagValue(ctx context.Context, value string, arg *modFunctionArg) (rval any, bypass bool, rerr error) {
	argType := arg.TypeDef
	states := FindStateTokens(value)

	if len(states) == 0 {
		if argType.AsObject != nil {
			switch argType.AsObject.Name {
			case Directory, File:
				// Ignore on git urls since the flag will parse it directly.
				// We just need to resolve the ref for "local" (contextual) paths.
				if _, err := parseGitURL(value); err != nil {
					return h.contextArgRef(value), false, nil //nolint:nilerr
				}
			}
		}
		return value, false, nil
	}

	if h.debug {
		shellDebug(ctx, "parse flag value", arg.FlagName(), states, bypass, rval)
	}

	// If value isn't one state exactly, we need to process into a string
	if len(states) > 1 || states[0] != value {
		r, err := h.resolveResult(ctx, value)
		return r, false, err
	}

	// Otherwise it may be an object that we want to bypass (for its ID)
	st, err := h.state.Extract(GetStateKey(value))
	if err != nil {
		return nil, false, err
	}
	r, err := h.StateResult(ctx, st)
	if err != nil {
		return nil, false, err
	}
	if r.IsObject() {
		return r.Value, true, err
	}
	s, err := r.String()
	return s, false, err
}

// Result is a resolved state
type Result struct {
	Value   any
	typeDef *modTypeDef
}

func (r *Result) String() (string, error) {
	if r.IsVoid() {
		return "", nil
	}
	sb := new(strings.Builder)
	err := printResponse(sb, r.Value, r.typeDef)
	return sb.String(), err
}

func (r *Result) IsObject() bool {
	return r.typeDef != nil && r.typeDef.AsFunctionProvider() != nil
}

func (r *Result) IsVoid() bool {
	return r.typeDef != nil && r.typeDef.Kind == dagger.TypeDefKindVoidKind
}

// StateResult resolves a state into a value, more commonly by making an API request.
func (h *shellCallHandler) StateResult(ctx context.Context, st *ShellState) (*Result, error) {
	if st == nil {
		return nil, nil
	}

	if st.IsCommandRoot() {
		r := &Result{}
		switch {
		case st.IsStdlib():
			r.Value = h.CommandsList(st.Cmd, h.Stdlib())
		case st.IsDeps():
			r.Value = h.DependenciesList()
		case st.IsCore():
			def := h.GetDef(nil)
			r.Value = h.FunctionsList(st.Cmd, def.GetCoreFunctions())
		default:
			return nil, fmt.Errorf("unexpected namespace %q", st.Cmd)
		}
		return r, nil
	}

	def := h.GetDef(st)
	var err error

	// Example: `build` (i.e., omitted constructor)
	if def.HasModule() && st.IsEmpty() {
		st, err = h.constructorCall(ctx, def, st, nil)
		if err != nil {
			return nil, err
		}
	}

	fn, err := st.Function().GetDef(def)
	if err != nil {
		return nil, err
	}

	r := &Result{typeDef: fn.ReturnType}
	q := handleObjectLeaf(st.QueryBuilder(h.dag), fn.ReturnType)
	err = makeRequest(ctx, q, &r.Value)

	return r, err
}

func (h *shellCallHandler) getOrInitDef(ref string, fn func() (*moduleDef, error)) (*moduleDef, error) {
	// Fast path
	if def := h.loadModDef(ref); def != nil {
		return def, nil
	}
	if fn == nil {
		return nil, fmt.Errorf("module %q not loaded", ref)
	}

	// Each ref can go through multiple initialization functions until the
	// module is found but we don't want to duplicate the effort in running
	// the same function for the same ref, so we cache by ref+fn.
	v, _ := h.modDefs.LoadOrStore(ref, &sync.Map{})

	switch t := v.(type) {
	case *moduleDef:
		// Some other goroutine has already stored the initialized module.
		return t, nil
	case *sync.Map:
		// A map means this ref is in the process of initialization.
		// Ensure this function is only called once.
		fnKey := fmt.Sprintf("%p", fn)
		once, _ := t.LoadOrStore(fnKey, sync.OnceValues(fn))
		switch f := once.(type) {
		case func() (*moduleDef, error):
			def, err := f()
			if err != nil || def == nil {
				return nil, err
			}
			// Module found. If multiple goroutines reach this point they should
			// have the same result from the onced function anyway.
			h.modDefs.Store(def.SourceDigest, def)
			return def, nil
		default:
			return nil, fmt.Errorf("unexpected initialization type %T for module definitions: %s", once, ref)
		}
	default:
		return nil, fmt.Errorf("unexpected loaded type %T for module definitions: %s", v, ref)
	}
}

// loadModDef returns the module definition for a module ref, if it exists
func (h *shellCallHandler) loadModDef(ref string) *moduleDef {
	if v, exists := h.modDefs.Load(ref); exists {
		if def, ok := v.(*moduleDef); ok {
			return def
		}
	}
	return nil
}

// GetDef returns the type definitions for a given state
//
// This is the main getter function for a module definition.
func (h *shellCallHandler) GetDef(st *ShellState) *moduleDef {
	dig := h.modDigest()

	if st != nil && st.ModDigest != "" && st.ModDigest != dig {
		dig = st.ModDigest
	}

	if def := h.loadModDef(dig); def != nil {
		return def
	}

	// Every time the default module ref is set, there should be a corresponding
	// value in h.modDefs. Otherwise there's a bug in the CLI.
	panic(fmt.Sprintf("module %q not loaded", dig))
}

// GetModuleDef returns the module definition for the current state
func (h *shellCallHandler) GetModuleDef(st *ShellState) (*moduleDef, error) {
	if def := h.GetDef(st); def.HasModule() {
		return def, nil
	}
	return nil, fmt.Errorf("module not loaded")
}

func (h *shellCallHandler) GetDependency(ctx context.Context, name string) (*ShellState, *moduleDef, error) {
	modDef, err := h.GetModuleDef(nil)
	if err != nil {
		return nil, nil, err
	}
	dep := modDef.GetDependency(name)
	if dep == nil {
		return nil, nil, fmt.Errorf("dependency %q not found", name)
	}
	def, err := h.getOrInitDef(dep.SourceDigest, func() (*moduleDef, error) {
		return initializeModule(ctx, h.dag, dep.Source)
	})
	if err != nil {
		return nil, nil, err
	}
	st := h.newModState(dep.SourceDigest)
	return &st, def, nil
}

func (h *shellCallHandler) debugLoadedModules() []string {
	return slices.Collect(h.loadedModuleValues(func(def *moduleDef) string {
		a, r, d := def.SourceRoot, h.modRelPath(def), def.SourceDigest
		if a != r {
			a += " → " + r
		}
		if d != "" {
			a += " (" + d + ")"
		}
		return a
	}))
}

// LoadedModulePaths returns a sorted list of the paths to all loaded modules
func (h *shellCallHandler) LoadedModulePaths() []string {
	return slices.Sorted(h.loadedModuleValues(func(def *moduleDef) string {
		return h.modRelPath(def)
	}))
}

// loadedModuleValues iterates over all loaded module definition values after applying provided function
func (h *shellCallHandler) loadedModuleValues(fn func(*moduleDef) string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for def := range h.loadedModules {
			if !yield(fn(def)) {
				return
			}
		}
	}
}

// loadedModules iterates over all loaded module definitions
func (h *shellCallHandler) loadedModules(yield func(*moduleDef) bool) {
	h.modDefs.Range(func(key, val any) bool {
		if dgst, ok := key.(string); ok && dgst != "" {
			// ignore modules that aren't fully loaded yet
			if def, ok := val.(*moduleDef); ok && def.HasModule() {
				if !yield(def) {
					return false
				}
			}
		}
		return true
	})
}

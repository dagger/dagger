package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/spf13/pflag"
	"mvdan.cc/sh/v3/interp"
)

const (
	shellHandlerExit = 200
)

// First command in pipeline: e.g., `cmd1 | cmd2 | cmd3`
func isFirstShellCommand(ctx context.Context) bool {
	return interp.HandlerCtx(ctx).Stdin == nil
}

// Exec is the main handler function, that prepares the command to be executed
// and wraps any returned errors
func (h *shellCallHandler) Exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		// This avoids interpreter builtins running first, which would make it
		// impossible to have a function named "echo", for example. We can
		// remove `.dag` from this point onward.
		if args[0] == ".dag" {
			args = args[1:]
		}

		// If argument is a state value, just pass it on to stdout.
		// Example: `$FOO` or `$FOO | bar`
		if strings.HasPrefix(args[0], shellStatePrefix) {
			hctx := interp.HandlerCtx(ctx)
			fmt.Fprint(hctx.Stdout, args[0])
			return nil
		}

		st, err := h.cmd(ctx, args)
		if err == nil && st != nil {
			if h.debug {
				shellDebug(ctx, "Stdout", args[0], args[1:], st)
			}
			err = st.Write(ctx)
		}
		if err != nil {
			m := err.Error()
			if h.debug {
				shellDebug(ctx, "Error", m, args)
			}
			// Ensure any error from the handler is written to stdout so that
			// the next command in the chain knows about it.
			if e := (ShellState{Error: &m}.Write(ctx)); e != nil {
				return fmt.Errorf("failed to encode error (%q): %w", m, e)
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

	if isFirstShellCommand(ctx) {
		return h.entrypointCall(ctx, c, a)
	}

	var b []byte
	st, b, err := shellState(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil {
		if h.debug {
			shellDebug(ctx, "InvalidStdin", args, b)
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
			def := h.modDef(st)
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

	st, err := h.stateLookup(ctx, cmd)
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

func (h *shellCallHandler) stateLookup(ctx context.Context, name string) (*ShellState, error) {
	if h.debug {
		shellDebug(ctx, "StateLookup", name)
	}

	// Is current context a loaded module?
	if md, _ := h.GetModuleDef(nil); md != nil {
		// 1. Function in current context
		if md.HasMainFunction(name) {
			return h.newState(), nil
		}

		// 2. Dependency short name
		if dep := md.GetDependency(name); dep != nil {
			depSt, _, err := h.GetDependency(ctx, name)
			return depSt, err
		}
	}

	// 3. Standard library command
	if cmd, _ := h.StdlibCommand(name); cmd != nil {
		return h.newStdlibState(), nil
	}

	// 4. Path to local or remote module source
	// (local paths are relative to the current working directory, not the loaded module)
	st, err := h.getOrInitDefState(name, func() (*moduleDef, error) {
		return tryInitializeModule(ctx, h.dag, name)
	})
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("function or module %q not found", name)
	}
	return st, nil
}

func (h *shellCallHandler) getOrInitDefState(ref string, fn func() (*moduleDef, error)) (*ShellState, error) {
	def, err := h.getOrInitDef(ref, fn)
	if err != nil || def == nil {
		return nil, err
	}
	return h.newModState(ref), nil
}

func (h *shellCallHandler) constructorCall(ctx context.Context, md *moduleDef, st *ShellState, args []string) (*ShellState, error) {
	fn := md.MainObject.AsObject.Constructor

	values, err := h.parseArgumentValues(ctx, md, fn, args)
	if err != nil {
		return nil, fmt.Errorf("constructor: %w", err)
	}

	return st.WithCall(fn, values), nil
}

// functionCall is executed for every command that the exec handler processes
func (h *shellCallHandler) functionCall(ctx context.Context, st *ShellState, name string, args []string) (*ShellState, error) {
	def := h.modDef(st)
	call := st.Function()

	fn, err := call.GetNextDef(def, name)
	if err != nil {
		return st, err
	}

	argValues, err := h.parseArgumentValues(ctx, def, fn, args)
	if err != nil {
		return st, fmt.Errorf("could not parse arguments for function %q: %w", fn.CmdName(), err)
	}

	return st.WithCall(fn, argValues), nil
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
func shellPreprocessArgs(fn *modFunction, args []string) ([]string, error) {
	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)

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
		return args, err
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

// parseArgumentValues returns a map of argument names and their parsed values
func (h *shellCallHandler) parseArgumentValues(ctx context.Context, md *moduleDef, fn *modFunction, args []string) (map[string]any, error) {
	newArgs, err := shellPreprocessArgs(fn, args)
	if err != nil {
		return nil, err
	}

	if h.debug {
		shellDebug(ctx, "Process args", fn.CmdName(), args, newArgs)
	}

	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)
	flags.SetOutput(interp.HandlerCtx(ctx).Stderr)

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
		v, bypass, err := h.parseFlagValue(ctx, value, a.TypeDef)
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
		return nil, err
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
func (h *shellCallHandler) parseFlagValue(ctx context.Context, value string, argType *modTypeDef) (any, bool, error) {
	if !strings.HasPrefix(value, shellStatePrefix) {
		return value, false, nil
	}

	var bypass bool

	handleObjectID := func(_ context.Context, q *querybuilder.Selection, t *modTypeDef) (*querybuilder.Selection, error) {
		// When an argument returns an object, assume we want its ID
		// TODO: Allow ids in TypeDefs so we can directly check if there's an `id`
		// function in this object.
		if t.AsFunctionProvider() != nil {
			if argType.Name() != t.Name() {
				return nil, fmt.Errorf("expected return type %q, got %q", argType.Name(), t.Name())
			}
			q = q.Select("id")
			bypass = true
		}

		// TODO: do a bit more validation. Consider that values that are not
		// to be replaced should only be strings, because that's what the
		// flagSet supports. This also means the type won't match the expected
		// definition. For example, a function that returns a `Directory` object
		// could have a subshell return a path string so the flag will turn that
		// into the `Directory` object.

		return q, nil
	}
	v, err := h.Result(ctx, strings.NewReader(value), false, handleObjectID)
	return v, bypass, err
}

// Result reads the state from stdin and returns the final result
func (h *shellCallHandler) Result(
	ctx context.Context,
	// r is the reader to read the shell state from
	r io.Reader,
	// doPrintResponse prepares the response for printing according to an output
	// format
	doPrintResponse bool,
	// beforeRequest is a callback that allows modifying the query before making
	// the request
	//
	// It's also useful for validating the query with the function's
	// return type.
	beforeRequest func(context.Context, *querybuilder.Selection, *modTypeDef) (*querybuilder.Selection, error),
) (any, error) {
	st, b, err := readShellState(r)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return string(b), nil
	}

	if st.IsCommandRoot() {
		switch {
		case st.IsStdlib():
			return h.CommandsList(st.Cmd, h.Stdlib()), nil
		case st.IsDeps():
			return h.DependenciesList(), nil
		case st.IsCore():
			def := h.modDef(nil)
			return h.FunctionsList(st.Cmd, def.GetCoreFunctions()), nil
		default:
			return nil, fmt.Errorf("unexpected namespace %q", st.Cmd)
		}
	}

	def := h.modDef(st)

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

	q := st.QueryBuilder(h.dag)
	if beforeRequest != nil {
		q, err = beforeRequest(ctx, q, fn.ReturnType)
		if err != nil {
			return nil, err
		}
	}

	// The beforeRequest hook has a chance to return a nil `q` to signal
	// that we shouldn't proceed with the request. For example, it's
	// possible  that a pipeline ending in an object doesn't have anything
	// to sub-select.
	if q == nil {
		return nil, nil
	}

	var response any

	if err := makeRequest(ctx, q, &response); err != nil {
		return nil, err
	}

	if fn.ReturnType.Kind == dagger.TypeDefKindVoidKind {
		return nil, nil
	}

	if doPrintResponse {
		buf := new(bytes.Buffer)
		frmt := outputFormat(fn.ReturnType)
		if err := printResponse(buf, response, frmt); err != nil {
			return nil, err
		}
		return buf.String(), nil
	}

	return response, nil
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
			h.modDefs.Store(ref, def)
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

// modDef returns the module definition for a given state
//
// This is the main getter function for a module definition.
func (h *shellCallHandler) modDef(st *ShellState) *moduleDef {
	h.mu.RLock()
	ref := h.modRef
	h.mu.RUnlock()

	if st != nil && st.ModRef != "" && st.ModRef != ref {
		ref = st.ModRef
	}
	if def := h.loadModDef(ref); def != nil {
		return def
	}

	// Every time h.modRef is set, there should be a corresponding value in
	// h.modDefs. Otherwise there's a bug in the CLI.
	panic(fmt.Sprintf("module %q not loaded", ref))
}

func (h *shellCallHandler) GetModuleDef(st *ShellState) (*moduleDef, error) {
	if def := h.modDef(st); def.HasModule() {
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
	st, err := h.getOrInitDefState(dep.ModRef, func() (*moduleDef, error) {
		var opts []dagger.ModuleSourceOpts
		if dep.RefPin != "" {
			opts = append(opts, dagger.ModuleSourceOpts{RefPin: dep.RefPin})
		}
		return initializeModule(ctx, h.dag, dep.ModRef, false, opts...)
	})
	if err != nil {
		return nil, nil, err
	}
	def, err := h.GetModuleDef(st)
	if err != nil {
		return nil, nil, err
	}
	return st, def, nil
}

// LoadedModulesList returns a sorted list of module references that are loaded and cached
func (h *shellCallHandler) LoadedModulesList() []string {
	var mods []string
	h.modDefs.Range(func(key, val any) bool {
		if modRef, ok := key.(string); ok && modRef != "" {
			// ignore modules that aren't fully loaded yet
			if _, ok := val.(*moduleDef); ok {
				mods = append(mods, modRef)
			}
		}
		return true
	})
	slices.Sort(mods)
	return mods
}

// IsDefaultModule returns true if the given module reference is the default loaded module
func (h *shellCallHandler) IsDefaultModule(ref string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return ref == "" || ref == h.modRef
}

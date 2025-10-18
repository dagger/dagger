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
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/sourcegraph/conc/pool"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/interp"
)

const (
	// shellInternalCmd is the command that is used internally to avoid conflicts
	// with interpreter builtins. For example when `echo` is used, the command becomes
	// `__dag echo`. Otherwise we can't have a function named `echo`.
	shellInternalCmd = "__dag"

	// shellInterpBuiltinPrefix is the prefix that users should add to an
	// interpreter builtin command to force running it.
	shellInterpBuiltinPrefix = "_"
)

// HandlerError attatches an exit code to an error returned by the handler.
//
// Could be replaced with `errors.Join(err, interp.ExitStatus(exit))` but
// it always adds a "exit status X" message to the error, so this allows
// printing only the original but still have `errors.As()` work with
// `interp.ExitStatus`, which is necessary for the interpreter.
//
// We want fatal errors but adding `interp.ExitStatus` makes it non-fatal
// so we always add it to get a consistent behavior and rely on
// `set -eo pipefail` to make it fatal.
//
// The discrepancy is due to the interpreter library considering errors
// without `interp.ExitStatus“ to be unexpected and thus fatal by default
// (opt-out, not opt-in), to avoid going unnoticed.
type HandlerError struct {
	// Err is the original error
	Err error
	// ExitCode is the exit status code
	ExitCode int
}

func (e *HandlerError) Error() string {
	return e.Err.Error()
}

func (e *HandlerError) Unwrap() []error {
	return []error{e.Err, interp.ExitStatus(e.ExitCode)}
}

func NewHandlerError(err error) *HandlerError {
	exit := 1

	// Currently only dagger.ExecError produces an exit code > 1.
	var exe *dagger.ExecError
	if errors.As(err, &exe) {
		exit = exe.ExitCode
	}

	return &HandlerError{
		Err:      err,
		ExitCode: exit,
	}
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

	// We may allow some interpreter builtins to be used as dagger shell
	// builtins but rather than calling the interpreter builtin from the
	// command's Run function it's simpler to use ShellCommand just for the
	// documentation (.help) but strip the builtin prefix here ('.') when executing
	// so the interpreter runs the builtin directly instead of our exec handler.
	if after, ok := strings.CutPrefix(args[0], "."); ok && interp.IsBuiltin(after) {
		if cmd, _ := h.BuiltinCommand(args[0]); cmd != nil && cmd.Run == nil {
			args[0] = shellInterpBuiltinPrefix + after
		}
	}

	// When there's a Dagger function with a name that conflicts
	// with an interpreter builtin, the Dagger function is favored.
	// To force the builtin to execute instead, prefix the command
	// with "_". For example: "container | from $(_echo alpine)".
	if after, found := strings.CutPrefix(args[0], shellInterpBuiltinPrefix); found && interp.IsBuiltin(after) {
		// Make sure to resolve state in arguments since this is going
		// to be handed off to the interpreter.
		a, err := h.resolveResults(ctx, args[1:])
		args = append([]string{after}, a...)
		return args, err
	}

	// If the command is an interpreter builtin (no prefix), bypass the
	// interpreter and run our exec handler instead to ensure module functions
	// take precedence.
	if interp.IsBuiltin(args[0]) {
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
	return func(ctx context.Context, args []string) (rerr error) {
		// This avoids interpreter builtins running first, which would make it
		// impossible to have a function named "echo", for example. We can
		// remove `__dag` from this point onward.
		if args[0] == shellInternalCmd {
			args = args[1:]
		}

		// If argument is a state value, just pass it on to stdout directly.
		// Example: `$FOO` or `$FOO | bar`
		if GetStateKey(args[0]) != "" {
			hctx := interp.HandlerCtx(ctx)
			fmt.Fprint(hctx.Stdout, args[0])
			return nil
		}

		// It's a cascading error if the state from the previous handler in a
		// pipeline failed.
		var cascadingErr bool

		// Read stdin. If not nil this will block until the previous handler
		// has finished. If state is nil here it means it's the first command
		// in a pipeline (foo | bar).
		st, err := h.loadInput(ctx, args)
		if st != nil {
			cascadingErr = st.IsHandlerError()
		}

		// Having a span for each handler makes it much easier to debug what
		// the shell is doing.
		opts := make([]trace.SpanStartOption, 0, 2)
		opts = append(opts, trace.WithAttributes(attribute.StringSlice("dagger.io/shell.handler.args", args)))
		// Don't show span by default unless there's an error or we're debugging
		// (with `.debug`).
		if !h.Debug() {
			opts = append(opts, telemetry.Passthrough())
		}
		ctx, span := Tracer().Start(ctx, args[0], opts...)
		defer telemetry.End(span, func() error {
			if cascadingErr {
				// Early exit if an error is passed through stdin.
				span.SetAttributes(
					attribute.Bool(telemetry.CanceledAttr, true),
				)
			} else if rerr != nil {
				// TODO: it's helpful to show the span on error when it's a usage
				// issue, but if it's from resolving a query it shows the error
				// twice. Could still be useful though, to pinpoint exactly which
				// part of the script triggered it.
				attrs := []attribute.KeyValue{
					attribute.Bool(telemetry.UIPassthroughAttr, false),
				}
				var he *HandlerError
				if errors.As(rerr, &he) {
					attrs = append(attrs, attribute.Int("dagger.io/shell.handler.exit", he.ExitCode))
				}
				span.SetAttributes(attrs...)
			}
			return rerr
		})

		slog := slog.SpanLogger(ctx, InstrumentationLibrary)

		// No error from loading input, so look for which function to call.
		if err == nil {
			st, err = h.cmd(ctx, args, st)

			// Command returned a state for saving.
			// Condition is nested just for clarity.
			if err == nil && st != nil {
				// The last command in a pipeline will resolve this state and
				// if that query returns an error, it will be returned here.
				// Otherwise this should only fail if there's an unexpected
				// error while writing to the pipe the interpreter sets up.
				err = h.Save(ctx, *st)
			}
		}

		// At this point the error could come from stdin, processing a function
		// call, or resolving a final query.
		if err != nil {
			if h.Debug() {
				slog.Debug("handler error", "err", err, "args", args)
			}

			if !cascadingErr {
				err = NewHandlerError(err)
			}

			if st == nil {
				st = &ShellState{}
			}

			// Ensure any error from the handler is written to stdout so that
			// the next command in the pipeline knows about it.
			//
			// st.Error will already be set when previous handler failed so
			// don't override its propagation.
			//
			// Current state kept simply as a precaution in case it helps
			// debug what state produced the error, but we could simplify and
			// always write a new empty state here.
			if st.Error == nil {
				st.Error = err
			}
			if e := h.Save(ctx, *st); e != nil {
				// Save is expected to return the current HandlerError if it's
				// the last command in a pipeline or no error if it's not.
				// Otherwise it has to be an unexpected failure when writing
				// to the pipe that the interpreter sets up.
				var he *HandlerError
				if !errors.As(e, &he) {
					// If we fail to pass the current error on to the next
					// handler in the pipeline the next one will return a
					// confiusing "unexpected input" error, but it's still
					// better than obfuscating the original one if we returned
					// here, so just log it.
					slog.Error("failed to save error state", "args", args, "err", e)
				}
			}

			if cascadingErr {
				return nil
			}

			return err
		}

		return nil
	}
}

func (h *shellCallHandler) loadInput(ctx context.Context, args []string) (*ShellState, error) {
	stdin := interp.HandlerCtx(ctx).Stdin

	// First command in pipeline: e.g., `cmd1 | cmd2 | cmd3`
	if stdin == nil {
		return nil, nil
	}

	b, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	s := string(b)

	// Stdin expects a single state
	st, err := h.state.Load(GetStateKey(s))
	if st == nil && err == nil {
		if h.Debug() {
			// Need `.debug` to check what was actually passed. Just write
			// to stderr instead of logger.
			shellDebug(ctx, "unexpected stdin", args, s)
		}
		return nil, fmt.Errorf("unexpected input for command %q", args[0])
	}

	// Should pass state even if err != nil, for cleanup.
	return st, err
}

// cmd is the main logic for executing simple commands
func (h *shellCallHandler) cmd(ctx context.Context, args []string, st *ShellState) (*ShellState, error) {
	c, a := args[0], args[1:]

	// First command in pipeline: e.g., `cmd1 | cmd2 | cmd3`
	if st == nil {
		return h.entrypointCall(ctx, c, a)
	}

	if h.Debug() {
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
	if h.Debug() {
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
	if h.Debug() {
		shellDebug(ctx, "StateLookup", name)
	}

	// Is current context a loaded module?
	if md, _ := h.GetModuleDef(nil); md != nil {
		// 1. Function in current context
		if md.HasMainFunction(name) {
			st := h.NewState()
			return &st, nil
		}

		// 2. Is it the current module's name?
		if md.Name == name {
			st := h.newModState(md.SourceDigest)
			return &st, nil
		}

		// 3. Dependency short name
		if dep := md.GetDependency(name); dep != nil {
			depSt, _, err := h.GetDependency(ctx, name)
			return depSt, err
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
func (h *shellCallHandler) shellPreprocessArgs(
	ctx context.Context,
	fn *modFunction,
	args []string,
) (map[string]any, []string, error) {
	// Final map of resolved argument values
	values := make(map[string]any, len(fn.Args))

	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)
	flags.SetOutput(io.MultiWriter(
		interp.HandlerCtx(ctx).Stderr,
		telemetry.SpanStdio(ctx, InstrumentationLibrary).Stderr,
	))

	opts := fn.OptionalArgs()
	reqs := fn.RequiredArgs()

	// All CLI arguments are strings at first, but booleans can be omitted.
	// We don't wan't to process values yet, just validate and consume the flags
	// so we get the remaining positional args.

	// Add required arguments as flags so they can be specified as named arguments
	for _, arg := range reqs {
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

	// Add optional arguments as flags
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
		return values, args, checkErrHelp(err, args)
	}

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

		// bypass additional flag parsing, but make sure state values are resolved
		results, err := h.resolveResults(ctx, pos)
		if err != nil {
			return values, pos, err
		}
		values[name] = results
	} else {
		// Collect which required arguments were provided as named flags
		providedAsFlags := make(map[string]bool)
		for _, arg := range reqs {
			flag := flags.Lookup(arg.FlagName())
			if flag != nil && flag.Changed {
				providedAsFlags[arg.FlagName()] = true
			}
		}

		// Calculate how many required arguments still need to be filled by positional args
		remainingRequiredCount := len(reqs) - len(providedAsFlags)

		// Validate that we have the right number of positional arguments
		// for the remaining required arguments
		if len(pos) != remainingRequiredCount {
			return values, args, fmt.Errorf("requires %d positional argument(s), received %d", remainingRequiredCount, len(pos))
		}

		a = make([]string, 0, len(fn.Args))

		// Process required arguments in order, using positional args for those
		// not provided as named flags
		posIndex := 0
		for _, arg := range reqs {
			flagName := arg.FlagName()
			if providedAsFlags[flagName] {
				// This required argument was provided as a named flag
				// It will be handled in the flags.Visit loop below
				continue
			} else if posIndex < len(pos) {
				// This required argument needs to be filled from positional args
				a = append(a, fmt.Sprintf("--%s=%v", flagName, pos[posIndex]))
				posIndex++
			}
		}
	}

	// Add all the flags (both required and optional that were specified)
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

	return values, a, nil
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
	values, newArgs, err := h.shellPreprocessArgs(ctx, fn, args)
	if err != nil {
		return nil, errors.Join(err, fmt.Errorf("usage: %s", h.FunctionFullUseLine(md, fn)))
	}

	// Flag processing can be a source of bugs so it's very useful to be
	// able to debug this step but excessive on default verbosity.
	if debugFlag && verbose > 3 && !slices.Equal(args, newArgs) {
		dbgArgs := []any{
			"function", fn.CmdName(),
			"before", args,
			"after", newArgs,
		}
		if len(values) > 0 {
			dbgArgs = append(dbgArgs, "values", values)
		}
		slog := slog.SpanLogger(ctx, InstrumentationLibrary)
		slog.Debug("preprocess function argument flags", dbgArgs...)
	}

	// no further processing needed
	if len(newArgs) == 0 {
		return values, nil
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

	// Parse arguments using flags to get the values matched with the right
	// argument definition. Bypass the flag if the argument value is an object
	// ID, otherwise set the flag value.
	f := func(flag *pflag.Flag, value string) error {
		a, err := fn.GetArg(flag.Name)
		if err != nil {
			return err
		}
		v, bypass, err := h.parseFlagValue(ctx, value, a)
		if err != nil {
			return fmt.Errorf("cannot expand function argument %q: %w", a.FlagName(), err)
		}

		// Bypass will only be true if the value is a resolved state returning
		// an object ID. Since custom flags don't support object ID values
		// bypass calling flag.Get() for this argument in that case. An
		// object ID is already a final value.
		if !bypass {
			return flags.Set(flag.Name, v)
		}

		if a.TypeDef.Kind == dagger.TypeDefKindListKind {
			// Final values are of type `any` and for a slice flag each
			// element will go through this parsing function independently.
			// For non object IDs the `flags.Set()` above already handles this
			// (for slice flags).
			if curr, exists := values[a.Name]; exists {
				list, ok := curr.([]string)
				if !ok {
					return fmt.Errorf("expected an object ID string for function argument %q, got %T", a.FlagName(), curr)
				}
				values[a.Name] = append(list, v)
				return nil
			}

			values[a.Name] = []string{v}
			return nil
		}

		values[a.Name] = v
		return nil
	}
	if err := flags.ParseAll(newArgs, f); err != nil {
		return nil, checkErrHelp(err, newArgs)
	}

	// Finally, get the values from the flags that haven't been resolved yet.
	type flagResult struct {
		idx   int
		flag  string
		value any
	}

	p := pool.NewWithResults[flagResult]().WithErrors()

	for i, a := range fn.Args {
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
		p.Go(func() (flagResult, error) {
			val, err := a.GetFlagValue(ctx, flag, h.dag, md)
			if err != nil {
				return flagResult{}, err
			}
			return flagResult{i, a.Name, val}, nil
		})
	}

	vals, err := p.Wait()
	if err != nil {
		return nil, err
	}

	for _, val := range vals {
		values[val.flag] = val.value
	}

	return values, nil
}

// parseFlagValue ensures that a flag value with state gets resolved
//
// This happens most commonly when argument is the result of command expansion
// from a sub-shell.
func (h *shellCallHandler) parseFlagValue(ctx context.Context, value string, arg *modFunctionArg) (rval string, bypass bool, rerr error) {
	argType := arg.TypeDef
	states := FindStateTokens(value)

	if len(states) == 0 {
		if argType.AsObject != nil {
			switch argType.AsObject.Name {
			case Directory, File:
				// Ignore on git urls since the flag will parse it directly.
				// We just need to resolve the ref for "local" (contextual) paths.
				if _, err := gitutil.ParseURL(value); err != nil {
					if newPath, err := h.contextArgRef(value); err == nil {
						return newPath, false, nil
					}
				}
			}
		}
		return value, false, nil
	}

	// If value isn't one state exactly, we need to process into a string
	if len(states) > 1 || states[0] != value {
		r, err := h.resolveResult(ctx, value)
		return r, false, err
	}

	// Otherwise it may be an object that we want to bypass (for its ID)
	st, err := h.state.Extract(ctx, GetStateKey(value))
	if err != nil {
		return "", false, err
	}
	r, err := h.StateResult(ctx, st)
	if err != nil {
		return "", false, err
	}
	if r.IsObject() {
		id, ok := r.Value.(string)
		if !ok {
			return id, true, fmt.Errorf("expected an object ID string, got %T", r.Value)
		}
		return id, true, nil
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
		return initializeModule(ctx, h.dag, dep.SourceRoot, dep.Source)
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

// HandlerCtx returns interp.HandlerContext value stored in ctx, or nil if
// it doesn't have one.
func HandlerCtx(ctx context.Context) (ret *interp.HandlerContext) {
	defer func() {
		if err := recover(); err != nil {
			ret = nil
		}
	}()
	hc := interp.HandlerCtx(ctx)
	return &hc
}

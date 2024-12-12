package main

import (
	"slices"
	"strings"

	"github.com/chzyer/readline"
	"mvdan.cc/sh/v3/syntax"
)

// shellAutoComplete is a wrapper for the shell call handler
type shellAutoComplete struct {
	// This is separated out, since we don't want to have to attach all these
	// methods to the shellCallHandler directly
	*shellCallHandler
}

var _ readline.AutoCompleter = (*shellAutoComplete)(nil)

func (h *shellAutoComplete) Do(line []rune, pos int) (newLine [][]rune, length int) {
	file, err := parseShell(strings.NewReader(string(line)), "")
	if err != nil {
		return nil, 0
	}

	// find the smallest stmt next to the cursor - this allows accurate
	// completion inside subshells, for example
	var stmt *syntax.Stmt
	excluded := map[*syntax.Stmt]struct{}{}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.BinaryCmd:
			if node.Op == syntax.Pipe {
				// pipes are special, and those statements aren't atomic
				// because they're chained off of the previous ones - so avoid
				// isolating them
				excluded[node.X] = struct{}{}
				excluded[node.Y] = struct{}{}
			}
		case *syntax.Stmt:
			if stmt == nil {
				stmt = node
				break
			}
			if pos < int(node.Pos().Offset()) || pos > int(node.End().Offset()) {
				return false
			}
			if _, ok := excluded[node]; !ok {
				stmt = node
			}
		}
		return true
	})

	var inprogressWord *syntax.Word
	syntax.Walk(file, func(node syntax.Node) bool {
		if node, ok := node.(*syntax.Word); ok {
			if node.End().Offset() == uint(pos) {
				inprogressWord = node
				return false
			}
		}
		return true
	})
	var inprogressPrefix string
	if inprogressWord != nil {
		inprogressPrefix = inprogressWord.Lit()
	}

	// discard the in-progress word for the process of determining the
	// auto-completion context (since it's likely to be invalid)
	var cursor uint
	if inprogressWord == nil {
		cursor = uint(pos)
	} else {
		cursor = inprogressWord.Pos().Offset()
	}

	shctx := h.root()
	if stmt != nil {
		shctx = h.dispatch(shctx, stmt, cursor)
	}
	if shctx == nil {
		return nil, 0
	}

	var results [][]rune
	for _, result := range shctx.completions(inprogressPrefix) {
		if result, ok := strings.CutPrefix(result, inprogressPrefix); ok {
			results = append(results, []rune(result+" "))
		}
	}
	return results, len(inprogressPrefix)
}

func (h *shellAutoComplete) dispatch(previous *CompletionContext, stmt *syntax.Stmt, cursor uint) *CompletionContext {
	if stmt == nil {
		return previous
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		return h.dispatchCall(previous, cmd, cursor)
	case *syntax.BinaryCmd:
		return h.dispatchPipe(previous, cmd, cursor)
	}
	return nil
}

func (h *shellAutoComplete) dispatchCall(previous *CompletionContext, call *syntax.CallExpr, cursor uint) *CompletionContext {
	if call.Pos().Offset() >= cursor {
		// short-circuit calls once we get past the current cursor context
		return previous
	}

	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		args = append(args, arg.Lit())
	}
	return previous.lookupField(args[0], args[1:])
}

func (h *shellAutoComplete) dispatchPipe(previous *CompletionContext, pipe *syntax.BinaryCmd, cursor uint) *CompletionContext {
	if pipe.Op != syntax.Pipe {
		return nil
	}

	previous = h.dispatch(previous, pipe.X, cursor)
	if previous == nil {
		return nil
	}

	if pipe.OpPos.Offset() >= cursor {
		// short-circuit pipes once we get past the current cursor context
		return previous
	}
	previous = previous.lookupType()
	if previous == nil {
		return nil
	}

	return h.dispatch(previous, pipe.Y, cursor)
}

func (h *shellAutoComplete) root() *CompletionContext {
	return &CompletionContext{
		Completer: h,
		root:      true,
	}
}

// CompletionContext provides completions for a specific point in a command
// chain. Each point is represented by one of `Mod` prefixed fields being set
// at a time.
type CompletionContext struct {
	Completer *shellAutoComplete

	// CmdRoot is the name of a namespace-setting command.
	CmdRoot string

	// ModType indicates the completions should be performed on an
	// object/interface/etc.
	ModType functionProvider

	// ModFunc indicates the completions should be performed on the arguments
	// for a function call.
	ModFunction *modFunction

	root bool
}

func (ctx *CompletionContext) completions(prefix string) []string {
	var results []string
	switch {
	case ctx.ModFunction != nil:
		// TODO: also complete required args sometimes (depending on type)

		// complete optional args
		if strings.HasPrefix(prefix, "-") {
			for _, arg := range ctx.ModFunction.OptionalArgs() {
				flag := "--" + arg.FlagName()
				results = append(results, flag)
			}
		}

	case ctx.ModType != nil:
		// complete possible functions for this type
		for _, f := range ctx.ModType.GetFunctions() {
			results = append(results, f.CmdName())
		}
		// complete potentially chainable builtins
		for _, builtin := range ctx.builtins() {
			results = append(results, builtin.Name())
		}

	case ctx.root:
		for _, cmd := range slices.Concat(ctx.builtins(), ctx.stdlib()) {
			results = append(results, cmd.Name())
		}
		if md, _ := ctx.Completer.GetModuleDef(nil); md != nil {
			for _, fn := range md.MainObject.AsFunctionProvider().GetFunctions() {
				results = append(results, fn.CmdName())
			}
			for _, dep := range md.Dependencies {
				results = append(results, dep.Name)
			}
		}
		for modRef := range ctx.Completer.modDefs {
			if modRef != "" {
				results = append(results, modRef)
			}
		}

	case ctx.CmdRoot == shellStdlibCmdName:
		for _, cmd := range ctx.Completer.Stdlib() {
			results = append(results, cmd.Name())
		}

	case ctx.CmdRoot == shellDepsCmdName:
		if md, _ := ctx.Completer.GetModuleDef(nil); md != nil {
			for _, dep := range md.Dependencies {
				results = append(results, dep.Name)
			}
		}

	case ctx.CmdRoot == shellCoreCmdName:
		for _, fn := range ctx.Completer.modDef(nil).GetCoreFunctions() {
			results = append(results, fn.CmdName())
		}
	}

	return results
}

func (ctx *CompletionContext) lookupField(field string, args []string) *CompletionContext {
	if cmd := ctx.builtinCmd(field); cmd != nil {
		return cmd.Complete(ctx, args)
	}

	def := ctx.Completer.modDef(nil)

	if ctx.ModType != nil {
		next, err := def.GetFunction(ctx.ModType, field)
		if err != nil {
			return nil
		}
		return &CompletionContext{
			Completer:   ctx.Completer,
			ModFunction: next,
		}
	}

	// Limit options for these namespace-setting commands
	switch ctx.CmdRoot {
	case shellStdlibCmdName:
		if cmd := ctx.stdlibCmd(field); cmd != nil {
			return cmd.Complete(ctx, args)
		}
	case shellDepsCmdName:
		// TODO: loading other modules isn't supported yet
		return nil

	case shellCoreCmdName:
		if fn := def.GetCoreFunction(field); fn != nil {
			return &CompletionContext{
				Completer:   ctx.Completer,
				ModFunction: fn,
			}
		}
	}

	// Default lookup and fallbacks after this point, which only happens
	// when it's the first command.
	if !ctx.root {
		return nil
	}

	// 1. Current module function
	if def.HasMainFunction(field) {
		next, err := def.GetFunction(def.MainObject.AsFunctionProvider(), field)
		if err != nil {
			return nil
		}
		return &CompletionContext{
			Completer:   ctx.Completer,
			ModFunction: next,
		}
	}

	// 2. Dependency
	if dep := def.GetDependency(field); dep != nil {
		// TODO: loading other modules isn't supported yet
		return nil
	}

	// 3. Stdlib
	if cmd := ctx.stdlibCmd(field); cmd != nil {
		return cmd.Complete(ctx, args)
	}

	// 4. Module reference
	// TODO: loading other modules isn't supported yet
	if field == ctx.Completer.modRef {
		return &CompletionContext{
			Completer:   ctx.Completer,
			ModFunction: def.MainObject.AsObject.Constructor,
		}
	}

	return nil
}

func (ctx *CompletionContext) lookupType() *CompletionContext {
	if ctx.ModType != nil || ctx.CmdRoot != "" {
		return ctx
	}
	if ctx.ModFunction != nil {
		def := ctx.Completer.modDef(nil)
		next := def.GetFunctionProvider(ctx.ModFunction.ReturnType.Name())
		return &CompletionContext{
			Completer: ctx.Completer,
			ModType:   next,
		}
	}
	return nil
}

func (ctx *CompletionContext) builtins() []*ShellCommand {
	var cmds []*ShellCommand
	for _, cmd := range ctx.Completer.Builtins() {
		if ctx.root && cmd.State != RequiredState || !ctx.root && cmd.State != NoState {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (ctx *CompletionContext) stdlib() []*ShellCommand {
	var cmds []*ShellCommand
	for _, cmd := range ctx.Completer.Stdlib() {
		if ctx.root && cmd.State != RequiredState || !ctx.root && cmd.State != NoState {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (ctx *CompletionContext) builtinCmd(name string) *ShellCommand {
	for _, cmd := range ctx.builtins() {
		if cmd.Name() == name {
			if cmd.Complete == nil {
				return nil
			}
			return cmd
		}
	}
	return nil
}

func (ctx *CompletionContext) stdlibCmd(name string) *ShellCommand {
	for _, cmd := range ctx.stdlib() {
		if cmd.Name() == name {
			if cmd.Complete == nil {
				return nil
			}
			return cmd
		}
	}
	return nil
}

package main

import (
	"strings"

	"github.com/vito/tuist"
	"mvdan.cc/sh/v3/syntax"
)

// shellAutoComplete is a wrapper for the shell call handler
type shellAutoComplete struct {
	// This is separated out, since we don't want to have to attach all these
	// methods to the shellCallHandler directly
	*shellCallHandler
}

func (h *shellAutoComplete) Complete(input string, cursorPos int) tuist.CompletionResult {
	line := input[:cursorPos]
	pos := cursorPos

	file, err := parseShell(strings.NewReader(line), "", syntax.RecoverErrors(5))
	if err != nil {
		return tuist.CompletionResult{}
	}

	// find the smallest stmt next to the cursor - this allows accurate
	// completion inside subshells, for example
	var stmt *syntax.Stmt
	stmtSize := len(line) + 1
	excluded := map[*syntax.Stmt]struct{}{}
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			return false
		}

		start := int(node.Pos().Offset())
		end := int(node.End().Offset())
		if node.End().IsRecovered() {
			end = pos
		}
		if pos < start || pos > end {
			return true
		}
		size := end - start
		if size > stmtSize {
			return true
		}

		switch node := node.(type) {
		case *syntax.BinaryCmd:
			if node.Op == syntax.Pipe {
				// pipes are special, and those statements aren't atomic
				// because they're chained off of the previous ones - so avoid
				// isolating them
				excluded[node.X] = struct{}{}
				excluded[node.Y] = struct{}{}
			}
		case *syntax.CmdSubst:
			if len(node.Stmts) > 0 {
				break
			}
			stmt = nil
			stmtSize = size
		case *syntax.Stmt:
			if stmt == nil {
				stmt = node
				break
			}
			if _, ok := excluded[node]; ok {
				break
			}
			stmt = node
			stmtSize = size
		}
		return true
	})

	var inprogressWord *syntax.Word
	syntax.Walk(file, func(node syntax.Node) bool {
		if node, ok := node.(*syntax.Word); ok {
			if node.End().IsValid() && node.End().Offset() == uint(pos) {
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
		return tuist.CompletionResult{}
	}

	completions := shctx.completions(inprogressPrefix)
	var items []tuist.Completion
	suggested := map[string]struct{}{}
	for _, c := range completions {
		if strings.HasPrefix(c, inprogressPrefix) {
			if _, ok := suggested[c]; ok {
				continue
			}
			items = append(items, tuist.Completion{Label: c})
			suggested[c] = struct{}{}
		}
	}
	return tuist.CompletionResult{
		Items:       items,
		ReplaceFrom: pos - len(inprogressPrefix),
	}
}

func (h *shellAutoComplete) dispatch(previous *CompletionContext, stmt *syntax.Stmt, cursor uint) *CompletionContext {
	if stmt == nil {
		return previous
	}
	switch cmd := stmt.Cmd.(type) {
	case nil:
		return previous
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
	if len(args) == 0 {
		return previous
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
		def := ctx.Completer.GetDef(nil)
		for _, fn := range def.MainObject.AsFunctionProvider().GetFunctions() {
			results = append(results, fn.CmdName())
		}
		for _, dep := range def.Dependencies {
			results = append(results, dep.Name)
		}
		results = append(results, ctx.Completer.LoadedModulePaths()...)
	}

	return results
}

func (ctx *CompletionContext) lookupField(field string, args []string) *CompletionContext {
	if cmd := ctx.builtinCmd(field); cmd != nil {
		return cmd.Complete(ctx, args)
	}

	def := ctx.Completer.GetDef(nil)

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

	// 3. Module reference
	// TODO: loading other modules isn't supported yet
	if ctx.Completer.IsDefaultModule(field) {
		return &CompletionContext{
			Completer:   ctx.Completer,
			ModFunction: def.MainObject.AsObject.Constructor,
		}
	}

	return nil
}

func (ctx *CompletionContext) lookupType() *CompletionContext {
	if ctx.ModFunction != nil {
		def := ctx.Completer.GetDef(nil)
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

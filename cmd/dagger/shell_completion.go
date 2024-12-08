package main

import (
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
			results = append(results, []rune(result))
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

	previous = h.dispatch(previous, pipe.Y, cursor)
	return previous
}

func (h *shellAutoComplete) root() *CompletionContext {
	def := h.modDef(nil)
	return &CompletionContext{
		Completer: h,
		ModType:   def.MainObject.AsFunctionProvider(),
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
	if ctx.ModFunction != nil {
		// TODO: also complete required args sometimes (depending on type)

		// complete optional args
		if strings.HasPrefix(prefix, "-") {
			for _, arg := range ctx.ModFunction.OptionalArgs() {
				flag := "--" + arg.FlagName() + " "
				results = append(results, flag)
			}
		}
	} else if ctx.ModType != nil {
		// complete possible functions for this type
		for _, f := range ctx.ModType.GetFunctions() {
			cmd := f.CmdName() + " "
			results = append(results, cmd)
		}
		// complete potentially chainable builtins
		for _, builtin := range ctx.builtins() {
			cmd := builtin.Name() + " "
			results = append(results, cmd)
		}
	}

	return results
}

func (ctx *CompletionContext) lookupField(field string, args []string) *CompletionContext {
	if builtin, _ := ctx.Completer.BuiltinCommand(field); builtin != nil {
		if builtin.Complete == nil {
			return nil
		}
		return builtin.Complete(ctx, args)
	}

	previous := ctx.ModType
	if previous == nil {
		previous = ctx.ModFunction.ReturnType.AsFunctionProvider()
	}
	if previous == nil {
		return nil
	}

	def := ctx.Completer.modDef(nil)
	next, err := def.GetObjectFunction(previous.ProviderName(), field)
	if err != nil {
		return nil
	}
	return &CompletionContext{
		Completer:   ctx.Completer,
		ModFunction: next,
	}
}

func (ctx *CompletionContext) lookupType() *CompletionContext {
	if ctx.ModType != nil {
		return ctx
	} else if ctx.ModFunction != nil {
		def := ctx.Completer.modDef(nil)
		next := def.GetFunctionProvider(ctx.ModFunction.ReturnType.Name())
		return &CompletionContext{
			Completer: ctx.Completer,
			ModType:   next,
		}
	} else {
		return nil
	}
}

func (ctx *CompletionContext) builtins() []*ShellCommand {
	if ctx.ModType == nil {
		return nil
	}
	var builtins []*ShellCommand
	for _, builtin := range ctx.Completer.Builtins() {
		if ctx.root && builtin.State == RequiredState {
			continue
		} else if !ctx.root && builtin.State == NoState {
			continue
		}
		builtins = append(builtins, builtin)
	}
	return builtins
}

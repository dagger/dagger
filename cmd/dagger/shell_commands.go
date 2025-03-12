package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/interp"
)

const (
	shellStdlibCmdName = ".stdlib"
	shellDepsCmdName   = ".deps"
	shellCoreCmdName   = ".core"
)

// ShellCommand is a Dagger Shell builtin or stdlib command
type ShellCommand struct {
	// Use is the one-line usage message
	Use string

	// Description is the short description shown in the '.help' output
	Description string

	// Expected arguments
	Args PositionalArgs

	// Expected state
	State StateArg

	// Run is the function that will be executed.
	Run func(ctx context.Context, cmd *ShellCommand, args []string, st *ShellState) error

	// Complete provides builtin completions
	Complete func(ctx *CompletionContext, args []string) *CompletionContext

	// HelpFunc is a custom function for customizing the help output
	HelpFunc func(cmd *ShellCommand) string

	// The group id under which this command is grouped in the '.help' output
	GroupID string

	// Hidden hides the command from `.help`
	Hidden bool
}

// Name is the command name.
func (c *ShellCommand) Name() string {
	name := c.Use
	i := strings.Index(name, " ")
	if i >= 0 {
		name = name[:i]
	}
	return name
}

// Short is the the summary for the command
func (c *ShellCommand) Short() string {
	return strings.Split(c.Description, "\n")[0]
}

func (c *ShellCommand) Help() string {
	if c.HelpFunc != nil {
		return c.HelpFunc(c)
	}
	return c.defaultHelp()
}

func (c *ShellCommand) defaultHelp() string {
	var doc ShellDoc

	if c.Description != "" {
		doc.Add("", c.Description)
	}

	doc.Add("Usage", c.Use)

	return doc.String()
}

type PositionalArgs func(args []string) error

func MinimumArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires at least %d argument(s), received %d", n, len(args))
		}
		return nil
	}
}

func MaximumArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) > n {
			return fmt.Errorf("accepts at most %d argument(s), received %d", n, len(args))
		}
		return nil
	}
}

func ExactArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires %d positional argument(s), received %d", n, len(args))
		}
		if len(args) > n {
			return fmt.Errorf("accepts at most %d positional argument(s), received %d", n, len(args))
		}
		return nil
	}
}

func NoArgs(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("received unknown %d args", len(args))
	}
	return nil
}

type StateArg uint

const (
	AnyState StateArg = iota
	RequiredState
	NoState
)

// Execute is the main dispatcher function for shell builtin commands
func (c *ShellCommand) Execute(ctx context.Context, h *shellCallHandler, args []string, st *ShellState) error {
	switch c.State {
	case AnyState:
	case RequiredState:
		if st == nil {
			return fmt.Errorf("command %q must be piped\n\nUsage: %s", c.Name(), c.Use)
		}
	case NoState:
		if st != nil {
			return fmt.Errorf("command %q cannot be piped\n\nUsage: %s", c.Name(), c.Use)
		}
	}
	if c.Args != nil {
		if err := c.Args(args); err != nil {
			return fmt.Errorf("command %q %w\n\nUsage: %s", c.Name(), err, c.Use)
		}
	}
	// resolve state values in arguments
	a, err := h.resolveResults(ctx, args)
	if err != nil {
		return err
	}
	if h.debug {
		shellDebug(ctx, "Command: "+c.Name(), a, st)
	}
	return c.Run(ctx, c, a, st)
}

func (h *shellCallHandler) BuiltinCommand(name string) (*ShellCommand, error) {
	if name == "." || !strings.HasPrefix(name, ".") || strings.Contains(name, "/") {
		return nil, nil
	}
	for _, c := range h.builtins {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("command not found: %q", name)
}

func (h *shellCallHandler) StdlibCommand(name string) (*ShellCommand, error) {
	for _, c := range h.stdlib {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("command not found: %q", name)
}

func (h *shellCallHandler) Builtins() []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.builtins))
	for _, c := range h.builtins {
		if !c.Hidden {
			l = append(l, c)
		}
	}
	return l
}

func (h *shellCallHandler) Stdlib() []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.stdlib))
	for _, c := range h.stdlib {
		if !c.Hidden {
			l = append(l, c)
		}
	}
	return l
}

func (h *shellCallHandler) registerCommands() { //nolint:gocyclo
	var builtins []*ShellCommand
	var stdlib []*ShellCommand

	builtins = append(builtins,
		&ShellCommand{
			Use:    ".debug",
			Hidden: true,
			Args:   NoArgs,
			State:  NoState,
			Run: func(_ context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				// Toggles debug mode, which can be useful when in interactive mode
				h.debug = !h.debug
				return nil
			},
		},
		&ShellCommand{
			Use:         ".help [command | function | module | type]\n<function> | .help [function]",
			Description: `Show documentation for a command, function, module, or type.`,
			Args:        MaximumArgs(1),
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, st *ShellState) error {
				var err error

				// First command in chain
				if st == nil {
					if len(args) == 0 {
						// No arguments, e.g, `.help`.
						return h.Print(ctx, h.MainHelp())
					}

					// Check builtins first
					if c, _ := h.BuiltinCommand(args[0]); c != nil {
						return h.Print(ctx, c.Help())
					}

					// Check if a type before handing off to state lookup
					if t := h.GetDef(nil).GetTypeDef(args[0]); t != nil {
						return h.Print(ctx, shellTypeDoc(t))
					}

					// Use the same function lookup as when executing
					// so that `> .help wolfi` documents `> wolfi`.
					st, err = h.StateLookup(ctx, args[0])
					if err != nil {
						return err
					}
					if st.ModDigest != "" {
						// First argument to `.help` is a module reference, so
						// remove it from list of arguments now that it's loaded.
						// The rest of the arguments should be passed on to
						// the constructor.
						args = args[1:]
					}
				}

				def := h.GetDef(st)

				if st.IsEmpty() {
					switch {
					case st.IsStdlib():
						// Document stdlib
						// Example: `.stdlib | .help`
						if len(args) == 0 {
							return h.Print(ctx, h.StdlibHelp())
						}
						// Example: .stdlib | .help <command>`
						c, err := h.StdlibCommand(args[0])
						if err != nil {
							return err
						}
						return h.Print(ctx, c.Help())

					case st.IsDeps():
						// Document dependency
						// Example: `.deps | .help`
						if len(args) == 0 {
							return h.Print(ctx, h.DepsHelp())
						}
						// Example: `.deps | .help <dependency>`
						depSt, depDef, err := h.GetDependency(ctx, args[0])
						if err != nil {
							return err
						}
						return h.Print(ctx, h.ModuleDoc(depSt, depDef))

					case st.IsCore():
						// Document core
						// Example: `.core | .help`
						if len(args) == 0 {
							return h.Print(ctx, h.CoreHelp())
						}
						// Example: `.core | .help <function>`
						fn := def.GetCoreFunction(args[0])
						if fn == nil {
							return fmt.Errorf("core function %q not found", args[0])
						}
						return h.Print(ctx, h.FunctionDoc(def, fn))

					case len(args) == 0:
						if !def.HasModule() {
							return fmt.Errorf("module not loaded.\nUse %q to see what's available", shellStdlibCmdName)
						}
						// Document module
						// Example: `.help [module]`
						return h.Print(ctx, h.ModuleDoc(st, def))
					}
				}

				t, err := st.GetTypeDef(def)
				if err != nil {
					return err
				}

				// Document type
				// Example: `container | .help`
				if len(args) == 0 {
					return h.Print(ctx, shellTypeDoc(t))
				}

				fp := t.AsFunctionProvider()
				if fp == nil {
					return fmt.Errorf("type %q does not provide functions", t.String())
				}

				// Document function from type
				// Example: `container | .help with-exec`
				fn, err := def.GetFunction(fp, args[0])
				if err != nil {
					return err
				}
				return h.Print(ctx, h.FunctionDoc(def, fn))
			},
		},
		&ShellCommand{
			Use: ".echo [-n] [string ...]",
			Description: `Write arguments to the standard output

Writes any specified operands, separated by single blank (' ') characters and followed by a newline ('\n') character, to the standard output. If the -n option is specified, the trailing newline is suppressed.
`,
		},
		&ShellCommand{
			Use: ".wait",
			Description: `Wait for background processes to complete

The return status is 0 if all specified processes exit successfully. 
If any process exits with a nonzero status, wait returns that status. 
`,
		},
		&ShellCommand{
			Use: ".cd [path | url]",
			Description: `Change the current working directory 

Absolute and relative paths are resolved in relation to the same context directory.
Using a git URL changes the context. Only the initial context can target local 
modules in different contexts.

If the target path is in a different module within the same context, it will be
loaded as the default automatically, making its functions available at the top level.

Without arguments, the current working directory is replaced by the initial context.
`,
			GroupID: moduleGroup.ID,
			Args:    MaximumArgs(1),
			State:   NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				var path string
				if len(args) > 0 {
					path = args[0]
				}
				return h.ChangeDir(ctx, path)
			},
		},
		&ShellCommand{
			Use:         ".pwd",
			Description: "Print the current working directory's absolute path",
			GroupID:     moduleGroup.ID,
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, _ []string, _ *ShellState) error {
				if h.debug {
					shellDebug(ctx, "Workdir", h.Workdir())
				}
				return h.Print(ctx, h.Pwd())
			},
		},
		&ShellCommand{
			Use:         ".ls [path]",
			Description: "List files in the current working directory",
			GroupID:     moduleGroup.ID,
			Args:        MaximumArgs(1),
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				var path string
				if len(args) > 0 {
					path = args[0]
				}
				dir, err := h.Directory(path)
				if err != nil {
					return err
				}
				contents, err := dir.Entries(ctx)
				if err != nil {
					return err
				}
				return h.Print(ctx, strings.Join(contents, "\n"))
			},
		},
		&ShellCommand{
			Use:         ".types",
			Description: "List all types available in the current context",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, _ []string, _ *ShellState) error {
				return h.Print(ctx, h.TypesHelp())
			},
		},
		&ShellCommand{
			Use:         shellDepsCmdName,
			Description: "Dependencies from the module loaded in the current context",
			GroupID:     moduleGroup.ID,
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, _ []string, _ *ShellState) error {
				_, err := h.GetModuleDef(nil)
				if err != nil {
					return err
				}
				return h.Save(ctx, h.NewDepsState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer:   ctx.Completer,
					CmdFunction: shellDepsCmdName,
				}
			},
		},
		&ShellCommand{
			Use:         shellStdlibCmdName,
			Description: "Standard library functions",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, _ []string, _ *ShellState) error {
				return h.Save(ctx, h.NewStdlibState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer:   ctx.Completer,
					CmdFunction: shellStdlibCmdName,
				}
			},
		},
		&ShellCommand{
			Use:         ".core [function]",
			Description: "Load any core Dagger type",
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				return h.Save(ctx, h.NewCoreState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer:   ctx.Completer,
					CmdFunction: shellCoreCmdName,
				}
			},
		},
		cobraToShellCommand(loginCmd),
		cobraToShellCommand(logoutCmd),
		cobraToShellCommand(moduleInstallCmd),
		cobraToShellCommand(moduleUnInstallCmd),
		cobraToShellCommand(moduleUpdateCmd),
	)

	def := h.GetDef(nil)

	for _, fn := range def.GetCoreFunctions() {
		// TODO: Don't hardcode this list.
		promoted := []string{
			"cache-volume",
			"container",
			"directory",
			"engine",
			"git",
			"host",
			"http",
			"set-secret",
			"version",
		}

		if !slices.Contains(promoted, fn.CmdName()) {
			continue
		}

		stdlib = append(stdlib,
			&ShellCommand{
				Use:         h.FunctionUseLine(def, fn),
				Description: fn.Description,
				State:       NoState,
				HelpFunc: func(cmd *ShellCommand) string {
					return h.FunctionDoc(def, fn)
				},
				Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
					emptySt := h.NewState()
					st, err := h.functionCall(ctx, &emptySt, fn.CmdName(), args)
					if err != nil {
						return err
					}

					if h.debug {
						shellDebug(ctx, "Stdout (stdlib)", fn.CmdName(), args, st)
					}

					return h.Save(ctx, *st)
				},
				Complete: func(ctx *CompletionContext, args []string) *CompletionContext {
					return &CompletionContext{
						Completer:   ctx.Completer,
						ModFunction: fn,
					}
				},
			},
		)
	}

	sort.Slice(builtins, func(i, j int) bool {
		return builtins[i].Use < builtins[j].Use
	})

	sort.Slice(stdlib, func(i, j int) bool {
		return stdlib[i].Use < stdlib[j].Use
	})

	h.builtins = builtins
	h.stdlib = stdlib
}

func cobraToShellCommand(c *cobra.Command) *ShellCommand {
	return &ShellCommand{
		Use:         "." + c.Use,
		Description: c.Short,
		GroupID:     c.GroupID,
		State:       NoState,
		Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
			// Re-execute the dagger command (hack)
			args = append([]string{c.Name()}, args...)
			hctx := interp.HandlerCtx(ctx)
			c := exec.CommandContext(ctx, "dagger", args...)
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			c.Stdout = io.MultiWriter(hctx.Stdout, stdio.Stdout)
			c.Stderr = io.MultiWriter(hctx.Stderr, stdio.Stderr)
			c.Stdin = hctx.Stdin
			return c.Run()
		},
	}
}

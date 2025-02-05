package main

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"

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
			return fmt.Errorf("requires at least %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func MaximumArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) > n {
			return fmt.Errorf("accepts at most %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func ExactArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("missing %d positional argument(s)", n-len(args))
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
			return fmt.Errorf("command %q must be piped\nusage: %s", c.Name(), c.Use)
		}
	case NoState:
		if st != nil {
			return fmt.Errorf("command %q cannot be piped\nusage: %s", c.Name(), c.Use)
		}
	}
	if c.Args != nil {
		if err := c.Args(args); err != nil {
			return fmt.Errorf("command %q %w\nusage: %s", c.Name(), err, c.Use)
		}
	}
	// Resolve state values in arguments
	a := make([]string, 0, len(args))
	for i, arg := range args {
		if strings.HasPrefix(arg, shellStatePrefix) {
			w := strings.NewReader(arg)
			v, _, err := h.Result(ctx, w, nil)
			if err != nil {
				return fmt.Errorf("cannot expand command argument at %d", i)
			}
			if v == nil {
				return fmt.Errorf("unexpected nil value while expanding argument at %d", i)
			}
			arg = fmt.Sprintf("%v", v)
		}
		a = append(a, arg)
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
			Run: func(_ context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				// Toggles debug mode, which can be useful when in interactive mode
				h.debug = !h.debug
				return nil
			},
		},
		&ShellCommand{
			Use:         ".help [command]",
			Description: "Print this help message",
			Args:        MaximumArgs(1),
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				if len(args) == 1 {
					c, err := h.BuiltinCommand(args[0])
					if err != nil {
						return err
					}
					if c == nil {
						err = fmt.Errorf("command not found: %q", args[0])
						if !strings.HasPrefix(args[0], ".") {
							if builtin, _ := h.BuiltinCommand("." + args[0]); builtin != nil {
								err = fmt.Errorf("%w, did you mean %q?", err, "."+args[0])
							}
						}
						return err
					}
					return h.Print(ctx, c.Help())
				}

				var doc ShellDoc

				for _, group := range shellGroups {
					cmds := h.GroupBuiltins(group.ID)
					if len(cmds) == 0 {
						continue
					}
					doc.Add(
						group.Title,
						nameShortWrapped(cmds, func(c *ShellCommand) (string, string) {
							return c.Name(), c.Short()
						}),
					)
				}

				doc.Add("", `Use ".help <command>" for more information.`)

				return h.Print(ctx, doc)
			},
		},
		&ShellCommand{
			Use: ".doc [module]\n<function> | .doc [function]",
			Description: `Show documentation for a module, a type, or a function


Local module paths are resolved relative to the workdir on the host, not relative
to the currently loaded module.
`,
			Args: MaximumArgs(1),
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, st *ShellState) error {
				var err error

				// First command in chain
				if st == nil {
					if len(args) == 0 {
						// No arguments, e.g, `.doc`.
						st = h.newState()
					} else {
						// Use the same function lookup as when executing so
						// that `> .doc wolfi` documents `> wolfi`.
						st, err = h.stateLookup(ctx, args[0])
						if err != nil {
							return err
						}
						if st.ModRef != "" {
							// First argument to `.doc` is a module reference, so
							// remove it from list of arguments now that it's loaded.
							// The rest of the arguments should be passed on to
							// the constructor.
							args = args[1:]
						}
					}
				}

				def := h.modDef(st)

				if st.IsEmpty() {
					switch {
					case st.IsStdlib():
						// Document stdlib
						// Example: `.stdlib | .doc`
						if len(args) == 0 {
							return h.Print(ctx, h.StdlibHelp())
						}
						// Example: .stdlib | .doc <command>`
						c, err := h.StdlibCommand(args[0])
						if err != nil {
							return err
						}
						return h.Print(ctx, c.Help())

					case st.IsDeps():
						// Document dependency
						// Example: `.deps | .doc`
						if len(args) == 0 {
							return h.Print(ctx, h.DepsHelp())
						}
						// Example: `.deps | .doc <dependency>`
						depSt, depDef, err := h.GetDependency(ctx, args[0])
						if err != nil {
							return err
						}
						return h.Print(ctx, shellModuleDoc(depSt, depDef))

					case st.IsCore():
						// Document core
						// Example: `.core | .doc`
						if len(args) == 0 {
							return h.Print(ctx, h.CoreHelp())
						}
						// Example: `.core | .doc <function>`
						fn := def.GetCoreFunction(args[0])
						if fn == nil {
							return fmt.Errorf("core function %q not found", args[0])
						}
						return h.Print(ctx, shellFunctionDoc(def, fn))

					case len(args) == 0:
						if !def.HasModule() {
							return fmt.Errorf("module not loaded.\nUse %q to see what's available", shellStdlibCmdName)
						}
						// Document module
						// Example: `.doc [module]`
						return h.Print(ctx, shellModuleDoc(st, def))
					}
				}

				t, err := st.GetTypeDef(def)
				if err != nil {
					return err
				}

				// Document type
				// Example: `container | .doc`
				if len(args) == 0 {
					return h.Print(ctx, shellTypeDoc(t))
				}

				fp := t.AsFunctionProvider()
				if fp == nil {
					return fmt.Errorf("type %q does not provide functions", t.String())
				}

				// Document function from type
				// Example: `container | .doc with-exec`
				fn, err := def.GetFunction(fp, args[0])
				if err != nil {
					return err
				}
				return h.Print(ctx, shellFunctionDoc(def, fn))
			},
		},
		&ShellCommand{
			Use: ".use <module>",
			Description: `Set a module as the default for the session

Local module paths are resolved relative to the workdir on the host, not relative
to the currently loaded module.
`,
			GroupID: moduleGroup.ID,
			Args:    ExactArgs(1),
			State:   NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				st, err := h.getOrInitDefState(args[0], func() (*moduleDef, error) {
					return initializeModule(ctx, h.dag, args[0], true)
				})
				if err != nil {
					return err
				}

				h.mu.Lock()
				if st.ModRef != h.modRef {
					h.modRef = st.ModRef
				}
				h.mu.Unlock()

				return nil
			},
		},
		&ShellCommand{
			Use:         ".refresh",
			Description: `Refresh the schema and reload all module functions`,
			GroupID:     moduleGroup.ID,
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, st *ShellState) error {
				// Get current module definition
				def := h.modDef(st)

				// Re-initialize the module to get fresh schema
				var newDef *moduleDef
				var err error
				if def.ModRef == "" {
					newDef, err = initializeCore(ctx, h.dag)
				} else {
					newDef, err = initializeModule(ctx, h.dag, def.ModRef, true)
				}
				if err != nil {
					return fmt.Errorf("failed to reinitialize module: %w", err)
				}

				// Update handler state with new definition
				h.mu.Lock()
				h.modDefs.Store(def.ModRef, newDef)
				h.mu.Unlock()

				// Reload type definitions
				if err := newDef.loadTypeDefs(ctx, h.dag); err != nil {
					return fmt.Errorf("failed to reload type definitions: %w", err)
				}

				return nil
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
				return h.newDepsState().Write(ctx)
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
				return h.newStdlibState().Write(ctx)
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
				return h.newCoreState().Write(ctx)
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

	def := h.modDef(nil)

	for _, fn := range def.GetCoreFunctions() {
		// TODO: Don't hardcode this list.
		promoted := []string{
			"llm",
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
				Use:         shellFunctionUseLine(def, fn),
				Description: fn.Description,
				State:       NoState,
				HelpFunc: func(cmd *ShellCommand) string {
					return shellFunctionDoc(def, fn)
				},
				Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
					st := h.newState()
					st, err := h.functionCall(ctx, st, fn.CmdName(), args)
					if err != nil {
						return err
					}

					if h.debug {
						shellDebug(ctx, "Stdout (stdlib)", fn.CmdName(), args, st)
					}

					return st.Write(ctx)
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
			c.Stdout = hctx.Stdout
			c.Stderr = hctx.Stderr
			c.Stdin = hctx.Stdin
			return c.Run()
		},
	}
}

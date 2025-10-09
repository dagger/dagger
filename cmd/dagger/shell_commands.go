package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
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

	// NoResolveStateArgs indicates that the command should not resolve state
	// values in arguments, before passing them to Run.
	NoResolveStateArgs bool
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

// Short is the summary for the command
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
	if !c.NoResolveStateArgs {
		// resolve state values in arguments
		a, err := h.resolveResults(ctx, args)
		if err != nil {
			return err
		}
		args = a
	}
	if h.Debug() {
		shellDebug(ctx, "Command: "+c.Name(), args, st)
	}
	return c.Run(ctx, c, args, st)
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

func (h *shellCallHandler) llmBuiltins() []*ShellCommand {
	return []*ShellCommand{
		{
			Use:         ".shell",
			Description: "Switch into shell mode",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				h.mode = modeShell
				return nil
			},
		},
		{
			Use:         ".prompt",
			Description: "Switch into prompt mode",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				// Initialize LLM if not already done
				_, err := h.llm(ctx)
				if err != nil {
					return err
				}
				h.mode = modePrompt
				return nil
			},
		},
		{
			Use:         ".clear",
			Description: "Clear the LLM history",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				if h.llmSession == nil {
					return fmt.Errorf("LLM not initialized")
				}
				h.llmSession = h.llmSession.Clear()
				return nil
			},
		},
		{
			Use:         ".compact",
			Description: "Compact the LLM history",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				if h.llmSession == nil {
					return fmt.Errorf("LLM not initialized")
				}
				newLLM, err := h.llmSession.Compact(ctx)
				if err != nil {
					return err
				}
				h.llmSession = newLLM
				return nil
			},
		},
		{
			Use:         ".history",
			Description: "Show the LLM history",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				if h.llmSession == nil {
					return fmt.Errorf("LLM not initialized")
				}
				_, err := h.llmSession.History(ctx)
				return err
			},
		},
		{
			Use:         ".model [model]",
			Description: "Swap out the LLM model",
			GroupID:     "llm",
			Args:        ExactArgs(1),
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, args []string, _ *ShellState) error {
				llm, err := h.llm(ctx)
				if err != nil {
					return err
				}
				newLLM, err := llm.Model(args[0])
				if err != nil {
					return err
				}
				h.llmSession = newLLM
				h.llmModel = newLLM.model
				return nil
			},
		},
	}
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
				// while developing or troubleshooting what happens in each step
				h.mu.Lock()
				h.debug = !h.debug
				h.mu.Unlock()
				return nil
			},
		},
		&ShellCommand{
			Use:         ".help [command | function | module | type]\n<function> | .help [function]",
			Description: `Show documentation for a command, function, module, or type`,
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
						_, depDef, err := h.GetDependency(ctx, args[0])
						if err != nil {
							return err
						}
						return h.Print(ctx, h.ModuleDoc(depDef))

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
						return h.Print(ctx, h.ModuleDoc(def))
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

Writes any specified operands, separated by single blank (' ') characters and
followed by a newline ('\n') character, to the standard output. If the -n option
is specified, the trailing newline is suppressed.
`,
		},
		&ShellCommand{
			Use:  ".printenv [name]",
			Args: MaximumArgs(1),
			Description: `Show available environment variables or a specific variable

If no name is provided, all environment variables are printed. If a name is provided, the value of that environment variable is printed.
			`,
			State: NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				hc := interp.HandlerCtx(ctx)

				if len(args) == 0 {
					// Print all environment variables
					for name, vr := range hc.Env.Each {
						fmt.Fprintf(hc.Stdout, "%s=%s\n", name, vr)
					}

					return nil
				}

				// Print a specific environment variable
				name := args[0]

				v := hc.Env.Get(name)
				if !v.IsSet() {
					return fmt.Errorf("environment variable %q not set", name)
				}

				return h.Print(ctx, v.String())
			},
		},
		&ShellCommand{
			Use: ".wait [id...]",
			Description: `Wait for background processes to complete

'id' is the process or job ID. If no ID is specified, .wait always returns 0 (zero).
Otherwise, it returns the exit status of the first command that failed. When multiple
processes are given, the command waits for all processes to complete.

Example:

  container | from alpine | with-exec false | stdout &
  job1=$!
  .echo "job id: $job1"
  .wait $job1
`,
			State: NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
				hc := interp.HandlerCtx(ctx)

				if len(args) == 0 {
					return hc.Builtin(ctx, []string{"wait"})
				}

				for _, job := range args {
					err := hc.Builtin(ctx, []string{"wait", job})
					if err != nil {
						return err
					}
				}

				return nil
			},
		},
		&ShellCommand{
			Use: ".exit [code]",
			Description: `Exit the shell with an optional status code

Without arguments, uses the exit status of the last command that executed.
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
			Hidden:  true,
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
			Hidden:      true,
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, _ []string, _ *ShellState) error {
				if h.Debug() {
					shellDebug(ctx, "Workdir", h.Workdir())
				}
				return h.Print(ctx, h.Pwd())
			},
		},
		&ShellCommand{
			Use:         ".ls [path]",
			Description: "List files in the current working directory",
			GroupID:     moduleGroup.ID,
			Hidden:      true,
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
			Use:         ".refresh",
			Description: `Refresh the schema and reload all module functions`,
			GroupID:     moduleGroup.ID,
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, cmd *ShellCommand, args []string, st *ShellState) error {
				// Get current module definition
				def := h.GetDef(st)

				// Re-initialize the module to get fresh schema
				var newDef *moduleDef
				var err error
				if def.Source == nil {
					newDef, err = initializeCore(ctx, h.dag)
				} else {
					newDef, err = initializeModule(ctx, h.dag, def.SourceRoot, def.Source)
				}
				if err != nil {
					return fmt.Errorf("failed to reinitialize module: %w", err)
				}

				// Update handler state with new definition
				h.modDefs.Store(def.SourceDigest, newDef)

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
			Hidden:      true,
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
			Hidden:      true,
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
			Hidden:      true,
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

	// Add LLM commands
	builtins = append(builtins, h.llmBuiltins()...)

	def := h.GetDef(nil)

	for _, fn := range def.GetCoreFunctions() {
		def.LoadFunctionTypeDefs(fn)

		// TODO: Don't hardcode this list.
		promoted := []string{
			"address",
			"llm",
			"cache-volume",
			"container",
			"checks",
			"directory",
			"engine",
			"file",
			"git",
			"host",
			"json",
			"env",
			"http",
			"set-secret",
			"secret",
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
				// Don't resolve state args since this is calling functionCall
				// which needs state args to be passed as-is for specialized
				// flag handling.
				NoResolveStateArgs: true,
				Run: func(ctx context.Context, cmd *ShellCommand, args []string, _ *ShellState) error {
					emptySt := h.NewState()
					st, err := h.functionCall(ctx, &emptySt, fn.CmdName(), args)
					if err != nil {
						return err
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

	slices.SortStableFunc(builtins, func(x, y *ShellCommand) int {
		return cmp.Compare(x.Use, y.Use)
	})

	slices.SortStableFunc(stdlib, func(x, y *ShellCommand) int {
		return cmp.Compare(x.Use, y.Use)
	})

	h.builtins = builtins
	h.stdlib = stdlib
}

func cobraToShellCommand(c *cobra.Command) *ShellCommand {
	return &ShellCommand{
		Use:         "." + c.Use,
		Description: c.Short,
		GroupID:     c.GroupID,
		Hidden:      true,
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

package daggercmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"dagger.io/dagger/core"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/interp"
)

const shellModuleGroupID = "module"

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

func (h *shellCallHandler) Builtins() []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.builtins))
	for _, c := range h.builtins {
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
				return h.clearModelInteractive(ctx)
			},
		},
		{
			Use:         ".compact",
			Description: "Compact the LLM history",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				target, err := h.commandTarget(ctx)
				if err != nil {
					return err
				}
				return target.CompactInPlace(ctx)
			},
		},
		{
			Use:         ".history",
			Description: "Show the LLM history",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				target, err := h.commandTarget(ctx)
				if err != nil {
					return err
				}
				return target.History(ctx)
			},
		},
		{
			Use:         ".model [model]",
			Description: "Swap out the LLM model (interactive picker if no model given)",
			GroupID:     "llm",
			Args:        MaximumArgs(1),
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, args []string, _ *ShellState) error {
				if len(args) == 0 {
					return h.selectModelInteractive(ctx)
				}
				target, err := h.commandTarget(ctx)
				if err != nil {
					return err
				}
				if err := target.Model(ctx, args[0]); err != nil {
					return err
				}
				h.noteCommandModel(target)
				return nil
			},
		},
		{
			Use:         ".effort [level]",
			Description: "Set the LLM reasoning effort (interactive picker if no level given)",
			GroupID:     "llm",
			Args:        MaximumArgs(1),
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, args []string, _ *ShellState) error {
				if len(args) == 0 {
					return h.selectEffortInteractive(ctx)
				}
				target, err := h.commandTarget(ctx)
				if err != nil {
					return err
				}
				return target.Effort(ctx, args[0])
			},
		},
		{
			Use:         ".context",
			Description: "Open the context visualizer: a web UI showing what occupies the context window",
			GroupID:     "llm",
			Args:        NoArgs,
			State:       NoState,
			Run: func(ctx context.Context, _ *ShellCommand, _ []string, _ *ShellState) error {
				llm, err := h.llm(ctx)
				if err != nil {
					return err
				}
				url, err := llm.ContextVizURL()
				if err != nil {
					return err
				}
				llm.ShowContextViz()
				return h.Print(ctx, url)
			},
		},
	}
}

// selectModelInteractive presents the configured providers' models, marking the
// current route. Selection changes this conversation; saving a default for new
// conversations requires a separate opt-in in the same form.
func (h *shellCallHandler) selectModelInteractive(ctx context.Context) error {
	target, err := h.commandTarget(ctx)
	if err != nil {
		return err
	}

	// The old route may no longer resolve after logout (notably local routes).
	// Keep the picker usable to repair it; cached identity is only a display and
	// preselection hint, never a prerequisite for choosing another provider.
	llm, current := target.llmSnapshot()
	if llm != nil {
		if model, err := llm.Model(ctx); err == nil {
			if provider, err := llm.Provider(ctx); err == nil {
				current = modelSelection{Provider: provider, Model: model}
			}
		}
	}
	options, err := availableModelOptions(ctx, current)
	if err != nil {
		return err
	}
	if len(options) == 0 {
		return fmt.Errorf("no models available; run /setup to configure a provider")
	}

	var selected modelSelection
	var useDefault bool
	form := newModelPickerForm(options, current, &selected, &useDefault)

	if err := Frontend.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}

	if form.State == huh.StateAborted || selected.Model == "" {
		return nil // user aborted
	}

	if err := target.Model(ctx, selected.Model, core.LLMWithModelOpts{Provider: selected.Provider}); err != nil {
		return err
	}
	h.noteCommandModel(target)
	// Persist only after the boundary edit succeeds: canceling a pending
	// command must not change the default for future conversations.
	if useDefault {
		notice, err := h.saveModelDefault(ctx, selected)
		if err != nil {
			return err
		}
		return h.Print(ctx, notice)
	}
	return nil
}

func (h *shellCallHandler) noteCommandModel(target *sessionAgent) {
	if target.isTarget() {
		_, model := target.llmSnapshot()
		h.noteModel(model.Model)
	}
}

func (h *shellCallHandler) clearModelInteractive(ctx context.Context) error {
	target, err := h.commandTarget(ctx)
	if err != nil {
		return err
	}
	var confirmed bool
	form := idtui.NewForm(huh.NewGroup(
		idtui.NewExplicitConfirm("Clear", "Cancel", &confirmed).
			Title(fmt.Sprintf("Clear %s's conversation?", target.Name())).
			Description("Interrupt its current work and discard its history and references. The agent will wait for your next prompt."),
	))
	if err := Frontend.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}
	if form.State == huh.StateAborted || !confirmed {
		return nil
	}
	return target.Clear(ctx)
}

func newModelPickerForm(options []huh.Option[modelSelection], current modelSelection, selected *modelSelection, useDefault *bool) *huh.Form {
	current = current.configRoute()
	description := "Current model unavailable"
	if current.Model != "" && current.Provider != "" {
		description = fmt.Sprintf("Current: %s (%s)", current.Model, current.Provider)
	}
	options = slices.Clone(options)
	for i := range options {
		if options[i].Value == current {
			options[i].Key += " (current)"
			*selected = current
		}
	}
	*useDefault = false
	return idtui.NewForm(huh.NewGroup(
		huh.NewSelect[modelSelection]().
			Title("Choose a model").
			Description(description).
			Height(min(len(options)+2, 12)).
			Filtering(true).
			Options(options...).
			Value(selected),
		idtui.NewExplicitConfirm("Yes", "No", useDefault).
			Title("Use as default for new conversations").
			Description("Environment and .env model settings take priority over saved defaults."),
	))
}

// saveModelDefault changes only the saved default route. In particular, choosing
// an environment- or parent-configured provider must not copy credentials into
// the file or re-enable a disabled saved provider.
func (h *shellCallHandler) saveModelDefault(ctx context.Context, selected modelSelection) (string, error) {
	selected = selected.configRoute()
	cfg, err := llmconfig.Load()
	if err != nil {
		return "", err
	}
	if cfg == nil {
		cfg = &llmconfig.Config{}
	}
	cfg.LLM.DefaultProvider = selected.Provider
	cfg.LLM.DefaultModel = selected.Model
	if err := ctx.Err(); err != nil {
		return "", context.Cause(ctx)
	}
	if err := cfg.Save(); err != nil {
		return "", err
	}
	if err := h.reloadLLMConfig(ctx); err != nil {
		return "", err
	}
	notice := fmt.Sprintf("Saved default for new conversations: %s (%s).", selected.Model, selected.Provider)
	effective, err := assembleLLMConfig()
	if err != nil {
		return "", err
	}
	if effective == nil || effective.DefaultProvider != selected.Provider || effective.DefaultModel != selected.Model {
		if effective != nil && effective.DefaultProvider != "" {
			notice += fmt.Sprintf("\nEnvironment or .env model settings currently override this default with %s (%s).", effective.DefaultModel, effective.DefaultProvider)
		} else {
			notice += "\nThis default is not effective while its saved provider is disabled; use /setup to enable it."
		}
		notice += " The selected model still applies to this conversation."
	}
	return notice, nil
}

// selectEffortInteractive presents a picker of the reasoning effort levels the
// current model supports and applies the chosen one. It mirrors the
// ".effort <level>" path once a level is selected, and no-ops if the user
// aborts the form.
func (h *shellCallHandler) selectEffortInteractive(ctx context.Context) error {
	target, err := h.commandTarget(ctx)
	if err != nil {
		return err
	}

	_, model := target.llmSnapshot()
	options := reasoningEffortOptions(model)

	var selected string
	form := idtui.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Reasoning effort").
				Height(min(len(options)+2, 12)).
				Options(options...).
				Value(&selected),
		),
	)

	if err := Frontend.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}

	if selected == "" {
		return nil // user aborted
	}

	return target.Effort(ctx, selected)
}

// reasoningEffortOptions builds the option list for the interactive ".effort"
// picker from the current provider's catwalk model metadata: the levels the model
// actually supports, plus "none" to disable reasoning. When the model isn't in
// the catalog (e.g. a local or custom endpoint), or is a reasoning-capable
// model without explicit levels, it falls back to the conventional
// low/medium/high levels. Known models that can't reason at all get only
// "none". Only look across providers when the current provider is unknown.
func reasoningEffortOptions(model modelSelection) []huh.Option[string] {
	var metadata llmconfig.ModelInfo
	var found bool
	if model.Provider != "" {
		metadata, found = llmconfig.ModelByID(model.Provider, model.Model)
	} else {
		for _, e := range llmconfig.ProviderEntries() {
			metadata, found = llmconfig.ModelByID(e.ConfigKey, model.Model)
			if found {
				break
			}
		}
	}

	var levels []string
	var defaultLevel string
	canReason := !found || metadata.CanReason // unknown models get the conventional fallback below
	if found && metadata.CanReason {
		levels = metadata.ReasoningLevels
		defaultLevel = metadata.DefaultReasoningEffort
	}
	if canReason && len(levels) == 0 {
		levels = []string{"low", "medium", "high"}
	}
	if !slices.Contains(levels, "none") {
		levels = append([]string{"none"}, levels...)
	}

	var options []huh.Option[string]
	for _, level := range levels {
		label := level
		if level == defaultLevel {
			label += " (default)"
		}
		options = append(options, huh.NewOption(label, level))
	}
	return options
}

// modelSelection keeps routing identity separate from the model's API ID.
// Providers can share IDs, and IDs containing slashes need not be prefixed.
type modelSelection struct {
	Provider string
	Model    string
}

// Unknown model names use the router's "other" provider, but its endpoint and
// credentials come from the OpenAI configuration slot. Keep picker values and
// saved defaults in the configuration's namespace rather than persisting "other".
func (selection modelSelection) configRoute() modelSelection {
	if selection.Provider == "other" {
		selection.Provider = "openai"
	}
	return selection
}

// availableModelOptions asks the connected engine for effective routing config,
// including providers inherited from a parent client in nested sessions. Without
// a connection, assemble the local file, environment and .env configuration.
func availableModelOptions(ctx context.Context, current modelSelection) ([]huh.Option[modelSelection], error) {
	var cfg *engine.LLMConfig
	var err error
	if read, ok := ctx.Value(llmModelConfigKey{}).(llmModelConfigReader); ok {
		cfg, err = read(ctx)
	} else {
		cfg, err = assembleLLMConfig()
	}
	if err != nil {
		return nil, err
	}
	return modelOptions(cfg, current), nil
}

// modelOptions deduplicates within each provider, never across providers: the
// same model can use different credentials/routes.
func modelOptions(cfg *engine.LLMConfig, current modelSelection) []huh.Option[modelSelection] {
	current = current.configRoute()
	if cfg == nil {
		return nil
	}

	var providers []string
	for name := range cfg.Providers {
		providers = append(providers, name)
	}
	slices.Sort(providers)

	var options []huh.Option[modelSelection]
	seen := make(map[modelSelection]bool)
	add := func(provider, id, label string) {
		selection := modelSelection{Provider: provider, Model: id}
		if id == "" || seen[selection] {
			return
		}
		seen[selection] = true
		options = append(options, huh.NewOption(fmt.Sprintf("%s (%s)", label, provider), selection))
	}

	for _, provider := range providers {
		// Keep a manually selected custom model visible, but never introduce an
		// unconfigured provider just because an old conversation used it.
		if provider == current.Provider {
			add(provider, current.Model, current.Model)
		}
		// Configured custom models may not occur in the embedded catalog.
		// The default model can be distinct from the provider's pinned model.
		if provider == cfg.DefaultProvider {
			add(provider, cfg.DefaultModel, cfg.DefaultModel)
		}
		p := cfg.Providers[provider]
		add(provider, p.Model, p.Model)
		add(provider, p.SmallModel, p.SmallModel)
		for _, m := range llmconfig.ModelsForProvider(provider) {
			label := m.Label
			if label == "" {
				label = m.ID
			}
			add(provider, m.ID, label)
		}
	}
	return options
}

func (h *shellCallHandler) registerCommands() error { //nolint:gocyclo
	var builtins []*ShellCommand
	// Keep Cloud sign-out distinct from the agent's provider logout picker.
	cloudLogout := cobraToShellCommand(logoutCmd)
	cloudLogout.Use = ".cloud-logout"

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
				if h.commandMode(ctx) == modePrompt && st == nil {
					help, err := h.promptHelp(args)
					if err != nil {
						return err
					}
					return h.Print(ctx, help)
				}
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

					// "." and the current module name refer to this module.
					if def := h.GetDef(nil); def.HasModule() && (args[0] == "." || args[0] == def.Name) {
						return h.Print(ctx, h.ModuleDoc(def))
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

				if st.IsEmpty() && len(args) == 0 {
					if !def.HasModule() {
						return fmt.Errorf("module not loaded")
					}
					// Document module
					// Example: `.help [module]`
					return h.Print(ctx, h.ModuleDoc(def))
				}

				t, err := st.GetTypeDef(def)
				if err != nil {
					return err
				}

				// Document type
				// Example: `container | .help`
				if len(args) == 0 {
					// When main proxying is active, the constructor
					// returns Query. Show the module's named main object
					// type instead so `. | .help` documents the module
					// object, not Query.
					if t.AsFunctionProvider() != nil && t.AsFunctionProvider().ProviderName() == "Query" && def.HasModule() {
						if mt := def.GetObject(def.Name); mt != nil && mt.typeDef != nil {
							t = mt.typeDef
						}
					}
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
			GroupID: shellModuleGroupID,
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
			GroupID:     shellModuleGroupID,
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
			GroupID:     shellModuleGroupID,
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
			GroupID:     shellModuleGroupID,
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
		cobraToShellCommand(loginCmd),
		cloudLogout,
		cobraToShellCommand(moduleDepInstallCmd),
		cobraToShellCommand(moduleUpdateCmd),
	)

	// Add LLM commands
	builtins = append(builtins, h.llmBuiltins()...)
	builtins = append(builtins, h.llmConfigBuiltins()...)
	slices.SortStableFunc(builtins, func(x, y *ShellCommand) int {
		return cmp.Compare(x.Use, y.Use)
	})

	h.builtins = builtins
	return nil
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

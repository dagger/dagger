package main

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
)

const (
	helpIndent = uint(2)
)

func (h *shellCallHandler) FunctionsList(name string, fns []*modFunction) string {
	if len(fns) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available functions:\n")
	for _, f := range fns {
		sb.WriteString("  - ")
		sb.WriteString(f.CmdName())
		sb.WriteString("\n")
	}

	usage := name + " | .help"

	fmt.Fprintf(sb, "\nUse %q for more details.", usage)
	fmt.Fprintf(sb, "\nUse %q for more information on a function.\n", usage+" <function>")

	return sb.String()
}

func (h *shellCallHandler) CommandsList(name string, cmds []*ShellCommand) string {
	if len(cmds) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available commands:\n")
	for _, c := range cmds {
		sb.WriteString("  - ")
		sb.WriteString(c.Name())
		sb.WriteString("\n")
	}

	usage := name + " | .help"

	fmt.Fprintf(sb, "\nUse %q for more details.", usage)
	fmt.Fprintf(sb, "\nUse %q for more information on a command.\n", usage+" <command>")

	return sb.String()
}

func (h *shellCallHandler) DependenciesList() string {
	// This is validated in the .deps command
	def, _ := h.GetModuleDef(nil)
	if def == nil || len(def.Dependencies) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available dependencies:\n")
	for _, dep := range def.Dependencies {
		sb.WriteString("  - ")
		sb.WriteString(dep.Name)
		sb.WriteString("\n")
	}

	usage := shellDepsCmdName + " | .help"

	fmt.Fprintf(sb, "\nUse %q for more details.", usage)
	fmt.Fprintf(sb, "\nUse %q for more information on a dependency.\n", usage+" <dependency>")

	return sb.String()
}

func (h *shellCallHandler) MainHelp() string {
	var doc ShellDoc

	// NB: Order is important to apply proper shadowing. This reflects the
	// lookup order when the handler executes a command.
	groups := slices.Collect(combineUsages(
		h.allBuiltinUsages(),
		h.allFunctionUsages(),
		h.allLoadedModules(),
		h.allDependencyUsages(),
		h.allStdlibUsages(),
	))

	// move builtins to last
	groups = append(groups[1:], groups[0])

	// move module before its functions
	groups[0], groups[1] = groups[1], groups[0]

	for _, group := range groups {
		doc.Add("", group)
	}

	types := "<command>"
	if len(doc.Groups) > 2 {
		types += " | <function>"
	}

	doc.Add("", fmt.Sprintf(`Use ".help %s" for more information.`, types))

	return doc.String()
}

// combineUsages removes shadowed functions, even in different sections or groups
//
// Returns the section's compiled string of usages, in the order they were passed.
func combineUsages(groups ...iter.Seq2[string, string]) iter.Seq[string] {
	return func(yield func(string) bool) {
		seen := make(map[string]struct{})

		// filter out elements that already exist
		filtered := func(seq iter.Seq2[string, string]) iter.Seq2[string, string] {
			return func(y func(string, string) bool) {
				for k, v := range seq {
					if _, exists := seen[k]; !exists {
						seen[k] = struct{}{}
						if !y(k, v) {
							return
						}
					}
				}
			}
		}

		for _, usages := range groups {
			body := nameShortWrappedIter(filtered(usages))
			if !yield(body) {
				return
			}
		}
	}
}

// allFunctionUsages returns a sequence of all top-level module functions, as name and short description
//
// If the constructor has any required arguments this will be empty because
// users won't be able to use the module's functions directly.
func (h *shellCallHandler) allFunctionUsages() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		def, _ := h.GetModuleDef(nil)
		if def == nil {
			return
		}

		constr := def.MainObject.AsObject.Constructor

		// When using a top-level function, we're automatically making a call
		// to the constructor without arguments. If it has required arguments
		// this isn't possible, so don't show them as being immediately available.
		// It'll at least show the constructor itself (next).
		if !constr.HasRequiredArgs() {
			for _, fn := range def.MainObject.AsFunctionProvider().GetFunctions() {
				if !yield(fn.CmdName(), fn.Short()) {
					return
				}
			}
		}
	}
}

// allLoadedModules returns a sequence of a single module if one is currently loaded
//
// The function name can be misleading since we only consider one module to
// be the "current" module, at least at the moment. Making this a sequence
// helps to combine in `combineUsages` function.
func (h *shellCallHandler) allLoadedModules() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		def, _ := h.GetModuleDef(nil)
		if def == nil {
			return
		}

		constr := def.MainObject.AsObject.Constructor

		// The module name is a convenience for clarity. Can always use the path (`.`)
		// No point if there's no arguments though.
		if len(constr.Args) == 0 {
			return
		}

		short := constr.Short()
		if short == "" || short == "-" {
			short = def.Short()
		}

		yield(constr.CmdName(), short)
	}
}

// allDependencyUsages returns a sequence of all dependencies, as name and short description
func (h *shellCallHandler) allDependencyUsages() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		def, _ := h.GetModuleDef(nil)
		if def == nil {
			return
		}
		for _, dep := range def.Dependencies {
			if !yield(dep.Name, dep.Short()) {
				return
			}
		}
	}
}

// allStdlibUsages returns a sequence of all stdlib commands, as name and short description
func (h *shellCallHandler) allStdlibUsages() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, cmd := range h.Stdlib() {
			if !yield(cmd.Name(), cmd.Short()) {
				return
			}
		}
	}
}

// allBuiltinUsages returns a sequence of all builtin commands, as name and short description
func (h *shellCallHandler) allBuiltinUsages() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, cmd := range h.Builtins() {
			if !yield(cmd.Name(), cmd.Short()) {
				return
			}
		}
	}
}

func (h *shellCallHandler) StdlibHelp() string {
	var doc ShellDoc

	doc.Add("Commands", nameShortWrapped(h.Stdlib(), func(c *ShellCommand) (string, string) {
		return c.Name(), c.Description
	}))

	doc.Add("", fmt.Sprintf(`Use "%s | .help <command>" for more information on a command.`, shellStdlibCmdName))

	return doc.String()
}

func (h *shellCallHandler) CoreHelp() string {
	var doc ShellDoc

	def := h.GetDef(nil)

	doc.Add(
		"Available Functions",
		nameShortWrapped(def.GetCoreFunctions(), func(f *modFunction) (string, string) {
			return f.CmdName(), f.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .help <function>" for more information on a function.`, shellCoreCmdName))

	return doc.String()
}

func (h *shellCallHandler) DepsHelp() string {
	// This is validated in the .deps command
	def, _ := h.GetModuleDef(nil)
	if def == nil {
		return ""
	}

	var doc ShellDoc

	doc.Add(
		"Module Dependencies",
		nameShortWrapped(def.Dependencies, func(dep *moduleDef) (string, string) {
			return dep.Name, dep.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .help <dependency>" for more information on a dependency.`, shellDepsCmdName))

	return doc.String()
}

func (h *shellCallHandler) TypesHelp() string {
	var doc ShellDoc

	var core []functionProvider
	var mod []functionProvider

	def := h.GetDef(nil)

	for _, o := range def.AsFunctionProviders() {
		if o.IsCore() {
			core = append(core, o)
		} else {
			mod = append(mod, o)
		}
	}

	doc.Add(
		"Core Types",
		nameShortWrapped(core, func(o functionProvider) (string, string) {
			return o.ProviderName(), o.Short()
		}),
	)

	if len(mod) > 0 && def.HasModule() {
		doc.Add(
			def.Name+" Types",
			nameShortWrapped(mod, func(o functionProvider) (string, string) {
				return o.ProviderName(), o.Short()
			}),
		)
	}

	doc.Add("", `Use ".help <type>" for more information on a type.`)

	return doc.String()
}

type ShellDoc struct {
	Groups []ShellDocSection
}

type ShellDocSection struct {
	Title  string
	Body   string
	Indent uint
}

func (d *ShellDoc) Add(title, body string) {
	if body != "" {
		d.Groups = append(d.Groups, ShellDocSection{Title: title, Body: body})
	}
}

func (d *ShellDoc) AddSection(title, body string) {
	d.Groups = append(d.Groups, ShellDocSection{Title: title, Body: body, Indent: helpIndent})
}

func (d ShellDoc) String() string {
	width := getViewWidth()

	sb := new(strings.Builder)
	for i, grp := range d.Groups {
		body := grp.Body

		if grp.Title != "" {
			sb.WriteString(indent.String(toUpperBold(grp.Title), grp.Indent))
			sb.WriteString("\n")

			// Indent body if there's a title
			var i uint
			if !strings.HasPrefix(body, strings.Repeat(" ", int(helpIndent))) {
				i = helpIndent + grp.Indent
			} else if grp.Indent > 0 && !strings.HasPrefix(body, strings.Repeat(" ", int(helpIndent+grp.Indent))) {
				i = grp.Indent
			}
			if i > 0 {
				wrapped := wordwrap.String(grp.Body, width-int(i))
				body = indent.String(wrapped, i)
			}
		}
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
		// Extra new line between groups
		if i < len(d.Groups)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// FunctionUseLine returns the usage line fine for a function
func (h *shellCallHandler) FunctionUseLine(md *moduleDef, fn *modFunction) string {
	sb := new(strings.Builder)

	if fn == md.MainObject.AsObject.Constructor {
		sb.WriteString(h.modRelPath(md))
	} else {
		sb.WriteString(fn.CmdName())
	}

	for _, arg := range fn.RequiredArgs() {
		sb.WriteString(" <")
		sb.WriteString(arg.FlagName())
		sb.WriteString(">")
	}

	if len(fn.OptionalArgs()) > 0 {
		sb.WriteString(" [options]")
	}

	return sb.String()
}

func (h *shellCallHandler) FunctionFullUseLine(md *moduleDef, fn *modFunction) string {
	usage := h.FunctionUseLine(md, fn)
	opts := fn.OptionalArgs()

	if len(opts) > 0 {
		sb := new(strings.Builder)

		for _, arg := range opts {
			sb.WriteString(" [--")
			sb.WriteString(arg.flagName)

			t := arg.TypeDef.String()
			if t != "bool" {
				sb.WriteString(" ")
				sb.WriteString(t)
			}

			sb.WriteString("]")
		}

		return strings.ReplaceAll(usage, " [options]", sb.String())
	}

	return usage
}

func (h *shellCallHandler) ModuleDoc(m *moduleDef) string {
	var doc ShellDoc

	meta := new(strings.Builder)
	meta.WriteString(m.Name)

	description := m.Description
	if description == "" {
		description = m.MainObject.AsObject.Description
	}
	if description != "" {
		meta.WriteString("\n\n")
		meta.WriteString(description)
	}
	if meta.Len() > 0 {
		doc.Add("Module", meta.String())
	}

	fn := m.MainObject.AsObject.Constructor
	usage := h.FunctionUseLine(m, fn)

	if len(fn.Args) > 0 {
		constructor := new(strings.Builder)
		constructor.WriteString("Usage: ")
		constructor.WriteString(usage)

		if fn.Description != "" {
			constructor.WriteString("\n\n")
			constructor.WriteString(fn.Description)
		}

		doc.Add("Entrypoint", constructor.String())

		if args := fn.RequiredArgs(); len(args) > 0 {
			doc.AddSection(
				"Required Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return strings.TrimPrefix(a.Usage(), "--"), a.Long()
				}),
			)
		}
		if args := fn.OptionalArgs(); len(args) > 0 {
			doc.AddSection(
				"Optional Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return a.Usage(), a.Long()
				}),
			)
		}
	}

	if fns := m.MainObject.AsFunctionProvider().GetFunctions(); len(fns) > 0 {
		doc.Add(
			"Available Functions",
			nameShortWrapped(fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			}),
		)
		doc.Add("", fmt.Sprintf(`Use "%s | .help <function>" for more information on a function.`,
			strings.TrimSuffix(usage, " [options]"),
		))
	}

	return doc.String()
}

func (h *shellCallHandler) FunctionDoc(md *moduleDef, fn *modFunction) string {
	var doc ShellDoc

	if fn.Description != "" {
		doc.Add("", fn.Description)
	}

	usage := h.FunctionUseLine(md, fn)
	if usage != "" {
		doc.Add("Usage", usage)
	}

	if args := fn.RequiredArgs(); len(args) > 0 {
		doc.Add(
			"Required Arguments",
			nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
				return strings.TrimPrefix(a.Usage(), "--"), a.Long()
			}),
		)
	}

	if args := fn.OptionalArgs(); len(args) > 0 {
		doc.Add(
			"Optional Arguments",
			nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
				return a.Usage(), a.Long()
			}),
		)
	}

	usage = strings.TrimSuffix(usage, " [options]")

	doc.Add(
		"Returns",
		fmt.Sprintf(`%s

Use "%s | .help" for more details.`,
			fn.ReturnType.Short(),
			strings.TrimSuffix(usage, " [options]"),
		))

	return doc.String()
}

func shellTypeDoc(t *modTypeDef) string {
	var doc ShellDoc

	fp := t.AsFunctionProvider()
	if fp == nil {
		doc.Add(t.KindDisplay(), t.Long())

		// If not an object, only have the type to show.
		return doc.String()
	}

	if fp.ProviderName() != "Query" {
		doc.Add(t.KindDisplay(), t.Long())
	}

	if fns := fp.GetFunctions(); len(fns) > 0 {
		doc.Add(
			"Available Functions",
			nameShortWrapped(fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			}),
		)
		doc.Add("", `Use ".help <function>" for more information on a function.`)
	}

	return doc.String()
}

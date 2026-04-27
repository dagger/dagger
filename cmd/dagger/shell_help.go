package main

import (
	"fmt"
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

func (h *shellCallHandler) MainHelp() string {
	var doc ShellDoc

	def := h.GetDef(nil)

	// Group functions by source module.
	type moduleGroup struct {
		name  string // module name ("" for core)
		short string // module description
		fns   []*modFunction
	}

	// Collect functions into groups, preserving encounter order.
	groups := make(map[string]*moduleGroup)
	var order []string

	constr := def.MainObject.AsObject.Constructor
	if !constr.HasRequiredArgs() {
		for _, fn := range def.MainObject.AsFunctionProvider().GetFunctions() {
			if isHiddenFunction(fn.CmdName()) {
				continue
			}
			src := fn.SourceModuleName
			g, ok := groups[src]
			if !ok {
				g = &moduleGroup{name: src}
				groups[src] = g
				order = append(order, src)
			}
			g.fns = append(g.fns, fn)
		}
	}

	// Fill in module descriptions from dependencies.
	for _, g := range groups {
		if g.name == "" {
			continue
		}
		if dep := def.GetDependency(g.name); dep != nil {
			g.short = dep.Short()
		} else if g.name == def.Name {
			g.short = def.Short()
		}
	}

	// Emit groups: core first, then entrypoint modules, then dependencies.
	if g, ok := groups[""]; ok {
		doc.Add("Available Functions",
			nameShortWrapped(g.fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			}),
		)
	}

	// Entrypoint module functions (source module == current module name).
	if def.HasModule() {
		if g, ok := groups[def.Name]; ok {
			title := def.Name
			body := nameShortWrapped(g.fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			})
			if g.short != "" && g.short != "-" {
				body = g.short + "\n" + body
			}
			doc.Add(title,
				body,
			)
		}
	}

	// Installed (non-entrypoint) modules in a single group.
	var installed []*modFunction
	for _, src := range order {
		if src == "" || src == def.Name {
			continue
		}
		installed = append(installed, groups[src].fns...)
	}
	doc.Add("Installed Modules",
		nameShortWrapped(installed, func(f *modFunction) (string, string) {
			return f.CmdName(), f.Short()
		}),
	)

	// Builtins last.
	doc.Add("Builtins",
		nameShortWrapped(h.Builtins(), func(c *ShellCommand) (string, string) {
			return c.Name(), c.Short()
		}),
	)

	types := "<command>"
	if len(doc.Groups) > 2 {
		types += " | <function>"
	}

	doc.Add("", fmt.Sprintf(`Use ".help %s" for more information.`, types))

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
		if mainObj := m.GetObject(m.Name); mainObj != nil {
			description = mainObj.Description
		}
	}
	if description != "" {
		meta.WriteString("\n\n")
		meta.WriteString(description)
	}
	if meta.Len() > 0 {
		doc.Add("Module", meta.String())
	}

	// When entrypoint proxying is active, MainObject is Query and the
	// constructor is the synthetic `with` function. Look up the module's
	// named main object to get the original constructor and its functions.
	constr := m.MainObject.AsObject.Constructor
	mainObj := m.GetObject(m.Name)
	if mainObj != nil && mainObj.Constructor != nil && mainObj.Constructor.Name != "" {
		constr = mainObj.Constructor
	}

	usage := h.FunctionUseLine(m, constr)

	if len(constr.Args) > 0 {
		constructor := new(strings.Builder)
		constructor.WriteString("Usage: ")
		constructor.WriteString(usage)

		if constr.Description != "" {
			constructor.WriteString("\n\n")
			constructor.WriteString(constr.Description)
		}

		doc.Add("Entrypoint", constructor.String())

		if args := constr.RequiredArgs(); len(args) > 0 {
			doc.AddSection(
				"Required Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return strings.TrimPrefix(a.Usage(), "--"), a.Long()
				}),
			)
		}
		if args := constr.OptionalArgs(); len(args) > 0 {
			doc.AddSection(
				"Optional Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return a.Usage(), a.Long()
				}),
			)
		}
	}

	// Show the module's own functions, not all of Query's.
	var fns []*modFunction
	if mainObj != nil {
		fns = mainObj.GetFunctions()
	} else {
		fns = m.MainObject.AsFunctionProvider().GetFunctions()
	}
	if len(fns) > 0 {
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

// isHiddenFunction returns true for internal functions that are callable
// but should not clutter .help output: the synthetic "with" constructor
// mechanism.
func isHiddenFunction(name string) bool {
	return name == "with"
}

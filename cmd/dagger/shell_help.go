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
	types := []string{"<command>"}

	doc.Add(
		"Builtin Commands",
		nameShortWrapped(h.Builtins(), func(c *ShellCommand) (string, string) {
			return c.Name(), c.Short()
		}),
	)

	if fns := h.getDefaultFunctions(); len(fns) > 0 {
		doc.Add(
			"Available Module Functions",
			nameShortWrapped(fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			}),
		)
		types = append(types, "<function>")
	}

	if deps := h.getCurrentDependencies(); len(deps) > 0 {
		doc.Add(
			"Available Module Dependencies",
			nameShortWrapped(deps, func(dep *moduleDef) (string, string) {
				return dep.Name, dep.Short()
			}),
		)
		types = append(types, "<dependency>")
	}

	doc.Add("Standard Commands", nameShortWrapped(h.Stdlib(), func(c *ShellCommand) (string, string) {
		return c.Name(), c.Short()
	}))

	doc.Add("", fmt.Sprintf(`Use ".help %s" for more information.`, strings.Join(types, " | ")))

	return doc.String()
}

func (h *shellCallHandler) getDefaultFunctions() []*modFunction {
	def, _ := h.GetModuleDef(nil)
	if def == nil {
		return nil
	}
	if def.MainObject.AsObject.Constructor.HasRequiredArgs() {
		return nil
	}
	fns := def.MainObject.AsFunctionProvider().GetFunctions()
	if len(fns) == 0 {
		return nil
	}
	return fns
}

func (h *shellCallHandler) getCurrentDependencies() []*moduleDef {
	def, _ := h.GetModuleDef(nil)
	if def == nil {
		return nil
	}
	return def.Dependencies
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
	d.Groups = append(d.Groups, ShellDocSection{Title: title, Body: body})
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

// shellFunctionUseLine returns the usage line fine for a function
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

func (h *shellCallHandler) ModuleDoc(st *ShellState, m *moduleDef) string {
	var doc ShellDoc

	meta := new(strings.Builder)
	meta.WriteString(m.Name)
	if m.Description != "" {
		meta.WriteString("\n\n")
		meta.WriteString(m.Description)
	}
	if meta.Len() > 0 {
		doc.Add("Module", meta.String())
	}

	fn := m.MainObject.AsObject.Constructor
	if len(fn.Args) > 0 {
		constructor := new(strings.Builder)
		constructor.WriteString("Usage: ")
		constructor.WriteString(h.FunctionUseLine(m, fn))

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

	// If it's just `.help` and the current module doesn't have required args,
	// can use the default constructor and show available functions.
	if st.IsEmpty() && st.ModDigest == "" && !fn.HasRequiredArgs() {
		if fns := m.MainObject.AsFunctionProvider().GetFunctions(); len(fns) > 0 {
			doc.Add(
				"Available Functions",
				nameShortWrapped(fns, func(f *modFunction) (string, string) {
					return f.CmdName(), f.Short()
				}),
			)
			doc.Add("", `Use ".help <function>" for more information on a function.`)
		}
	}

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

	if rettype := fn.ReturnType.Short(); rettype != "" {
		doc.Add("Returns", rettype)
	}

	if fn.ReturnType.AsFunctionProvider() != nil {
		u := strings.TrimSuffix(usage, " [options]")
		doc.Add("", fmt.Sprintf(`Use "%s | .help" for available functions.`, u))
	}

	return doc.String()
}

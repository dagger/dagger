package main

import (
	"fmt"
	"strings"

	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
	"github.com/spf13/cobra"
)

const (
	helpIndent = uint(2)
)

var coreGroup = &cobra.Group{
	ID:    "core",
	Title: "Dagger Core Commands",
}

var shellGroups = []*cobra.Group{
	moduleGroup,
	coreGroup,
	cloudGroup,
	{
		ID:    "",
		Title: "Additional Commands",
	},
}

func (h *shellCallHandler) GroupBuiltins(groupID string) []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.builtins))
	for _, c := range h.Builtins() {
		if c.GroupID == groupID {
			l = append(l, c)
		}
	}
	return l
}

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

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, name))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <function>" for more information on a function.`, name))
	sb.WriteString("\n")

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

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, name))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <command>" for more information on a command.`, name))
	sb.WriteString("\n")

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

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, shellDepsCmdName))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <dependency>" for more information on a dependency.`, shellDepsCmdName))
	sb.WriteString("\n")

	return sb.String()
}

func (h *shellCallHandler) StdlibHelp() string {
	var doc ShellDoc

	doc.Add("Commands", nameShortWrapped(h.Stdlib(), func(c *ShellCommand) (string, string) {
		return c.Name(), c.Description
	}))

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <command>" for more information on a command.`, shellStdlibCmdName))

	return doc.String()
}

func (h *shellCallHandler) CoreHelp() string {
	var doc ShellDoc

	def := h.modDef(nil)

	doc.Add(
		"Available Functions",
		nameShortWrapped(def.GetCoreFunctions(), func(f *modFunction) (string, string) {
			return f.CmdName(), f.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <function>" for more information on a function.`, shellCoreCmdName))

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
		nameShortWrapped(def.Dependencies, func(dep *moduleDependency) (string, string) {
			return dep.Name, dep.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <dependency>" for more information on a dependency.`, shellDepsCmdName))

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
func shellFunctionUseLine(md *moduleDef, fn *modFunction) string {
	sb := new(strings.Builder)

	if fn == md.MainObject.AsObject.Constructor {
		sb.WriteString(md.ModRef)
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

func shellModuleDoc(st *ShellState, m *moduleDef) string {
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
		constructor.WriteString(shellFunctionUseLine(m, fn))

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

	// If it's just `.doc` and the current module doesn't have required args,
	// can use the default constructor and show available functions.
	if st.IsEmpty() && st.ModRef == "" && !fn.HasRequiredArgs() {
		if fns := m.MainObject.AsFunctionProvider().GetFunctions(); len(fns) > 0 {
			doc.Add(
				"Available Functions",
				nameShortWrapped(fns, func(f *modFunction) (string, string) {
					return f.CmdName(), f.Short()
				}),
			)
			doc.Add("", `Use ".doc <function>" for more information on a function.`)
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
		doc.Add("", `Use ".doc <function>" for more information on a function.`)
	}

	return doc.String()
}

func shellFunctionDoc(md *moduleDef, fn *modFunction) string {
	var doc ShellDoc

	if fn.Description != "" {
		doc.Add("", fn.Description)
	}

	usage := shellFunctionUseLine(md, fn)
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
		doc.Add("", fmt.Sprintf(`Use "%s | .doc" for available functions.`, u))
	}

	return doc.String()
}

package daggercmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	cobra.AddTemplateFunc("artifactCommandUsage", artifactCommandUsage)
	cobra.AddTemplateFunc("artifactCommandFlags", artifactCommandFlags)
	for _, cmd := range []*cobra.Command{checksCmd, generateCmd, upCmd, shellCmd, agentCmd} {
		cmd.SetUsageTemplate(`{{ "Usage" | toUpperBold }}
{{ artifactCommandUsage . }}

{{ artifactCommandFlags . | trimTrailingWhitespaces }}
`)
	}
}

func artifactCommandUsage(cmd *cobra.Command) string {
	var run, list string
	switch cmd.Name() {
	case "check":
		run, list = "Run checks", "List checks"
	case "generate":
		run, list = "Generate files", "List generators"
	case "up":
		run, list = "Start services", "List services"
	case "shell":
		run, list = "Open a shell", "List shells"
	case "agent":
		run, list = "Start agents", "List agents"
	}
	listUsage := cmd.CommandPath() + " -l [FILTERS] [OPTIONS]"
	width := max(len(cmd.UseLine()), len(listUsage))
	return fmt.Sprintf("  %-*s   %s\n  %-*s   %s", width, cmd.UseLine(), run, width, listUsage, list)
}

func artifactCommandFlags(cmd *cobra.Command) string {
	flags := pflag.NewFlagSet("help", pflag.ContinueOnError)
	flags.AddFlagSet(availableFlagsForCommand(cmd, cmd.LocalFlags()))
	flags.AddFlagSet(inheritedFlags(cmd))
	groups := map[string]*pflag.FlagSet{}
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || flag.Name == "failfast" {
			return
		}
		copy := *flag
		group := "Options"
		switch flag.Name {
		case "generated", "skip":
			group = "Filters"
		case "list", "all", "absolute":
			group = "List options"
		case "workspace":
			group, copy.Usage = "Workspace", "Select a workspace at `location` (local path or Git ref)"
		case "env":
			group, copy.Usage = "Workspace", "Apply workspace environment `name`"
		case "load-module":
			group, copy.Usage = "Workspace", "Use a one-off `module`"
		case "engine":
			group, copy.Usage = "Engine", "Select an `engine` (or set DAGGER_ENGINE)"
		case "allow-llm":
			group, copy.Usage = "Engine", "Allow `module` to access LLM APIs; use 'all' for all modules"
		case "shell-on-error":
			group, copy.Usage = "Engine", "Open a shell when a container command fails"
		case "progress":
			group, copy.Usage = "Progress", "Select progress `mode`"
		case "quiet":
			group, copy.Usage = "Progress", "Reduce progress output"
		case "silent":
			group, copy.Usage = "Progress", "Hide progress output"
		case "verbose":
			group, copy.Usage = "Progress", "Show more detail; repeat for more"
		case "debug":
			group, copy.Usage = "Progress", "Show engine diagnostics"
		case "web":
			group, copy.Usage = "Progress", "Open the trace in a browser"
		case "no-exit":
			group, copy.Usage = "Progress", "Keep the terminal UI open"
		}
		if len(flag.Annotations[artifactDimensionFlag]) > 0 {
			group = "Filters"
		}
		if groups[group] == nil {
			groups[group] = pflag.NewFlagSet(group, pflag.ContinueOnError)
		}
		if flag.Value.Type() == "count" {
			// Count flags take no value. An empty pflag placeholder hides the type.
			copy.Usage = "``" + copy.Usage
		}
		if flag.Name == "generated" {
			copy.Name = "generated[=BOOL]"
			// The workspace setting supplies the default; the flag's storage
			// default does not describe it. This copy is only used for help.
			copy.DefValue = "false"
		}
		groups[group].AddFlag(&copy)
	})
	var out strings.Builder
	for _, title := range []string{"Filters", "Options", "List options", "Workspace", "Engine", "Progress"} {
		group := groups[title]
		if title != "Filters" && (group == nil || !group.HasAvailableFlags()) {
			continue
		}
		out.WriteString(toUpperBold(title) + "\n")
		if title == "Filters" {
			link := "LINK..."
			if cmd.Name() == "shell" {
				link = "LINK"
			}
			fmt.Fprintf(&out, "  %s   Select artifacts by DAG link; place links after flags.\n            See 'dagger help links'.\n", link)
		}
		if group != nil {
			usage := flagUsagesWrapped(sortRequiredFlags(group))
			group.VisitAll(func(flag *pflag.Flag) {
				name, _ := pflag.UnquoteUsage(flag)
				if name != "" {
					usage = strings.Replace(usage, "--"+flag.Name+" "+name+" ", "--"+flag.Name+" "+strings.ToUpper(name)+" ", 1)
				}
			})
			out.WriteString(usage)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

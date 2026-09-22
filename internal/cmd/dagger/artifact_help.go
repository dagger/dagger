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
			group, copy.Usage = "Workspace options", "Select a workspace at `location` (local path or Git ref)"
		case "env":
			group, copy.Usage = "Workspace options", "Apply workspace environment `name`"
		case "load-module":
			group, copy.Usage = "Workspace options", "Use a one-off `module`"
		case "engine":
			group, copy.Usage = "Execution options", "Select an `engine` (or set DAGGER_ENGINE)"
		case "allow-llm":
			group, copy.Usage = "Execution options", "Allow `module` to access LLM APIs; use 'all' for all modules"
		case "shell-on-error":
			group, copy.Usage = "Execution options", "Open a shell when a container command fails"
		case "progress":
			group, copy.Usage = "Tracing options", "Select progress `mode`"
		case "quiet":
			group, copy.Usage = "Tracing options", "Reduce progress output"
		case "silent":
			group, copy.Usage = "Tracing options", "Hide progress output"
		case "verbose":
			group, copy.Usage = "Tracing options", "Show more detail; repeat for more"
		case "debug":
			group, copy.Usage = "Tracing options", "Show engine diagnostics"
		case "web":
			group, copy.Usage = "Tracing options", "Open the trace in a browser"
		case "no-exit":
			group, copy.Usage = "Tracing options", "Keep the terminal UI open"
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
			copy.Name = "generated[=true|false]"
			// The workspace setting supplies the default; the flag's storage
			// default does not describe it. This copy is only used for help.
			copy.DefValue = "false"
		}
		groups[group].AddFlag(&copy)
	})
	filters := pflag.NewFlagSet("filters", pflag.ContinueOnError)
	filters.SortFlags = false
	if group := groups["Filters"]; group != nil {
		group.VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "generated[=true|false]" && flag.Name != "skip" {
				filters.AddFlag(flag)
			}
		})
		if generated := group.Lookup("generated[=true|false]"); generated != nil {
			filters.AddFlag(generated)
		}
	}
	link, target := "LINK...", "artifacts"
	switch cmd.Name() {
	case "check":
		target = "checks"
	case "generate":
		target = "generators"
	case "up":
		target = "services"
	case "agent":
		target = "agents"
	case "shell":
		link, target = "LINK", "containers and directories"
	}
	// This help-only entry lets pflag align and wrap the positional argument
	// with the flags. It is never registered on the command.
	filters.Bool(link, false, "Filter "+target+" by their DAG link. See 'dagger help links'")
	if group := groups["Filters"]; group != nil {
		if skip := group.Lookup("skip"); skip != nil {
			filters.AddFlag(skip)
		}
	}
	groups["Filters"] = filters
	var out strings.Builder
	for _, title := range []string{"Filters", "Options", "List options", "Workspace options", "Execution options", "Tracing options"} {
		group := groups[title]
		if title != "Filters" && (group == nil || !group.HasAvailableFlags()) {
			continue
		}
		out.WriteString(toUpperBold(title) + "\n")
		if group != nil {
			ordered := group
			if title != "Filters" {
				ordered = sortRequiredFlags(group)
			}
			usage := flagUsagesWrapped(ordered)
			if title == "Filters" {
				usage = strings.Replace(usage, "--"+link+" ", link+"   ", 1)
			}
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

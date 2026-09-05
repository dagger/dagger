// Package cobradocs renders Markdown reference documentation for a Cobra
// command tree, independent of any particular CLI.
package cobradocs

import (
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/spf13/pflag"
)

type MarkdownOptions struct {
	// Frontmatter is prepended verbatim to the output, before any command docs.
	Frontmatter string
	// IncludeFlag controls whether a flag is included in a command's reference.
	// A nil function includes all flags.
	IncludeFlag func(*cobra.Command, *pflag.Flag) bool
}

// Markdown writes reference documentation for root and all of its subcommands.
func Markdown(root *cobra.Command, w io.Writer, opts MarkdownOptions) error {
	root.DisableAutoGenTag = true

	if opts.Frontmatter != "" {
		if _, err := io.WriteString(w, opts.Frontmatter); err != nil {
			return err
		}
	}

	return markdown(root, w, opts)
}

// HideCommands hides every command in the tree for which condition reports true,
// pruning it (and its subtree) from generated output.
func HideCommands(cmd *cobra.Command, condition func(*cobra.Command) bool) {
	if condition(cmd) {
		cmd.Hidden = true
		return
	}
	for _, c := range cmd.Commands() {
		HideCommands(c, condition)
	}
}

func markdown(cmd *cobra.Command, w io.Writer, opts MarkdownOptions) error {
	if err := generateCommandMarkdown(cmd, w, opts.IncludeFlag); err != nil {
		return err
	}

	for _, c := range cmd.Commands() {
		if c.Hidden || len(c.Deprecated) > 0 {
			continue
		}
		// Help topics have no Run, so Cobra does not call them available.
		// They are reference content, so the generated docs include them.
		if !c.IsAvailableCommand() && !c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := markdown(c, w, opts); err != nil {
			return err
		}
	}

	return nil
}

func generateCommandMarkdown(cmd *cobra.Command, w io.Writer, includeFlag func(*cobra.Command, *pflag.Flag) bool) error {
	if includeFlag != nil {
		defer HideFlags(cmd, func(flag *pflag.Flag) bool { return includeFlag(cmd, flag) })()
	}
	return doc.GenMarkdownCustom(cmd, w, linkHandler)
}

// HideFlags hides every visible flag of cmd for which keep reports false, and
// returns the function that shows them again.
func HideFlags(cmd *cobra.Command, keep func(*pflag.Flag) bool) (restore func()) {
	var hidden []*pflag.Flag
	for _, flags := range []*pflag.FlagSet{cmd.InheritedFlags(), cmd.NonInheritedFlags()} {
		flags.VisitAll(func(flag *pflag.Flag) {
			if !flag.Hidden && !keep(flag) {
				flag.Hidden = true
				hidden = append(hidden, flag)
			}
		})
	}
	return func() {
		for _, flag := range hidden {
			flag.Hidden = false
		}
	}
}

// linkHandler links to other commands in the same document via a fragment.
func linkHandler(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	return "#" + strings.ReplaceAll(base, "_", "-")
}

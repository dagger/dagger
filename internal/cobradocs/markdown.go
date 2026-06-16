// Package cobradocs renders Markdown reference documentation for a Cobra
// command tree, independent of any particular CLI.
package cobradocs

import (
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

type MarkdownOptions struct {
	// Frontmatter is prepended verbatim to the output, before any command docs.
	Frontmatter string
}

// Markdown writes reference documentation for root and all of its subcommands.
func Markdown(root *cobra.Command, w io.Writer, opts MarkdownOptions) error {
	root.DisableAutoGenTag = true

	if opts.Frontmatter != "" {
		if _, err := io.WriteString(w, opts.Frontmatter); err != nil {
			return err
		}
	}

	return markdown(root, w)
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

func markdown(cmd *cobra.Command, w io.Writer) error {
	if err := doc.GenMarkdownCustom(cmd, w, linkHandler); err != nil {
		return err
	}

	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := markdown(c, w); err != nil {
			return err
		}
	}

	return nil
}

// linkHandler links to other commands in the same document via a fragment.
func linkHandler(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	return "#" + strings.ReplaceAll(base, "_", "-")
}

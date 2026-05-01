package cobradocs

import (
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

type MarkdownOptions struct {
	Frontmatter string
}

func Markdown(root *cobra.Command, w io.Writer, opts MarkdownOptions) error {
	root.DisableAutoGenTag = true

	if opts.Frontmatter != "" {
		if _, err := io.WriteString(w, opts.Frontmatter); err != nil {
			return err
		}
	}

	return markdown(root, w)
}

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

func linkHandler(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	return "#" + strings.ReplaceAll(base, "_", "-")
}

package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func newGenCmd() *cobra.Command {
	var gendoc string

	var cmd = &cobra.Command{
		Use:    "gen",
		Short:  "Generate CLI reference documentation",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Printf("Generating dagger command-line documentation in %q...\n", gendoc)
			return genRun(rootCmd, gendoc)
		},
	}

	cmd.Flags().StringVar(
		&gendoc,
		"file",
		"./docs/versioned_docs/version-zenith/reference/979596-cli.mdx",
		"the file to write the reference doc",
	)

	return cmd
}

func genRun(cmd *cobra.Command, target string) error {
	cmd.DisableAutoGenTag = true

	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	name := filepath.Base(target)

	// extract id and slug from filename for the frontmatter
	match := regexp.
		MustCompile(`^(\d+)-(.+)\.mdx?$`).
		FindStringSubmatch(name)

	if len(match) < 2 {
		return fmt.Errorf("invalid filename: %s", name)
	}

	docid := match[1]
	slug := match[2]

	frontmatter := fmt.Sprintf(`---
slug: /reference/%s/%s/
pagination_next: null
pagination_prev: null
---

import PartialExperimentalDocs from '../partials/_experimental.mdx';

# Reference

<PartialExperimentalDocs />

`,
		docid,
		slug,
	)

	if _, err := io.WriteString(f, frontmatter); err != nil {
		return err
	}

	// link to other commands in the same document with a fragment
	linkHandler := func(name string) string {
		base := strings.TrimSuffix(name, path.Ext(name))
		return "#" + strings.ReplaceAll(base, "_", "-")
	}

	return docGenMarkdown(cmd, f, linkHandler)
}

// docGenMarkdown generates reference markdown documentation for the given command
func docGenMarkdown(cmd *cobra.Command, w io.Writer, linkHandler func(string) string) error {
	// TODO: the completion Long fields  are causing issues with docusaurus
	// because of examples with `$(...)`:
	//   Unexpected character `(` (U+0028) before name, expected a
	//   character that can start a name, such as a letter, `$`, or `_`"
	// Need to wrap those examples in a code block.
	if cmd.CommandPath() == "dagger completion" {
		return nil
	}

	if err := doc.GenMarkdownCustom(cmd, w, linkHandler); err != nil {
		return err
	}

	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := docGenMarkdown(c, w, linkHandler); err != nil {
			return err
		}
	}

	return nil
}

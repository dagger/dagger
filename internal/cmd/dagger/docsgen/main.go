package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	daggercmd "github.com/dagger/dagger/internal/cmd/dagger"
	"github.com/dagger/dagger/internal/cobradocs"
	"github.com/spf13/cobra"
)

func main() {
	var (
		output              string
		frontmatterPath     string
		includeExperimental bool
	)

	flag.StringVar(&output, "out", "", "write generated Markdown to this path")
	flag.StringVar(&frontmatterPath, "frontmatter", "", "prepend the contents of this file")
	flag.BoolVar(&includeExperimental, "include-experimental", false, "include experimental commands")
	flag.Parse()

	if output == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}

	var frontmatter []byte
	if frontmatterPath != "" {
		var err error
		frontmatter, err = os.ReadFile(frontmatterPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read frontmatter: %v\n", err)
			os.Exit(1)
		}
	}

	root := daggercmd.NewRootCommand(daggercmd.Options{})

	if !includeExperimental {
		cobradocs.HideCommands(root, isExperimental)
	}

	cobradocs.HideCommands(root, func(cmd *cobra.Command) bool {
		return cmd.CommandPath() == "dagger completion"
	})

	buf := new(bytes.Buffer)
	if err := cobradocs.Markdown(root, buf, cobradocs.MarkdownOptions{
		Frontmatter: string(frontmatter),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "generate markdown: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(output, buf.Bytes(), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", output, err)
		os.Exit(1)
	}
}

func isExperimental(cmd *cobra.Command) bool {
	if _, ok := cmd.Annotations["experimental"]; ok {
		return true
	}
	var experimental bool
	cmd.VisitParents(func(cmd *cobra.Command) {
		if _, ok := cmd.Annotations["experimental"]; ok {
			experimental = true
			return
		}
	})
	return experimental
}

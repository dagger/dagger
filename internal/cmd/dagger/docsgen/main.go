// Command docsgen renders the Dagger CLI reference Markdown from the assembled
// Cobra command tree. It replaces the former hidden `dagger gen` command.
//
// It is driven by the //go:generate directive in the CLI-reference docs module
// (docs/current_docs/reference), which runs it from the repo root so the output
// lands inside that module's tree.
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	daggercmd "github.com/dagger/dagger/internal/cmd/dagger"
	"github.com/dagger/dagger/internal/cobradocs"
	"github.com/spf13/cobra"
)

// frontmatter is prepended to the generated reference. It is embedded so the
// generator has no runtime file dependency when run inside a container.
//
//go:embed frontmatter.mdx
var frontmatter string

func main() {
	var (
		output              string
		includeExperimental bool
	)

	flag.StringVar(&output, "out", "", "write generated Markdown to this path")
	flag.BoolVar(&includeExperimental, "include-experimental", false, "include experimental commands")
	flag.Parse()

	if output == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}

	root := daggercmd.RootCommand()

	if !includeExperimental {
		cobradocs.HideCommands(root, daggercmd.IsExperimental)
	}

	// The completion command's Long help contains `$(...)` examples that break
	// Docusaurus parsing; exclude it from the reference.
	cobradocs.HideCommands(root, func(cmd *cobra.Command) bool {
		return cmd.CommandPath() == "dagger completion"
	})

	buf := new(bytes.Buffer)
	if err := cobradocs.Markdown(root, buf, cobradocs.MarkdownOptions{
		Frontmatter: frontmatter,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "generate markdown: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(output, buf.Bytes(), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", output, err)
		os.Exit(1)
	}
}

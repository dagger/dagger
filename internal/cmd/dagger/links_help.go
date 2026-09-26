package daggercmd

import "github.com/spf13/cobra"

var linksHelpCmd = &cobra.Command{
	Use:   "links",
	Short: "How to select artifacts with DAG links",
	Long: `A DAG link selects artifacts. A relative link uses the current workspace.
An absolute link specifies a workspace and revision.

FORMS
  dag://go/test                              Relative link
  dag://github.com/acme/project@main:go/test  Absolute link
  dag+check://go/test                        Filter by artifact type

SELECTION
  A path selects that artifact and its children. Several links select their
  combined results. With no link, commands use their default selection.

  The dag:// prefix is optional for short paths: go/test is a relative link.
  The legacy form go:test is also accepted.

  Path patterns accept wildcards: * matches within a path segment, and **
  matches across segments. Quote links with wildcards or query parameters.

  --skip accepts the same link filters and excludes their matches from the
  selected checks. Its workspace prefix does not change the selected workspace.

DIMENSIONS
  Query parameters select dimension keys:
    'dag://?module=go&check=test'

  Select a module with --module=go, or --go when available.
  If --go conflicts with another flag, use --by-go.
  The equivalent link is 'dag://?module=go'. Module names come from the
  workspace configuration.

  Type flags match path suffixes. For example, --check=stale selects all
  matching checks. Use --check=/go/generate/stale to match a complete path.
  Use 'dagger check --help' to see available filters.

  Repeated keys in one dimension select alternatives. Different dimensions
  must all match. Named flags, such as --module=go, apply to all links.
`,
}

func init() {
	rootCmd.AddCommand(linksHelpCmd)
}

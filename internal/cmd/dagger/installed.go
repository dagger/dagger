package daggercmd

import (
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

var moduleListTree bool

// installedCmd lists installed modules in the current workspace.
var installedCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed modules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			if moduleListTree {
				return listWorkspaceModuleTree(ctx, cmd.OutOrStdout(), engineClient.Dagger())
			}
			return listWorkspaceModules(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

func init() {
	installedCmd.Flags().BoolVar(&moduleListTree, "tree", false, "Show each module's declared dependencies and clients as a tree")
}

type moduleListRow struct {
	Name   string
	Source string
}

func listWorkspaceModules(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	var res struct {
		CurrentWorkspace struct {
			Modules []moduleListRow
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { modules { name source } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return err
	}
	return printModuleList(out, res.CurrentWorkspace.Modules)
}

func printModuleList(out io.Writer, modules []moduleListRow) error {
	if len(modules) == 0 {
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSOURCE"); err != nil {
		return err
	}
	for _, module := range modules {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", module.Name, module.Source); err != nil {
			return err
		}
	}
	return w.Flush()
}

type moduleTreeNode struct {
	Label    string
	Children []*moduleTreeNode
}

// listWorkspaceModuleTree keys modules by their workspace-relative path, or by
// their ref for git modules, so a module reached twice is expanded once.
func listWorkspaceModuleTree(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	var res struct {
		CurrentWorkspace struct {
			Modules []moduleListRow
			SDKs    []struct {
				Clients []struct{ Scope, Source string }
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { modules { name source } sdks { clients { scope: name source } } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return err
	}

	query := core.NewQuery(dag)
	load := func(source string) *core.ModuleSource {
		if workspace.IsLocalRef(source, "") {
			return query.CurrentWorkspace().ModuleSource(path.Join("/", source))
		}
		return query.ModuleSource(source)
	}
	modules := res.CurrentWorkspace.Modules
	names := map[string]string{}
	// Workspace modules expand at the root, so any earlier appearance under
	// another module is a repeat.
	seen := map[string]bool{}
	for i, module := range modules {
		modules[i].Source = moduleTreeKey(module.Source)
		names[modules[i].Source] = module.Name
		seen[modules[i].Source] = true
	}
	clients := map[string][]string{}
	var scopes []string
	for _, sdk := range res.CurrentWorkspace.SDKs {
		for _, c := range sdk.Clients {
			scope := moduleTreeKey(c.Scope)
			if _, ok := clients[scope]; !ok {
				scopes = append(scopes, scope)
			}
			clients[scope] = append(clients[scope], c.Source)
		}
	}

	var expand func(label, key string, src *core.ModuleSource) *moduleTreeNode
	visit := func(label, key string, src *core.ModuleSource) *moduleTreeNode {
		if seen[key] {
			return &moduleTreeNode{Label: label + " (*)"}
		}
		seen[key] = true
		return expand(label, key, src)
	}
	clientNodes := func(scope string) []*moduleTreeNode {
		var nodes []*moduleTreeNode
		for _, source := range clients[scope] {
			key, src := moduleTreeKey(source), load(source)
			name := names[key]
			if name == "" {
				name, _ = src.ModuleName(ctx)
			}
			nodes = append(nodes, visit(fmt.Sprintf("client %s (%s)", name, key), key, src))
		}
		return nodes
	}
	expand = func(label, key string, src *core.ModuleSource) *moduleTreeNode {
		var res struct {
			Node struct {
				AsString     string
				Dependencies []struct {
					ID                              core.ID
					ModuleName, Kind, AsString, Pin string
				}
			}
		}
		id, err := src.ID(ctx)
		if err == nil {
			err = dag.Do(ctx, &dagger.Request{
				Query:     `query($id: ID!) { node(id: $id) { ... on ModuleSource { asString dependencies { id moduleName kind asString pin } } } }`,
				Variables: map[string]any{"id": id},
			}, &dagger.Response{Data: &res})
		}
		if err != nil {
			line, _, _ := strings.Cut(err.Error(), "\n")
			label += " (error: " + strings.TrimSpace(telemetry.ErrorOriginRegex.ReplaceAllString(line, "")) + ")"
		}
		node := &moduleTreeNode{Label: label}
		for _, dep := range res.Node.Dependencies {
			depKey, depLabel := dep.AsString, dep.AsString
			if dep.Kind != string(core.ModuleSourceKindGitSource) {
				// A local asString is a host-absolute path, so Rel cannot fail;
				// rebasing it on the parent's key keeps it workspace-relative.
				rel, _ := filepath.Rel(res.Node.AsString, dep.AsString)
				depKey = moduleTreeKey(path.Join(key, filepath.ToSlash(rel)))
				depLabel = depKey
			} else if dep.Pin != "" && !strings.Contains(depLabel, "@") {
				depLabel += "@" + dep.Pin[:min(12, len(dep.Pin))]
			}
			node.Children = append(node.Children, visit(fmt.Sprintf("dependency %s (%s)", dep.ModuleName, depLabel), depKey, core.Ref[*core.ModuleSource](query, dep.ID)))
		}
		node.Children = append(node.Children, clientNodes(key)...)
		return node
	}

	var nodes []*moduleTreeNode
	for _, module := range modules {
		nodes = append(nodes, expand(fmt.Sprintf("%s (%s)", module.Name, module.Source), module.Source, load(module.Source)))
	}
	for _, scope := range scopes {
		if _, ok := names[scope]; !ok {
			nodes = append(nodes, &moduleTreeNode{Label: fmt.Sprintf("scope %s (%s)", strings.TrimPrefix(scope, "./"), scope), Children: clientNodes(scope)})
		}
	}
	_, err := io.WriteString(out, ".\n"+formatModuleTree(nodes, ""))
	return err
}

// moduleTreeKey cleans a local source and spells it with a leading "./" for
// display, the way dagger.toml writes client targets.
func moduleTreeKey(source string) string {
	if !workspace.IsLocalRef(source, "") {
		return source
	}
	p := path.Clean(source)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return p
	}
	return "./" + p
}

func formatModuleTree(nodes []*moduleTreeNode, prefix string) string {
	var out string
	for i, node := range nodes {
		branch, indent := "├── ", "│   "
		if i == len(nodes)-1 {
			branch, indent = "└── ", "    "
		}
		out += prefix + branch + node.Label + "\n" + formatModuleTree(node.Children, prefix+indent)
	}
	return out
}

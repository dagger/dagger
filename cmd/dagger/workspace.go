package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/util/gitutil"
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Short:   "Manage the current workspace",
	GroupID: workspaceGroup.ID,
}

var workspaceRootCmd = &cobra.Command{
	Use:   "root",
	Short: "Print the workspace root",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()
			address, err := ws.Address(ctx)
			if err != nil {
				return fmt.Errorf("load workspace address: %w", err)
			}
			cwd, err := ws.Cwd(ctx)
			if err != nil {
				return fmt.Errorf("load workspace cwd: %w", err)
			}
			root, err := workspaceRootFromAddress(address, cwd)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), root)
			return err
		})
	},
}

var workspaceCwdCmd = &cobra.Command{
	Use:   "cwd",
	Short: "Print the workspace cwd",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			cwd, err := engineClient.Dagger().CurrentWorkspace().Cwd(ctx)
			if err != nil {
				return fmt.Errorf("load workspace cwd: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), cwd)
			return err
		})
	},
}

var workspaceConfigFileCmd = &cobra.Command{
	Use:   "config-file",
	Short: "Print the selected workspace config file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			configFile, err := engineClient.Dagger().CurrentWorkspace().ConfigFile(ctx)
			if err != nil {
				return fmt.Errorf("load workspace config file: %w", err)
			}
			if configFile == "" {
				configFile = "none"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), configFile)
			return err
		})
	},
}

var workspaceInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create workspace config",
	Long:  "Create .dagger/config.toml for the current workspace.",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceInit,
}

var workspaceConfigCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: `Get or set workspace configuration values in .dagger/config.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.

With --env, reads show the effective env-applied view while writes target that
environment's overlay. Explicit env.* keys always address raw overlay storage.

Local module source values are stored relative to .dagger/config.toml.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runWorkspaceConfig,
}

var workspaceRemotesCmd = &cobra.Command{
	Use:   "remotes",
	Short: "List selectable remote workspace addresses",
	Args:  cobra.NoArgs,
	RunE:  WorkspaceRemotes,
}

var workspaceActivityCmd = &cobra.Command{
	Use:   "activity",
	Short: "List recent Cloud activity for the selected workspace",
	Args:  cobra.NoArgs,
	RunE:  WorkspaceActivity,
}

func init() {
	workspaceCmd.AddCommand(workspaceConfigCmd)
	workspaceCmd.AddCommand(workspaceConfigFileCmd)
	workspaceCmd.AddCommand(workspaceCwdCmd)
	workspaceCmd.AddCommand(workspaceInitCmd)
	workspaceCmd.AddCommand(workspaceRemotesCmd)
	workspaceCmd.AddCommand(workspaceRootCmd)
	workspaceCmd.AddCommand(workspaceActivityCmd)

	addWorkspaceHereFlag(workspaceConfigCmd)
	addWorkspaceHereFlag(workspaceInitCmd)

	setWorkspaceFlagPolicy(workspaceInitCmd, workspaceFlagPolicyLocalOnly)
}

func addWorkspaceHereFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&workspaceHere, "here", false, "Write workspace config at the selected workspace cwd")
}

func runWorkspaceInit(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		configDir, err := initWorkspace(ctx, engineClient.Dagger())
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created workspace config in %s\n", configDir)
		return err
	})
}

func runWorkspaceConfig(cmd *cobra.Command, args []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		ws := engineClient.Dagger().CurrentWorkspace()

		switch len(args) {
		case 0:
			return printWorkspaceConfig(ctx, cmd.OutOrStdout(), ws, "")
		case 1:
			return printWorkspaceConfig(ctx, cmd.OutOrStdout(), ws, args[0])
		case 2:
			return writeWorkspaceConfig(ctx, ws, args[0], args[1])
		default:
			return fmt.Errorf("expected 0-2 arguments, got %d", len(args))
		}
	})
}

func initWorkspace(ctx context.Context, dag *dagger.Client) (string, error) {
	return dag.CurrentWorkspace().Init(ctx, dagger.WorkspaceInitOpts{Here: workspaceHere})
}

func printWorkspaceConfig(ctx context.Context, out io.Writer, ws *dagger.Workspace, key string) error {
	value, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{Key: key})
	if err != nil {
		return err
	}

	value = strings.TrimRight(value, "\n")
	if key == "" && value == "" {
		return nil
	}

	_, err = fmt.Fprintln(out, value)
	return err
}

func writeWorkspaceConfig(ctx context.Context, ws *dagger.Workspace, key, value string) error {
	_, err := ws.ConfigWrite(ctx, key, value, dagger.WorkspaceConfigWriteOpts{Here: workspaceHere})
	return err
}

func installWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, ref, name string, here bool) error {
	msg, err := dag.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: name, Here: here})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

//nolint:unparam
func workspaceRootFromAddress(address, cwd string) (string, error) {
	if cwd == "" || cwd == "." {
		return fileURLPathOrAddress(address), nil
	}

	if parsed, err := url.Parse(address); err == nil && parsed.Scheme == "file" {
		root := strings.TrimSuffix(filepath.Clean(parsed.Path), string(filepath.Separator)+filepath.Clean(cwd))
		return root, nil
	}

	version := ""
	base := address
	if idx := strings.LastIndex(address, "@"); idx > strings.LastIndex(address, "/") {
		base = address[:idx]
		version = address[idx:]
	}
	root := strings.TrimSuffix(filepath.ToSlash(base), "/"+filepath.ToSlash(filepath.Clean(cwd)))
	return root + version, nil
}

func fileURLPathOrAddress(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" {
		return address
	}
	return parsed.Path
}

func WorkspaceRemotes(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{LoadWorkspaceModules: true}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		address, err := dag.CurrentWorkspace().Address(ctx)
		if err != nil {
			return err
		}
		remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("workspace remotes only supports remote workspaces for now; selected workspace is %s", address)
		}

		rows, err := loadWorkspaceRemoteRows(ctx, dag, remote)
		if err != nil {
			return err
		}
		if err := annotateWorkspaceRemoteRows(ctx, rows); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Cloud check annotation failed: %v\n", err)
		}
		renderWorkspaceRemoteRows(cmd, rows)
		return nil
	})
}

func WorkspaceActivity(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{LoadWorkspaceModules: true}, func(ctx context.Context, engineClient *client.Client) error {
		address, err := engineClient.Dagger().CurrentWorkspace().Address(ctx)
		if err != nil {
			return err
		}
		if !workspaceAddressLooksRemote(address) {
			return fmt.Errorf("workspace activity only supports remote workspaces for now; selected workspace is %s", address)
		}

		res, _, err := cloudCLI.loadCloudCheckRowsForWorkspace(ctx, address, nil, false)
		if err != nil {
			return err
		}
		rows := workspaceActivityRows(res.Rows)
		if len(rows) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No Cloud activity found for %s.\n", address)
			return nil
		}
		renderWorkspaceActivityRows(cmd, rows)
		return nil
	})
}

type workspaceRemoteAddress struct {
	CloneRef    string
	Path        string
	Version     string
	BaseAddress string
}

func workspaceAddressLooksRemote(address string) bool {
	address = strings.TrimSpace(address)
	if address == "" || strings.HasPrefix(address, "file://") {
		return false
	}
	switch core.FastModuleSourceKindCheck(address, "") {
	case core.ModuleSourceKindLocal:
		return false
	case core.ModuleSourceKindGit:
		return true
	default:
		return strings.Contains(address, ".") && !strings.HasPrefix(address, "/")
	}
}

func parseWorkspaceRemoteAddress(ctx context.Context, address string) (workspaceRemoteAddress, bool, error) {
	if !workspaceAddressLooksRemote(address) {
		return workspaceRemoteAddress{}, false, nil
	}
	if strings.Contains(address, "#") {
		gitURL, err := gitutil.ParseURL(address)
		if err != nil {
			return workspaceRemoteAddress{}, false, fmt.Errorf("parse remote workspace address %q: %w", address, err)
		}
		version := ""
		workspacePath := "."
		if gitURL.Fragment != nil {
			version = gitURL.Fragment.Ref
			workspacePath = cleanWorkspaceRemoteSubdir(gitURL.Fragment.Subdir)
		}
		cloneRef := gitURL.Remote()
		return workspaceRemoteAddress{
			CloneRef:    cloneRef,
			Path:        workspacePath,
			Version:     version,
			BaseAddress: core.GitRefString(cloneRef, workspacePath, ""),
		}, true, nil
	}
	parsed, err := core.ParseGitRefString(ctx, address)
	if err != nil {
		return workspaceRemoteAddress{}, false, fmt.Errorf("parse remote workspace address %q: %w", address, err)
	}
	workspacePath := parsed.RepoRootSubdir
	if workspacePath == "" || workspacePath == "/" {
		workspacePath = "."
	}
	return workspaceRemoteAddress{
		CloneRef:    parsed.SourceCloneRef,
		Path:        workspacePath,
		Version:     parsed.ModVersion,
		BaseAddress: core.GitRefString(parsed.SourceCloneRef, workspacePath, ""),
	}, true, nil
}

func cleanWorkspaceRemoteSubdir(subdir string) string {
	if subdir == "" {
		return "."
	}
	subdir = filepath.Clean(subdir)
	subdir = strings.TrimPrefix(subdir, string(filepath.Separator))
	if subdir == "" || subdir == "." {
		return "."
	}
	return subdir
}

type workspaceRemoteRow struct {
	Kind    string
	Address string
	Checks  string
}

func loadWorkspaceRemoteRows(ctx context.Context, dag *dagger.Client, remote workspaceRemoteAddress) ([]*workspaceRemoteRow, error) {
	repo := dag.Git(remote.CloneRef)
	branches, err := repo.Branches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list branches for %s: %w", remote.CloneRef, err)
	}
	tags, err := repo.Tags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", remote.CloneRef, err)
	}

	sort.Strings(branches)
	sort.Strings(tags)

	rows := make([]*workspaceRemoteRow, 0, len(branches)+len(tags)+1)
	seen := map[string]struct{}{}
	add := func(kind, version string) {
		if version == "" {
			return
		}
		address := core.GitRefString(remote.CloneRef, remote.Path, version)
		if _, ok := seen[address]; ok {
			return
		}
		seen[address] = struct{}{}
		rows = append(rows, &workspaceRemoteRow{
			Kind:    kind,
			Address: address,
			Checks:  "-",
		})
	}

	for _, branch := range branches {
		add("branch", branch)
	}
	for _, tag := range tags {
		add("tag", tag)
	}
	if remote.Version != "" {
		add(workspaceRemoteVersionKind(remote.Version), remote.Version)
	}
	return rows, nil
}

func workspaceRemoteVersionKind(version string) string {
	if cloudPullRequestNumber(version) != "" {
		return "pr"
	}
	if looksLikeGitSHA(version) {
		return "sha"
	}
	return "ref"
}

func looksLikeGitSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func annotateWorkspaceRemoteRows(ctx context.Context, rows []*workspaceRemoteRow) error {
	if len(rows) == 0 {
		return nil
	}
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, rows[0].Address)
	if err != nil || !ok {
		return err
	}
	res, err := cloudCLI.loadCloudCheckRowsNoLogin(ctx, cloudCheckSelectorFlags{
		GitHubRepo: []string{remote.CloneRef},
		Workspace:  []string{remote.BaseAddress},
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		checkRows, _, err := cloudRowsForWorkspaceAddress(ctx, res.Rows, row.Address, nil)
		if err != nil {
			continue
		}
		row.Checks = cloudChecksSummary(checkRows)
	}
	return nil
}

func renderWorkspaceRemoteRows(cmd *cobra.Command, rows []*workspaceRemoteRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tADDRESS\tCHECKS")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Kind, row.Address, row.Checks)
	}
	_ = tw.Flush()
}

type workspaceActivityRow struct {
	UpdatedAt time.Time
	Kind      string
	Address   string
	Checks    string
}

func workspaceActivityRows(rows []cloudCheckRow) []workspaceActivityRow {
	groups := map[string][]cloudCheckRow{}
	order := []string{}
	for _, row := range rows {
		kind, address := cloudCheckWorkspaceAddress(row)
		if address == "" {
			continue
		}
		key := row.Commit.CommitSHA + "\x00" + kind + "\x00" + address
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}

	out := make([]workspaceActivityRow, 0, len(order))
	for _, key := range order {
		group := groups[key]
		kind, address := cloudCheckWorkspaceAddress(group[0])
		out = append(out, workspaceActivityRow{
			UpdatedAt: latestCloudRowTime(group),
			Kind:      kind,
			Address:   address,
			Checks:    cloudChecksSummary(group),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func renderWorkspaceActivityRows(cmd *cobra.Command, rows []workspaceActivityRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tKIND\tADDRESS\tCHECKS")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", relativeTime(row.UpdatedAt), row.Kind, row.Address, row.Checks)
	}
	_ = tw.Flush()
}

package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/gitref"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/util/gitutil"
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Aliases: []string{"ws"},
	Short:   "Inspect or configure your workspace (cwd, remotes, config, etc.)",
	Long: `Inspect or configure your workspace.

A workspace is a project configured to use Dagger — a directory holding
a dagger.toml that records installed modules, environment overlays, and
settings. Most commands (install, check, generate, up, settings, ...)
operate on the workspace reachable from the current directory. The -W
flag selects a different workspace (local path or git ref); --env
applies a named overlay; dagger.toml is the source of truth.

Run with no subcommand to print a digest of workspace state (cwd, root,
current remote, installed modules summary).`,
	Annotations: map[string]string{
		visibleAliasesAnnotation: "ws",
	},
	Args: cobra.NoArgs,
	RunE: runWorkspaceDigest,
}

// runWorkspaceDigest prints a one-shot summary of the current workspace:
// cwd, root, selected remote, all selectable remotes, and a brief list of
// installed modules. Bound to bare `dagger workspace` invocation; replaces
// the briefly-considered `dagger status` verb.
func runWorkspaceDigest(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		ws := dag.CurrentWorkspace()
		out := cmd.OutOrStdout()

		cwd, err := ws.Cwd(ctx)
		if err != nil {
			return fmt.Errorf("load workspace cwd: %w", err)
		}
		address, err := ws.Address(ctx)
		if err != nil {
			return fmt.Errorf("load workspace address: %w", err)
		}
		root, err := workspaceRootFromAddress(address, cwd)
		if err != nil {
			return err
		}
		configFile, err := ws.ConfigFile(ctx)
		if err != nil {
			return fmt.Errorf("load workspace config file: %w", err)
		}
		if configFile == "" {
			configFile = "none"
		}

		fmt.Fprintf(out, "cwd:     %s\n", cwd)
		fmt.Fprintf(out, "root:    %s\n", root)
		fmt.Fprintf(out, "config:  %s\n", configFile)

		// Remote info — best-effort; an unconfigured remote is normal.
		if _, remote, err := selectedRemoteWorkspaceAddress(ctx, "workspace"); err == nil && remote != "" {
			fmt.Fprintf(out, "remote:  %s\n", remote)
		}

		// Modules summary.
		var res struct {
			CurrentWorkspace struct {
				ModuleList []struct {
					Name   string
					Source string
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query: `query { currentWorkspace { moduleList { name source } } }`,
		}, &dagger.Response{Data: &res}); err != nil {
			return fmt.Errorf("list installed modules: %w", err)
		}
		mods := res.CurrentWorkspace.ModuleList
		if len(mods) == 0 {
			fmt.Fprintln(out, "modules: (none installed)")
			return nil
		}
		fmt.Fprintln(out, "modules:")
		for _, m := range mods {
			fmt.Fprintf(out, "  %s (%s)\n", m.Name, m.Source)
		}
		return nil
	})
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

var workspaceConfigCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: `Get or set workspace configuration values in dagger.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.

With --env, reads show the effective env-applied view while writes target that
environment's overlay. Explicit env.* keys always address raw overlay storage.

Local module source values are stored relative to dagger.toml.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runWorkspaceConfig,
}

var workspaceRemotesCmd = &cobra.Command{
	Use:   "remotes",
	Short: "List selectable remote workspace addresses",
	Args:  cobra.NoArgs,
	RunE:  WorkspaceRemotes,
}

var workspaceRemoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Print the selectable remote address for the current workspace",
	Args:  cobra.NoArgs,
	RunE:  WorkspaceRemote,
}

// activityCmd is the top-level activity command (hoisted from what was
// `dagger workspace activity`).
var activityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show recent activity (runs, traces, etc.) for this workspace",
	Args:  cobra.NoArgs,
	RunE:  WorkspaceActivity,
}

var workspaceActivityAll bool

func init() {
	workspaceCmd.AddCommand(workspaceConfigCmd)
	workspaceCmd.AddCommand(workspaceConfigFileCmd)
	workspaceCmd.AddCommand(workspaceCwdCmd)
	workspaceCmd.AddCommand(workspaceRemoteCmd)
	workspaceCmd.AddCommand(workspaceRemotesCmd)
	workspaceCmd.AddCommand(workspaceRootCmd)

	addWorkspaceHereFlag(workspaceConfigCmd)
	activityCmd.Flags().BoolVarP(&workspaceActivityAll, "all", "a", false, "Show activity from all remotes in the current workspace")
}

func addWorkspaceHereFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&workspaceHere, "here", false, "Write workspace config at the selected workspace cwd")
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

func uninstallWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, name string, here bool) error {
	msg, err := dag.CurrentWorkspace().Uninstall(ctx, name, dagger.WorkspaceUninstallOpts{Here: here})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

func workspaceRootFromAddress(address, cwd string) (string, error) {
	cwd, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		return fileURLPathOrAddress(address), nil
	}

	if parsed, err := url.Parse(address); err == nil && parsed.Scheme == "file" {
		root, ok := stripWorkspaceCwdSuffix(filepath.Clean(parsed.Path), cwd)
		if !ok {
			return "", fmt.Errorf("workspace address %q is not within workspace cwd %q", address, cwd)
		}
		return root, nil
	}

	version := ""
	base := address
	if idx := strings.LastIndex(address, "@"); idx > strings.LastIndex(address, "/") {
		base = address[:idx]
		version = address[idx:]
	}
	root, ok := stripWorkspaceCwdSlashSuffix(filepath.ToSlash(base), filepath.ToSlash(filepath.Clean(cwd)))
	if !ok {
		return "", fmt.Errorf("workspace address %q is not within workspace cwd %q", address, cwd)
	}
	return root + version, nil
}

func stripWorkspaceCwdSuffix(base, cwd string) (string, bool) {
	base = filepath.Clean(base)
	cwd = filepath.Clean(cwd)
	suffix := string(filepath.Separator) + cwd
	if !strings.HasSuffix(base, suffix) {
		return "", false
	}
	root := strings.TrimSuffix(base, suffix)
	if root == "" {
		root = string(filepath.Separator)
	}
	return root, true
}

func stripWorkspaceCwdSlashSuffix(base, cwd string) (string, bool) {
	base = path.Clean(base)
	cwd = path.Clean(cwd)
	suffix := "/" + cwd
	if !strings.HasSuffix(base, suffix) {
		return "", false
	}
	root := strings.TrimSuffix(base, suffix)
	if root == "" {
		root = "/"
	}
	return root, true
}

func workspaceRelativeCwd(cwd string) (string, error) {
	if cwd == "" || cwd == "." || cwd == "/" {
		return "", nil
	}
	raw := filepath.ToSlash(cwd)
	if strings.HasPrefix(raw, "/") {
		raw = strings.TrimLeft(raw, "/")
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "", nil
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("workspace cwd %q escapes workspace root", cwd)
	}
	return filepath.FromSlash(cleaned), nil
}

func fileURLPathOrAddress(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" {
		return address
	}
	return parsed.Path
}

func WorkspaceRemote(cmd *cobra.Command, _ []string) error {
	address, ok, err := currentWorkspaceRemoteAddress(cmd.Context())
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), address)
	return err
}

func WorkspaceRemotes(cmd *cobra.Command, _ []string) error {
	remote, _, err := selectedRemoteWorkspaceAddress(cmd.Context(), "workspace remotes")
	if err != nil {
		return err
	}
	return withEngineSilent(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		rows, err := loadWorkspaceRemoteRows(ctx, dag, remote)
		if err != nil {
			return err
		}
		if err := annotateWorkspaceRemoteRows(ctx, rows); err != nil {
			if !errors.Is(err, errCloudNotAuthenticated) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Cloud check annotation failed: %v\n", err)
			}
		}
		renderWorkspaceRemoteRows(cmd, rows)
		return nil
	})
}

func WorkspaceActivity(cmd *cobra.Command, _ []string) error {
	remote, address, err := selectedRemoteWorkspaceAddress(cmd.Context(), "workspace activity")
	if err != nil {
		return err
	}
	if workspaceActivityAll {
		address = remote.BaseAddress
	}
	res, _, err := cloudCLI.loadCloudCheckRowsForWorkspaceAcrossUserOrgs(cmd.Context(), address, nil, true)
	if errors.Is(err, errCloudNotAuthenticated) {
		return fmt.Errorf("not authenticated; run 'dagger login' to view workspace activity")
	}
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
}

func currentWorkspaceRemoteAddress(ctx context.Context) (string, bool, error) {
	address := strings.TrimSpace(workspaceRef)
	if address != "" {
		_, ok, err := parseWorkspaceRemoteAddress(ctx, address)
		if err != nil {
			return "", false, err
		}
		if ok {
			return address, true, nil
		}
	}

	_, inferred, err := inferLocalWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		// Inference failures are not propagated: callers treat the empty
		// result as "no remote workspace selected".
		return "", false, nil //nolint:nilerr
	}
	return inferred, true, nil
}

func selectedRemoteWorkspaceAddress(ctx context.Context, command string) (workspaceRemoteAddress, string, error) {
	address := strings.TrimSpace(workspaceRef)
	if address != "" {
		remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
		if err != nil {
			return workspaceRemoteAddress{}, "", err
		}
		if ok {
			return remote, address, nil
		}
	}

	remote, inferred, err := inferLocalWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		if address == "" {
			return workspaceRemoteAddress{}, "", fmt.Errorf("%s requires a remote workspace selected with -W or a local workspace with git origin: %w", command, err)
		}
		return workspaceRemoteAddress{}, "", fmt.Errorf("%s only supports remote workspaces or local git workspaces; selected workspace is %s: %w", command, address, err)
	}
	return remote, inferred, nil
}

type localWorkspaceRemoteInfo struct {
	repoRoot      string
	cloneRef      string
	workspacePath string
}

func localWorkspaceRemoteInfoForAddress(ctx context.Context, address string) (localWorkspaceRemoteInfo, error) {
	localPath := address
	if localPath == "" {
		localPath = "."
	}
	if parsed, err := url.Parse(localPath); err == nil && parsed.Scheme == "file" {
		localPath = parsed.Path
	}
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return localWorkspaceRemoteInfo{}, fmt.Errorf("resolve local workspace path: %w", err)
	}
	if stat, err := os.Stat(absPath); err == nil && !stat.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	repoRoot, err := gitOutput(ctx, absPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return localWorkspaceRemoteInfo{}, fmt.Errorf("find git root: %w", err)
	}
	origin, err := gitOutput(ctx, repoRoot, "config", "--get", "remote.origin.url")
	if err != nil {
		return localWorkspaceRemoteInfo{}, fmt.Errorf("find git origin: %w", err)
	}

	detected, err := workspacepkg.DetectInRoot(ctx, localPathExists, absPath, repoRoot)
	if err != nil {
		return localWorkspaceRemoteInfo{}, err
	}
	workspaceDir := repoRoot
	if detected.ConfigFile != "" {
		workspaceDir = filepath.Join(repoRoot, filepath.Dir(detected.ConfigFile))
	}
	workspacePath, err := filepath.Rel(repoRoot, workspaceDir)
	if err != nil {
		return localWorkspaceRemoteInfo{}, fmt.Errorf("resolve workspace subdir: %w", err)
	}
	workspacePath = cleanWorkspaceRemoteSubdir(filepath.ToSlash(workspacePath))

	return localWorkspaceRemoteInfo{
		repoRoot:      repoRoot,
		cloneRef:      normalizeWorkspaceGitOrigin(origin),
		workspacePath: workspacePath,
	}, nil
}

func inferLocalWorkspaceRemoteAddress(ctx context.Context, address string) (workspaceRemoteAddress, string, error) {
	info, err := localWorkspaceRemoteInfoForAddress(ctx, address)
	if err != nil {
		return workspaceRemoteAddress{}, "", err
	}
	version, err := currentGitRef(ctx, info.repoRoot)
	if err != nil {
		return workspaceRemoteAddress{}, "", err
	}

	inferred := gitref.RefString(info.cloneRef, info.workspacePath, version)
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, inferred)
	if err != nil {
		return workspaceRemoteAddress{}, "", err
	}
	if !ok {
		return workspaceRemoteAddress{}, "", fmt.Errorf("inferred git origin %q is not a remote workspace address", info.cloneRef)
	}
	return remote, inferred, nil
}

func inferCleanLocalWorkspaceRemoteAddress(ctx context.Context, address string) (workspaceRemoteAddress, string, bool, error) {
	info, err := localWorkspaceRemoteInfoForAddress(ctx, address)
	if err != nil {
		return workspaceRemoteAddress{}, "", false, err
	}
	dirty, err := gitWorktreeDirty(ctx, info.repoRoot)
	if err != nil {
		return workspaceRemoteAddress{}, "", false, err
	}
	if dirty {
		return workspaceRemoteAddress{}, "", true, nil
	}
	commit, err := currentGitCommit(ctx, info.repoRoot)
	if err != nil {
		return workspaceRemoteAddress{}, "", false, err
	}

	inferred := gitref.RefString(info.cloneRef, info.workspacePath, commit)
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, inferred)
	if err != nil {
		return workspaceRemoteAddress{}, "", false, err
	}
	if !ok {
		return workspaceRemoteAddress{}, "", false, fmt.Errorf("inferred git origin %q is not a remote workspace address", info.cloneRef)
	}
	return remote, inferred, false, nil
}

// This is intentionally narrower than the workspace-git API: it only projects
// a local checkout into a copy/pasteable remote workspace address, without
// starting the engine or modeling full Git state.
func currentGitRef(ctx context.Context, repoRoot string) (string, error) {
	ref, err := gitOutput(ctx, repoRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil && ref != "" {
		return ref, nil
	}
	sha, err := gitOutput(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("find git ref: %w", err)
	}
	return sha, nil
}

func currentGitCommit(ctx context.Context, repoRoot string) (string, error) {
	sha, err := gitOutput(ctx, repoRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("find git commit: %w", err)
	}
	return sha, nil
}

func gitWorktreeDirty(ctx context.Context, repoRoot string) (bool, error) {
	status, err := gitOutput(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("check git status: %w", err)
	}
	return status != "", nil
}

func localPathExists(_ context.Context, path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		parent := filepath.Dir(path)
		if info.IsDir() {
			parent = filepath.Dir(filepath.Clean(path))
		}
		return parent, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Dir(path), false, nil
	}
	return "", false, err
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg != "" {
				return "", fmt.Errorf("%s", msg)
			}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func normalizeWorkspaceGitOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	gitURL, err := gitutil.ParseURL(origin)
	if err != nil {
		return strings.TrimSuffix(origin, ".git")
	}

	path := strings.TrimPrefix(gitURL.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	if gitURL.Host == "github.com" {
		return "github.com/" + path
	}

	if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
		parsed.Path = "/" + path
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	return strings.TrimSuffix(origin, ".git")
}

func withEngineSilent(ctx context.Context, params client.Params, fn runClientCallback) error {
	oldFrontend := Frontend
	oldOpts := opts
	Frontend = idtui.NewPlain(io.Discard)
	opts.Silent = true
	defer func() {
		Frontend = oldFrontend
		opts = oldOpts
	}()
	return withEngine(ctx, params, fn)
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
	switch gitref.FastKindCheck(address, "") {
	case gitref.KindLocal:
		return false
	case gitref.KindGit:
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
			BaseAddress: gitref.RefString(cloneRef, workspacePath, ""),
		}, true, nil
	}
	parsed, err := gitref.Parse(ctx, address)
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
		BaseAddress: gitref.RefString(parsed.SourceCloneRef, workspacePath, ""),
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
	Kind      string
	Address   string
	Autocheck string
	Checks    string
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
		address := gitref.RefString(remote.CloneRef, remote.Path, version)
		if _, ok := seen[address]; ok {
			return
		}
		seen[address] = struct{}{}
		rows = append(rows, &workspaceRemoteRow{
			Kind:      kind,
			Address:   address,
			Autocheck: "-",
			Checks:    "-",
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
	if state, ok, err := loadWorkspaceAutocheckState(ctx, remote); err != nil {
		return err
	} else if ok {
		for _, row := range rows {
			row.Autocheck = workspaceAutocheckStateString(state)
		}
	}
	res, err := cloudCLI.loadCloudCheckRowsAcrossUserOrgs(ctx, cloudCheckSelectorFlags{
		GitHubRepo: []string{remote.CloneRef},
		Workspace:  []string{remote.BaseAddress},
	}, false)
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
	fmt.Fprintln(tw, "KIND\tADDRESS\tAUTOCHECK\tCHECKS")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Kind, row.Address, row.Autocheck, row.Checks)
	}
	_ = tw.Flush()
}

type workspaceAutocheckState struct {
	OrgName        string
	OrgID          string
	Repo           string
	Enabled        bool
	IsPublic       bool
	InstallationID string
	SourceMode     string
	SelectedRepos  []string
}

func loadWorkspaceAutocheckState(ctx context.Context, remote workspaceRemoteAddress) (workspaceAutocheckState, bool, error) {
	client, err := workspaceAutocheckClient(ctx, false)
	if err != nil {
		return workspaceAutocheckState{}, false, err
	}
	return findWorkspaceAutocheckState(ctx, client, remote.CloneRef)
}

func setWorkspaceAutocheckState(ctx context.Context, remote workspaceRemoteAddress, enabled bool) (workspaceAutocheckState, error) {
	client, err := workspaceAutocheckClient(ctx, true)
	if err != nil {
		return workspaceAutocheckState{}, err
	}
	current, ok, err := findWorkspaceAutocheckState(ctx, client, remote.CloneRef)
	if err != nil {
		return workspaceAutocheckState{}, err
	}
	if !ok {
		current = workspaceAutocheckState{
			Repo:     remote.CloneRef,
			IsPublic: true,
		}
	}
	if current.Enabled == enabled {
		return current, nil
	}
	if current.OrgID == "" || current.InstallationID == "" {
		return workspaceAutocheckState{}, fmt.Errorf("no Cloud source mapping found for %s", current.Repo)
	}
	selected := setWorkspaceAutocheckRepoSelected(current.SelectedRepos, current.Repo, enabled)
	if !enabled && len(selected) == 0 {
		return workspaceAutocheckState{}, fmt.Errorf("turn workspace autocheck off: Cloud requires at least one selected repository for source %s", current.InstallationID)
	}
	if _, err := client.ConfigureOrgSource(ctx, current.OrgID, current.InstallationID, "SELECTED", selected); err != nil {
		return workspaceAutocheckState{}, fmt.Errorf("turn workspace autocheck %s: %w", onOff(enabled), err)
	}
	if enabled {
		if _, err := client.UpdateOrgRepoSetting(ctx, current.OrgID, current.Repo, current.IsPublic); err != nil {
			return workspaceAutocheckState{}, fmt.Errorf("update Cloud repo setting for %s: %w", current.Repo, err)
		}
	}
	current.Enabled = enabled
	current.SourceMode = "SELECTED"
	current.SelectedRepos = selected
	return current, nil
}

func workspaceAutocheckClient(ctx context.Context, login bool) (*cloudapi.Client, error) {
	client, _, err := cloudCLI.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func findWorkspaceAutocheckState(ctx context.Context, client *cloudapi.Client, repo string) (workspaceAutocheckState, bool, error) {
	repo = "github.com/" + normalizeGitHubRepo(repo)
	sources, err := client.Sources(ctx)
	if err != nil {
		return workspaceAutocheckState{}, false, fmt.Errorf("lookup Cloud sources: %w", err)
	}
	source, ok := workspaceSourceForRepo(sources, repo)
	if !ok || source.OrgName == nil {
		if ok, err := workspaceRemoteRepoExists(ctx, client, repo); err != nil {
			return workspaceAutocheckState{}, false, err
		} else if ok {
			return workspaceAutocheckState{
				Repo:     repo,
				Enabled:  false,
				IsPublic: true,
			}, true, nil
		}
		return workspaceAutocheckState{}, false, nil
	}
	org, err := client.OrgByName(ctx, *source.OrgName)
	if err != nil {
		return workspaceAutocheckState{}, false, fmt.Errorf("lookup Cloud org %q: %w", *source.OrgName, err)
	}
	mapped, err := workspaceMappedSourceForInstallation(ctx, client, org.Name, source.ID)
	if err != nil {
		return workspaceAutocheckState{}, false, err
	}
	sourceRepos, err := client.SourceRepositories(ctx, source.ID, org.ID)
	if err != nil {
		return workspaceAutocheckState{}, false, fmt.Errorf("lookup Cloud source repositories for %s: %w", repo, err)
	}
	selected, enabled := workspaceSelectedSourceRepos(sourceRepos, repo)
	setting, err := client.OrgRepoSetting(ctx, org.Name, repo)
	if err != nil {
		return workspaceAutocheckState{}, false, fmt.Errorf("fetch cloud repo setting for org %q: %w", org.Name, err)
	}
	isPublic := true
	if setting != nil {
		isPublic = setting.IsPublic
	}
	if len(selected) > 0 || setting != nil {
		return workspaceAutocheckState{
			OrgName:        org.Name,
			OrgID:          org.ID,
			Repo:           repo,
			Enabled:        enabled,
			IsPublic:       isPublic,
			InstallationID: source.ID,
			SourceMode:     mapped.Mode,
			SelectedRepos:  selected,
		}, true, nil
	}
	return workspaceAutocheckState{}, false, nil
}

func workspaceSourceForRepo(sources []cloudapi.Source, repo string) (cloudapi.Source, bool) {
	owner, _, ok := strings.Cut(normalizeGitHubRepo(repo), "/")
	if !ok {
		return cloudapi.Source{}, false
	}
	for _, source := range sources {
		if strings.EqualFold(source.Name, owner) {
			return source, true
		}
	}
	return cloudapi.Source{}, false
}

func workspaceMappedSourceForInstallation(ctx context.Context, client *cloudapi.Client, orgName, installationID string) (cloudapi.MappedSource, error) {
	sources, err := client.OrgMappedSources(ctx, orgName)
	if err != nil {
		return cloudapi.MappedSource{}, fmt.Errorf("lookup Cloud mapped sources for org %q: %w", orgName, err)
	}
	for _, source := range sources {
		if source.InstallationID == installationID {
			return source, nil
		}
	}
	return cloudapi.MappedSource{}, fmt.Errorf("cloud source %s is not mapped to org %q", installationID, orgName)
}

func workspaceSelectedSourceRepos(repos []cloudapi.SourceRepository, repo string) ([]string, bool) {
	repo = normalizeGitHubRepo(repo)
	selected := make([]string, 0, len(repos))
	enabled := false
	for _, candidate := range repos {
		if !candidate.Selected {
			continue
		}
		candidateRepo := "github.com/" + normalizeGitHubRepo(candidate.Repository)
		selected = append(selected, candidateRepo)
		if normalizeGitHubRepo(candidate.Repository) == repo {
			enabled = true
		}
	}
	return selected, enabled
}

func setWorkspaceAutocheckRepoSelected(selected []string, repo string, enabled bool) []string {
	repo = "github.com/" + normalizeGitHubRepo(repo)
	out := make([]string, 0, len(selected)+1)
	found := false
	for _, candidate := range selected {
		candidate = "github.com/" + normalizeGitHubRepo(candidate)
		if normalizeGitHubRepo(candidate) == normalizeGitHubRepo(repo) {
			found = true
			if !enabled {
				continue
			}
		}
		out = append(out, candidate)
	}
	if enabled && !found {
		out = append(out, repo)
	}
	sort.Strings(out)
	return out
}

func workspaceRemoteRepoExists(ctx context.Context, client *cloudapi.Client, repo string) (bool, error) {
	repos, err := client.Repos(ctx)
	if err != nil {
		return false, fmt.Errorf("lookup cloud repos: %w", err)
	}
	repo = normalizeGitHubRepo(repo)
	for _, candidate := range repos {
		if normalizeGitHubRepo(candidate.FullName) == repo {
			return true, nil
		}
	}
	return false, nil
}

func workspaceAutocheckStateString(state workspaceAutocheckState) string {
	return onOff(state.Enabled)
}

type workspaceActivityRow struct {
	UpdatedAt   time.Time
	Address     string
	URL         string
	Description string
	Checks      string
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
		_, address := cloudCheckWorkspaceAddress(group[0])
		out = append(out, workspaceActivityRow{
			UpdatedAt:   latestCloudRowTime(group),
			Address:     address,
			URL:         firstNonEmptyCloudDimension(group, "url"),
			Description: workspaceActivityDescription(group),
			Checks:      cloudChecksEmojiSummary(group),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func renderWorkspaceActivityRows(cmd *cobra.Command, rows []workspaceActivityRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tADDRESS\tURL\tDESCRIPTION\tCHECKS")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", relativeTime(row.UpdatedAt), row.Address, row.URL, row.Description, row.Checks)
	}
	_ = tw.Flush()
}

func firstNonEmptyCloudDimension(rows []cloudCheckRow, dim string) string {
	for _, row := range rows {
		if value := row.Dimensions[dim]; value != "" {
			return value
		}
	}
	return ""
}

func workspaceActivityDescription(rows []cloudCheckRow) string {
	if description := firstNonEmptyCloudDimension(rows, "description"); description != "" {
		return description
	}
	for _, row := range rows {
		if summary := firstCommitMessageLine(row.Commit.CommitMessage); summary != "" {
			return summary
		}
	}
	return ""
}

func firstCommitMessageLine(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

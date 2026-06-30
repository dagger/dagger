package daggercmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// setupCmd is the idempotent "ensure environment works" doctor verb.
// Walks through three optional steps, each with a confirmation prompt:
// (1) Cloud login, (2) workspace migration, (3) recommended modules.
var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Ensure Dagger is properly set up and operational in the workspace",
	Long: `Ensure Dagger is properly set up and operational in the workspace.

Walks through three steps, each prompted independently:

  1. Cloud login        — authenticate with Dagger Cloud.
  2. Workspace migrate  — convert any legacy dagger.json projects to
                          the current workspace format.
  3. Recommended modules — suggest modules to install based on files
                           present in the workspace.

Idempotent: safe to run anytime. No-ops what's already in good shape.
Each step can be skipped at the prompt. With --auto-apply, all steps
are applied without prompting. In non-interactive mode (no TTY) the
default is to skip steps that would mutate state.`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	if err := setupStepLogin(cmd); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Step 1 (login): %v\n", err)
		// Login failures shouldn't block migration/recommend.
	}

	// All steps run under ONE Frontend (one live TUI) so their prompts can be
	// huh forms the TUI renders — a raw stdin prompt would be drawn over by the
	// progress display. withSetupSessions provides connect() so the install can
	// run in a FRESH engine session: the per-client workspace is detected once
	// and cached for a session's lifetime, so install must not reuse the
	// migrate session or it would still see the legacy dagger.json.
	return withSetupSessions(cmd.Context(), func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error {
		// Session 1: migrate (apply form) + recommend (compute + install
		// confirm form). The migrate write lands here; the session is closed
		// before the install session opens so the workspace lock is released.
		var (
			recs     []recommendation
			install  bool
			migrated bool
		)
		if err := func() error {
			sess, closeSess, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSess()
			dag := sess.Dagger()
			migrated, err = setupStepMigrate(ctx, cmd, dag)
			if err != nil {
				return fmt.Errorf("step 2 (migrate): %w", err)
			}
			recs, install, err = planRecommend(ctx, cmd, dag)
			if err != nil {
				return fmt.Errorf("step 3 (recommend): %w", err)
			}
			return nil
		}(); err != nil {
			return err
		}

		// Session 2: a fresh session re-detects the workspace migrated in
		// session 1 as native. Resolve any SDK that migration recorded by short
		// name to its real ref (sdks.json), then install accepted recommendations.
		needInstall := install && len(recs) > 0
		if migrated || needInstall {
			sess, closeSess, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSess()
			dag := sess.Dagger()
			// Only a migration writes SDK installs by short name, so scope the
			// resolution to that case — never rewrite an already-native config.
			if migrated {
				if err := setupResolveMigratedSDKs(ctx, cmd, dag); err != nil {
					return fmt.Errorf("step 2 (resolve SDKs): %w", err)
				}
			}
			if needInstall {
				if err := installRecommended(ctx, cmd, dag, recs); err != nil {
					return fmt.Errorf("step 3 (install): %w", err)
				}
			}
		}

		fmt.Fprintln(out, "\nSetup complete.")
		return nil
	})
}

// --- Step 1: Cloud login ---

func setupStepLogin(cmd *cobra.Command) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Step 1: Cloud login")

	if _, err := cloudauth.GetCloudAuth(ctx); err == nil {
		fmt.Fprintln(out, "  Already logged in.")
		return nil
	}

	if !confirm(cmd, "  Log in to Dagger Cloud?") {
		fmt.Fprintln(out, "  Skipped.")
		return nil
	}

	if err := cloudauth.Login(ctx, cmd.ErrOrStderr(), cloudauth.WithAuthGate()); err != nil {
		return err
	}
	fmt.Fprintln(out, "  Logged in.")
	return nil
}

// --- Step 2: Migrate ---

const emptyWorkspaceSetupHint = `  No workspace has been loaded — nothing to migrate and no modules are
  installed here yet.

  Install a module to get started:

      dagger install <module>

  For example:
      dagger install github.com/dagger/go
`

// setupStepMigrate reports whether a migration was needed (and thus a fresh
// session should resolve SDKs migration may have recorded by short name).
func setupStepMigrate(ctx context.Context, cmd *cobra.Command, dag *dagger.Client) (bool, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nStep 2: Workspace migration")

	ws := dag.CurrentWorkspace()
	migration := ws.Migrate()
	changes := migration.Changes()

	changesID, err := changes.ID(ctx)
	if err != nil {
		return false, fmt.Errorf("compute migration: %w", err)
	}
	changes = dagger.Ref[*dagger.Changeset](dag, changesID)

	isEmpty, err := changes.IsEmpty(ctx)
	if err != nil {
		return false, fmt.Errorf("check migration: %w", err)
	}
	if isEmpty {
		configFile, err := ws.ConfigFile(ctx)
		if err != nil {
			return false, fmt.Errorf("check workspace config: %w", err)
		}
		if configFile == "" {
			// Nothing to migrate and no workspace config exists yet. Don't write a
			// default dagger.toml — guide the user to create one by installing a
			// module instead.
			fmt.Fprint(out, emptyWorkspaceSetupHint)
			return false, nil
		}
		fmt.Fprintln(out, "  No migration needed.")
		return false, nil
	}

	exportPath, err := currentWorkspaceExportPath(ctx, ws)
	if err != nil {
		return false, err
	}
	// handleChangesetResponseAt owns the apply prompt via a huh form when
	// autoApply is false — we don't run our own confirm() here, otherwise
	// the user would face two prompts back-to-back for the same action.
	if err := handleChangesetResponseAt(ctx, dag, changes, autoApply, exportPath); err != nil {
		return false, err
	}
	return true, nil
}

// setupResolveMigratedSDKs rewrites SDK installs that migration recorded by bare
// short name (e.g. `php`) to their real ref and canonical name from sdks.json,
// so the SDK is loadable for authoring (`dagger module init <sdk>`) instead of
// being treated as a local path. Runs in a post-migration session where the
// workspace is native; a no-op when nothing was recorded by short name (and
// when the user declined migration, leaving the legacy config in place).
func setupResolveMigratedSDKs(ctx context.Context, cmd *cobra.Command, dag *dagger.Client) error {
	ws := dag.CurrentWorkspace()
	raw, err := ws.ConfigRead(ctx)
	if err != nil {
		return err
	}
	cfg, err := workspace.ParseConfig([]byte(raw))
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, fix := range planMigratedSDKFixups(cfg) {
		if _, err := ws.ConfigWrite(ctx, "modules."+fix.ModuleName+".source", fix.Ref); err != nil {
			return fmt.Errorf("set %s SDK source: %w", fix.SDKName, err)
		}
		if _, err := ws.ConfigWrite(ctx, "modules."+fix.ModuleName+".as-sdk.name", fix.SDKName); err != nil {
			return fmt.Errorf("set %s SDK name: %w", fix.SDKName, err)
		}
		fmt.Fprintf(out, "  Resolved SDK %q to %s\n", fix.SDKName, fix.Ref)
	}
	return nil
}

// --- Step 3: Recommend modules ---

// planRecommend computes the recommended modules and prompts (via a Frontend
// form) whether to install them. It runs in the same session as migrate and
// returns the modules plus the user's decision; the actual install runs later
// in a fresh session (see runSetup) so it re-detects the migrated workspace.
func planRecommend(ctx context.Context, cmd *cobra.Command, dag *dagger.Client) (recs []recommendation, install bool, _ error) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nStep 3: Recommended modules")

	recs, err := runRecommend(ctx, dag)
	if errors.Is(err, errCloudNotAuthenticated) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		// Login or context issues shouldn't fail setup as a whole.
		fmt.Fprintf(out, "  Skipped: %v\n", err)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(recs) == 0 {
		fmt.Fprintln(out, "  No recommendations.")
		return nil, false, nil
	}

	install, err = confirmInstallRecommended(ctx, cmd, recs)
	if err != nil {
		return nil, false, err
	}
	if !install {
		fmt.Fprintln(out, "  Skipped.")
		return nil, false, nil
	}
	return recs, true, nil
}

// installRecommended installs the accepted recommended modules. It runs in a
// fresh session so dag.CurrentWorkspace() re-detects the workspace migrated in
// the migrate session as native — without this, install sees the cached legacy
// dagger.json and fails with "run dagger setup first".
func installRecommended(ctx context.Context, cmd *cobra.Command, dag *dagger.Client, recs []recommendation) error {
	out := cmd.OutOrStdout()
	for _, r := range recs {
		fmt.Fprintf(out, "  Installing %s...\n", r.Module.Repo)
		if err := installWorkspaceModule(ctx, out, dag, r.Module.Repo, "", false); err != nil {
			return fmt.Errorf("install %s: %w", r.Module.Repo, err)
		}
	}
	return nil
}

// confirmInstallRecommended asks whether to install the recommended modules.
// It prompts through the Frontend (a huh confirm with the recommendation table
// as its description) so it renders inside the live progress TUI — the same
// mechanism the migrate step uses for its apply prompt. With --auto-apply it
// returns true without prompting; in non-interactive mode it skips (the safe
// default — don't mutate state without a TTY).
func confirmInstallRecommended(ctx context.Context, cmd *cobra.Command, recs []recommendation) (bool, error) {
	if autoApply {
		return true, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(cmd.OutOrStdout(), "  Install recommended modules? [skipped: non-interactive — use --auto-apply to accept]")
		return false, nil
	}

	var table strings.Builder
	w := tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tDESCRIPTION\tMATCHED")
	for _, r := range recs {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Module.Repo, r.Module.Description, r.Match)
	}
	_ = w.Flush()

	var install bool
	form := idtui.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install recommended modules?").
				Description(table.String()).
				Affirmative("Install").
				Negative("Skip").
				Value(&install),
		),
	)
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return false, err
	}
	return install, nil
}

// currentWorkspaceExportPath returns the host filesystem path the current
// workspace should write to when applying a Changeset. Used by the migrate
// step (was previously in the dedicated migrate.go before that file was
// removed in the workspace slim-down).
func currentWorkspaceExportPath(ctx context.Context, ws *dagger.Workspace) (string, error) {
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return "", fmt.Errorf("workspace cwd: %w", err)
	}
	address, err := ws.Address(ctx)
	if err != nil {
		return "", fmt.Errorf("workspace address: %w", err)
	}
	wd, err := localWorkspaceAddressPath(address)
	if err != nil {
		return "", err
	}
	return workspaceRootFromCwd(wd, cwd)
}

func localWorkspaceAddressPath(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("workspace address %q: %w", address, err)
	}
	if u.Scheme != "file" || u.Path == "" {
		return "", fmt.Errorf("workspace migration requires a local file workspace, got %q", address)
	}
	return filepath.FromSlash(u.Path), nil
}

func workspaceRootFromCwd(wd, workspaceCwd string) (string, error) {
	wd, err := filepath.Abs(wd)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	workspaceCwd, err = workspaceRelativeCwd(workspaceCwd)
	if err != nil {
		return "", err
	}
	if workspaceCwd == "" {
		return wd, nil
	}
	root, ok := stripWorkspaceCwdSuffix(wd, workspaceCwd)
	if !ok {
		return "", fmt.Errorf("working directory %q is not within workspace cwd %q", wd, workspaceCwd)
	}
	return root, nil
}

// --- Confirm prompt helper ---

// confirm prompts the user with question and returns true if they accept.
// With --auto-apply, returns true without prompting.
// In non-interactive mode (no TTY on stdin), returns false (the safe default
// — skip rather than mutate state silently).
//
// The read is performed on a goroutine and races against ctx.Done() so a
// SIGINT during the prompt cancels cleanly rather than blocking on stdin
// forever. A read error other than EOF is reported to stderr instead of
// being silently treated as "user said no."
func confirm(cmd *cobra.Command, question string) bool {
	if autoApply {
		return true
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s [skipped: non-interactive — use --auto-apply to accept]\n", question)
		return false
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s [Y/n] ", question)

	type readResult struct {
		line string
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		reader := bufio.NewReader(cmd.InOrStdin())
		line, err := reader.ReadString('\n')
		done <- readResult{line: line, err: err}
	}()

	ctx := cmd.Context()
	select {
	case <-ctx.Done():
		fmt.Fprintln(cmd.OutOrStdout())
		return false
	case r := <-done:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			fmt.Fprintf(cmd.ErrOrStderr(), "prompt read error: %v\n", r.err)
			return false
		}
		line := strings.TrimSpace(strings.ToLower(r.line))
		return line == "" || line == "y" || line == "yes"
	}
}

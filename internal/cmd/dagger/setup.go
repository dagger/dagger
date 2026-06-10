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
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	if err := setupStepLogin(cmd); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Step 1 (login): %v\n", err)
		// Login failures shouldn't block migration/recommend.
	}

	return withEngine(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()

		if err := setupStepMigrate(ctx, cmd, dag); err != nil {
			return fmt.Errorf("step 2 (migrate): %w", err)
		}

		if err := setupStepRecommend(ctx, cmd, dag); err != nil {
			return fmt.Errorf("step 3 (recommend): %w", err)
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

func setupStepMigrate(ctx context.Context, cmd *cobra.Command, dag *dagger.Client) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nStep 2: Workspace migration")

	ws := dag.CurrentWorkspace()
	migration := ws.Migrate()
	changes := migration.Changes()

	changesID, err := changes.ID(ctx)
	if err != nil {
		return fmt.Errorf("compute migration: %w", err)
	}
	changes = dagger.Ref[*dagger.Changeset](dag, changesID)

	isEmpty, err := changes.IsEmpty(ctx)
	if err != nil {
		return fmt.Errorf("check migration: %w", err)
	}
	if isEmpty {
		fmt.Fprintln(out, "  No migration needed.")
		return nil
	}

	exportPath, err := currentWorkspaceExportPath(ctx, ws)
	if err != nil {
		return err
	}
	// handleChangesetResponseAt owns the apply prompt via a huh form when
	// autoApply is false — we don't run our own confirm() here, otherwise
	// the user would face two prompts back-to-back for the same action.
	return handleChangesetResponseAt(ctx, dag, changes, autoApply, exportPath)
}

// --- Step 3: Recommend modules ---

func setupStepRecommend(ctx context.Context, cmd *cobra.Command, dag *dagger.Client) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nStep 3: Recommended modules")

	recs, err := runRecommend(ctx, dag)
	if errors.Is(err, errCloudNotAuthenticated) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		// Login or context issues shouldn't fail setup as a whole.
		fmt.Fprintf(out, "  Skipped: %v\n", err)
		return nil
	}
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Fprintln(out, "  No recommendations.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  ADDRESS\tDESCRIPTION\tMATCHED")
	for _, r := range recs {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", r.Module.Repo, r.Module.Description, r.Match)
	}
	_ = w.Flush()

	if !confirm(cmd, "  Install recommended modules?") {
		fmt.Fprintln(out, "  Skipped.")
		return nil
	}

	for _, r := range recs {
		fmt.Fprintf(out, "  Installing %s...\n", r.Module.Repo)
		if err := installWorkspaceModule(ctx, out, dag, r.Module.Repo, "", false); err != nil {
			return fmt.Errorf("install %s: %w", r.Module.Repo, err)
		}
	}
	return nil
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

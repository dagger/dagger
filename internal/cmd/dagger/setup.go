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

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
	"github.com/mattn/go-isatty"
	toml "github.com/pelletier/go-toml"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// setupCmd is the idempotent "ensure environment works" doctor verb.
// Walks through optional steps, each with a confirmation prompt:
// (1) Cloud login, then EITHER (2) workspace migration when the workspace
// is legacy, OR (3) recommended modules when it is already current.
var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Ensure Dagger is properly set up and operational in the workspace",
	Long: `Ensure Dagger is properly set up and operational in the workspace.

Starts with a Cloud login prompt, then takes one of two paths:

  • Workspace migrate    — if a legacy dagger.json project is detected,
                           convert it to the current workspace format.
  • Recommended modules  — otherwise, suggest modules to install based
                           on files present in the workspace.

Migration and recommendations never run together: after applying a
migration, run setup again to see recommendations for the migrated
workspace. Declining the migration falls through to recommendations.

Run from a module subdirectory (a dagger.json below the repository
root), the migrate step converts just that module to dagger-module.toml:
no workspace is created and module recommendations are skipped. If that
config lists toolchains, they are installed into a dagger.toml at the
repository root — never a nested one. A subdirectory config with a
blueprint is left as legacy with a warning.

Toolchains of a module with an SDK are also added as dependencies in the
migrated dagger-module.toml, since 0.21 exposed toolchains to module code
the same way as dependencies. Remove any the module code does not use.

Idempotent: safe to run anytime. No-ops what's already in good shape.
Each step can be skipped at the prompt. With --auto-apply, workspace
changes and module recommendations are applied without prompting. Cloud
login is skipped in non-interactive mode; run dagger login separately.`,
	Args: cobra.NoArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, _ []string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("setup does not support --env; it configures the base workspace")
	}

	// All steps run under ONE Frontend (one live TUI) so their prompts can be
	// huh forms the TUI renders — a raw stdin prompt would be drawn over by the
	// progress display. withSetupSessions provides connect() so the install can
	// run in a FRESH engine session: the per-client workspace is detected once
	// and cached for a session's lifetime, so install must not reuse the
	// migrate session or it would still see the legacy dagger.json.
	var (
		setupUI       *setupUI
		setupLoginErr error
	)
	return withSetupSessions(cmd.Context(), func(ctx context.Context) {
		setupUI = newSetupUI(Frontend)
		setupLoginErr = setupStepLogin(ctx, cmd, cloudauth.GetCloudAuth, setupUI)
		if setupLoginErr != nil {
			if !errors.Is(setupLoginErr, idtui.ErrInterrupted) {
				setupUI.setLoginFailed(setupLoginErr)
			}
			if setupUI == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Step 1 (login): %v\n", setupLoginErr)
			}
			// Login failures shouldn't block migration/recommend.
		}
	}, func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) (rerr error) {
		if errors.Is(setupLoginErr, idtui.ErrInterrupted) {
			return setupLoginErr
		}
		defer func() {
			if rerr != nil {
				setupUI.fail(rerr)
			}
		}()
		ctx, setupSpan := Tracer().Start(ctx, "setup", telemetry.Passthrough())
		defer telemetry.EndWithCause(setupSpan, &rerr)
		setupStdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		defer setupStdio.Close()
		setupID := dagui.SpanID{SpanID: setupSpan.SpanContext().SpanID()}
		Frontend.SetPrimary(setupID)
		setupUI.setRoot(setupID)

		// Session 1: migrate (apply form) or recommend (compute + install
		// confirm form). The migrate write lands here; the session is closed
		// before the install session opens so the workspace lock is released.
		var (
			recs            []recommendation
			install         bool
			migrated        bool
			moduleOnly      bool
			migratedConfigs []string
		)
		if err := func() error {
			sess, closeSess, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSess()
			dag := sess.Dagger()
			migrated, moduleOnly, migratedConfigs, err = setupStepMigrate(ctx, dag, setupUI)
			if err != nil {
				return fmt.Errorf("step 2 (migrate): %w", err)
			}
			if migrated {
				// Migration and recommendations are alternative paths: piling
				// recommended modules onto a freshly migrated workspace adds
				// confusion, not value. Recommendations appear on the next
				// `dagger setup`. A declined migration falls through — the
				// user opted to keep the workspace as-is, so recommendations
				// still run.
				return nil
			}
			if moduleOnly {
				// Setup ran from a module subdirectory: the migration converts
				// just that module and creates no workspace. Recommendations
				// are workspace-scoped and would create one, so they never run
				// here — even when the migration was declined.
				return nil
			}
			recs, install, err = planRecommend(ctx, dag, setupUI)
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
		// A module-only migration writes no dagger.toml, so there is nothing to
		// resolve or install and no second session to open.
		needInstall := install && len(recs) > 0
		if (migrated && !moduleOnly) || needInstall {
			sess, closeSess, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSess()
			dag := sess.Dagger()
			// Only a migration writes SDK installs by short name, so scope the
			// resolution to that case — never rewrite an already-native config.
			if migrated {
				if err := setupResolveMigratedSDKs(ctx, dag, migratedConfigs); err != nil {
					return fmt.Errorf("step 2 (resolve SDKs): %w", err)
				}
			}
			if needInstall {
				if err := installRecommended(ctx, dag, recs, setupUI); err != nil {
					return fmt.Errorf("step 3 (install): %w", err)
				}
			}
		}

		if migrated {
			if moduleOnly {
				setupHumanMessage(setupUI, ctx, "module migrated", "Migrated the module in place; no workspace config was created.")
			} else {
				setupHumanMessage(setupUI, ctx, "migration next steps", "Run `dagger setup` again to see recommended modules for the migrated workspace.")
			}
		}
		if setupUI != nil {
			setupUI.complete()
		} else {
			fmt.Fprintln(setupStdio.Stdout, "Setup complete.")
		}
		return nil
	})
}

func setupHumanMessage(ui *setupUI, ctx context.Context, name, markdown string) {
	if ui != nil {
		ui.setMigrationMessage(markdown)
		return
	}
	setupMessage(ctx, name, markdown)
}

func setupRecommendMessage(ui *setupUI, ctx context.Context, name, markdown string) {
	if ui != nil {
		ui.setRecommendMessage(markdown)
		return
	}
	setupMessage(ctx, name, markdown)
}

// setupMessage emits human-facing Markdown as a revealed message span. The
// stable span name remains useful in telemetry while the TUI renders the log
// content in its place.
func setupMessage(ctx context.Context, name, markdown string) {
	ctx, span := Tracer().Start(ctx, name, telemetry.Reveal(), telemetry.Encapsulate())
	span.SetAttributes(attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived))
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	_, _ = fmt.Fprintln(stdio.Stdout, markdown)
	stdio.Close()
	telemetry.EndWithCause(span, nil)
}

// --- Step 1: Cloud login ---

func setupStepLogin(ctx context.Context, cmd *cobra.Command, getCloudAuth func(context.Context) (*cloudauth.Cloud, error), ui *setupUI) error {
	out := cmd.OutOrStdout()

	if ui == nil {
		fmt.Fprintln(out, "Step 1: Cloud login")
	} else {
		ui.setLoginPending("Checking Cloud account...")
	}

	if auth, err := getCloudAuth(ctx); err == nil && auth != nil {
		if ui == nil {
			fmt.Fprintln(out, "  Already logged in.")
		} else {
			ui.setLoginComplete("Already logged in.")
		}
		return nil
	}
	if !autoApply {
		disabled, err := setupCloudLoginPromptDisabled()
		if err != nil {
			return err
		}
		if disabled {
			if ui == nil {
				fmt.Fprintln(out, "  "+setupLoginSkippedHint)
			} else {
				ui.setLoginSkipped(setupLoginSkippedHint)
			}
			return nil
		}
	}

	if ui != nil {
		ui.setLoginPending("Waiting for login choice...")
	}
	choice, err := confirmSetupLogin(ctx, cmd, ui)
	if err != nil {
		return err
	}
	if choice == setupLoginNever {
		if err := disableSetupCloudLoginPrompt(); err != nil {
			return err
		}
	}
	if choice != setupLogin {
		message := "Skipped."
		if choice == setupLoginNever {
			message = setupLoginSkippedHint
		}
		if ui == nil {
			fmt.Fprintln(out, "  "+message)
		} else {
			ui.setLoginSkipped(message)
		}
		return nil
	}

	loginOut := cmd.ErrOrStderr()
	if ui != nil && ui.live {
		ui.setLoginPending("Waiting for authentication...")
		loginOut = setupLoginWriter{ui: ui}
	}
	if err := cloudauth.Login(ctx, loginOut); err != nil {
		return err
	}
	if ui == nil {
		fmt.Fprintln(out, "  Logged in.")
	} else {
		ui.setLoginComplete("Logged in.")
	}
	return nil
}

type setupLoginChoice string

const (
	setupLogin       setupLoginChoice = "login"
	setupLoginNotNow setupLoginChoice = "not-now"
	setupLoginNever  setupLoginChoice = "never"

	setupLoginSkippedHint = "Skipped. (dagger login to log in)"
)

func confirmSetupLogin(ctx context.Context, cmd *cobra.Command, ui *setupUI) (setupLoginChoice, error) {
	return confirmSetupLoginInteractive(ctx, cmd, ui, isatty.IsTerminal(os.Stdin.Fd()))
}

func confirmSetupLoginInteractive(ctx context.Context, cmd *cobra.Command, ui *setupUI, interactive bool) (setupLoginChoice, error) {
	if !interactive {
		if ui != nil {
			ui.setLoginSkipped("Skipped in non-interactive mode; run `dagger login` to log in.")
		}
		return setupLoginNotNow, nil
	}
	if ui == nil || !ui.live {
		if confirm(cmd, "  Log in to Dagger Cloud?") {
			return setupLogin, nil
		}
		return setupLoginNotNow, nil
	}
	if autoApply {
		return setupLogin, nil
	}
	choice := setupLogin
	form := huh.NewForm(huh.NewGroup(
		idtui.NewExplicitChoice(&choice,
			huh.NewOption("Log in", setupLogin),
			huh.NewOption("Not now", setupLoginNotNow),
			huh.NewOption("Never ask again", setupLoginNever),
		).
			Title("Log in to Dagger Cloud?").
			TitleLink("https://dagger.io/cloud").
			Description("For observability, compute, and persistence (ish).\nMore info: https://dagger.io/cloud"),
	))
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return "", err
	}
	return choice, nil
}

func setupCloudLoginPromptDisabled() (bool, error) {
	data, err := os.ReadFile(llmconfig.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read Dagger config: %w", err)
	}
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return false, fmt.Errorf("parse Dagger config: %w", err)
	}
	return tree.GetPath([]string{"setup", "cloud_login"}) == string(setupLoginNever), nil
}

func disableSetupCloudLoginPrompt() error {
	return llmconfig.UpdateFile(func(existing []byte) ([]byte, error) {
		tree, err := toml.LoadBytes(existing)
		if err != nil {
			return nil, fmt.Errorf("parse Dagger config: %w", err)
		}
		tree.SetPath([]string{"setup", "cloud_login"}, string(setupLoginNever))
		out, err := tree.ToTomlString()
		if err != nil {
			return nil, fmt.Errorf("serialize Dagger config: %w", err)
		}
		return []byte(out), nil
	})
}

func clearSetupCloudLoginPromptPreference() error {
	if _, err := os.Stat(llmconfig.ConfigFile); os.IsNotExist(err) {
		return nil
	}
	return llmconfig.UpdateFile(func(existing []byte) ([]byte, error) {
		tree, err := toml.LoadBytes(existing)
		if err != nil {
			return nil, fmt.Errorf("parse Dagger config: %w", err)
		}
		path := []string{"setup", "cloud_login"}
		if tree.HasPath(path) {
			if err := tree.DeletePath(path); err != nil {
				return nil, fmt.Errorf("clear setup Cloud login preference: %w", err)
			}
		}
		if setup, ok := tree.Get("setup").(*toml.Tree); ok && len(setup.Keys()) == 0 {
			if err := tree.Delete("setup"); err != nil {
				return nil, fmt.Errorf("clear empty setup config: %w", err)
			}
		}
		out, err := tree.ToTomlString()
		if err != nil {
			return nil, fmt.Errorf("serialize Dagger config: %w", err)
		}
		return []byte(out), nil
	})
}

type setupLoginWriter struct{ ui *setupUI }

func (w setupLoginWriter) Write(p []byte) (int, error) {
	w.ui.appendLoginDetail(string(p))
	return len(p), nil
}

// --- Step 2: Migrate ---

// emptyWorkspaceSetupHint is printed when `dagger setup` has nothing to migrate
// and no workspace config exists yet: the greenfield case, where the useful
// thing is to show how to get started rather than write an empty dagger.toml.
const emptyWorkspaceSetupHint = `No workspace loaded here yet — nothing to migrate.

To get started:

- Install a published module as a dependency: dagger install <module>
- Install an SDK to author your own: dagger sdk search
- Create a new module after installing an SDK: dagger module init <sdk> <name>
`

// setupStepMigrate reports whether a migration was applied (which routes setup
// down the migration path instead of recommendations, and means a fresh
// session should resolve SDKs migration may have recorded by short name) and
// the workspace-root-relative paths of the dagger.toml configs it wrote — the
// exact set the SDK resolution pass must scope itself to.
//
// moduleOnly reports a migration that writes no dagger.toml at all: setup ran
// from a module subdirectory, so only the module config converts (dagger.json
// -> dagger-module.toml) and no workspace is created. It is reported whether
// or not the migration was applied, so the caller can skip workspace-scoped
// recommendations either way.
func setupStepMigrate(ctx context.Context, dag *dagger.Client, ui *setupUI) (applied bool, moduleOnly bool, configs []string, rerr error) {
	messageCtx := ctx
	spanOpts := []trace.SpanStartOption{telemetry.Reveal()}
	if ui != nil {
		spanOpts = append(spanOpts, telemetry.Encapsulate())
	}
	ctx, span := Tracer().Start(ctx, "Workspace migration", spanOpts...)
	ui.setMigration(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
	defer telemetry.EndWithCause(span, &rerr)

	ws := dag.CurrentWorkspace()
	migration := ws.Migrate()
	changes := migration.Changes()

	changesID, err := changes.ID(ctx)
	if err != nil {
		return false, false, nil, fmt.Errorf("compute migration: %w", err)
	}
	changes = dagger.Ref[*dagger.Changeset](dag, changesID)

	isEmpty, err := changes.IsEmpty(ctx)
	if err != nil {
		return false, false, nil, fmt.Errorf("check migration: %w", err)
	}
	if isEmpty {
		// An empty changeset can still carry warning-only steps: a legacy
		// config migration skipped by design (e.g. a subdirectory dagger.json
		// with a blueprint is left as legacy). Surface those instead of "No
		// migration needed", and skip workspace-scoped recommendations — the
		// selected legacy config sits in a module subdirectory.
		skipWarnings, err := migrationStepWarnings(ctx, migration)
		if err != nil {
			return false, false, nil, fmt.Errorf("check migration warnings: %w", err)
		}
		if len(skipWarnings) > 0 {
			for _, warning := range skipWarnings {
				setupHumanMessage(ui, messageCtx, "migration skipped", "Skipped: "+warning)
			}
			return false, true, nil, nil
		}

		configFile, err := ws.ConfigFile(ctx)
		if err != nil {
			return false, false, nil, fmt.Errorf("check workspace config: %w", err)
		}
		if configFile == "" {
			// Nothing to migrate and no workspace config yet — don't seed an empty
			// dagger.toml; guide the user to get started instead.
			if !silent {
				if ui != nil {
					ui.setMigrationMessage("Nothing to migrate.")
					ui.setMigrationFinalMessage(emptyWorkspaceSetupHint)
				} else {
					setupHumanMessage(nil, messageCtx, "workspace not loaded", emptyWorkspaceSetupHint)
				}
			}
			return false, false, nil, nil
		}
		setupHumanMessage(ui, messageCtx, "no migration needed", "No migration needed.")
		return false, false, nil, nil
	}

	configs, err = migratedConfigPaths(ctx, changes)
	if err != nil {
		return false, false, nil, err
	}
	moduleOnly = len(configs) == 0

	// handleWorkspaceResponse owns the apply prompt via a huh form when
	// autoApply is false — we don't run our own confirm() here, otherwise
	// the user would face two prompts back-to-back for the same action.
	// Migration can move config above the current directory. Use the workspace
	// root so changes() includes it.
	applied, err = handleWorkspaceResponse(ctx, dag, ws, ws.WithChanges(changes).WithWorkdir("."), autoApply)
	if err != nil {
		return false, false, nil, err
	}
	if !applied {
		// The user declined: the legacy config is left in place, so there is
		// nothing to resolve and the migrated config files were never written.
		return false, moduleOnly, nil, nil
	}
	return true, moduleOnly, configs, nil
}

// migrationStepWarnings collects the warnings attached to the migration's
// steps. With an empty changeset these are the only signal a legacy config was
// deliberately skipped rather than absent.
func migrationStepWarnings(ctx context.Context, migration *dagger.WorkspaceMigration) ([]string, error) {
	steps, err := migration.Steps(ctx)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, step := range steps {
		stepWarnings, err := step.Warnings(ctx)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, stepWarnings...)
	}
	return warnings, nil
}

// migratedConfigPaths returns the workspace-root-relative paths of the
// dagger.toml files the migration changeset creates or rewrites. Scoping SDK
// resolution to exactly these files avoids touching pre-existing workspace
// configs the migration deliberately left alone (it treats them as ownership
// boundaries).
func migratedConfigPaths(ctx context.Context, changes *dagger.Changeset) ([]string, error) {
	added, err := changes.AddedPaths(ctx)
	if err != nil {
		return nil, err
	}
	modified, err := changes.ModifiedPaths(ctx)
	if err != nil {
		return nil, err
	}
	var configs []string
	for _, p := range append(added, modified...) {
		if filepath.Base(p) == workspace.ConfigFileName {
			configs = append(configs, p)
		}
	}
	return configs, nil
}

// setupResolveMigratedSDKs rewrites SDK installs that migration recorded by bare
// short name (e.g. `php`) to their real ref and canonical name from sdks.json,
// so the SDK is loadable for authoring (`dagger module init <sdk>`) instead of
// being treated as a local path. Runs in a post-migration session where the
// workspace is native; a no-op when nothing was recorded by short name (and
// when the user declined migration, leaving the legacy config in place).
func setupResolveMigratedSDKs(ctx context.Context, dag *dagger.Client, migratedConfigs []string) (rerr error) {
	ctx, span := Tracer().Start(ctx, "Resolve migrated SDKs", telemetry.Reveal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	out := stdio.Stdout

	ws := dag.CurrentWorkspace()
	raw, err := ws.ConfigRead(ctx)
	if err != nil {
		return err
	}
	cfg, err := workspace.ParseConfig([]byte(raw))
	if err != nil {
		return err
	}

	fixes := planMigratedSDKFixups(cfg)
	if len(fixes) > 0 {
		updated := ws
		for _, fix := range fixes {
			updated = updated.
				WithConfigValue("modules."+fix.ModuleName+".source", fix.Ref).
				WithConfigValue("modules."+fix.ModuleName+".as-sdk.name", fix.SDKName)
		}
		if err := updated.Export(ctx); err != nil {
			return err
		}
		for _, fix := range fixes {
			fmt.Fprintf(out, "  Resolved SDK %q to %s\n", fix.SDKName, fix.Ref)
		}
	}

	// Migration also writes SDK installs into workspace configs other than the
	// top-level one it runs in — nested workspace plans and synthesized parents.
	// The workspace API above only reaches the current workspace, so resolve the
	// short-name installs those carry directly on disk. The changeset is already
	// applied in this session, and the paths are scoped to exactly the configs
	// migration wrote, so pre-existing workspaces are never touched.
	root, err := currentWorkspaceExportPath(ctx, ws)
	if err != nil {
		return err
	}
	for _, rel := range migratedConfigs {
		if filepath.Clean(rel) == workspace.ConfigFileName {
			// The top-level config is handled through the workspace API above.
			continue
		}
		if err := resolveMigratedSDKsInConfigFile(out, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return nil
}

func resolveMigratedSDKsInConfigFile(out io.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return err
	}
	fixes := planMigratedSDKFixups(cfg)
	if len(fixes) == 0 {
		return nil
	}
	for _, fix := range fixes {
		entry := cfg.Modules[fix.ModuleName]
		entry.Source = fix.Ref
		if entry.AsSDK == nil {
			entry.AsSDK = &workspace.ModuleAsSDK{}
		}
		entry.AsSDK.Name = fix.SDKName
		cfg.Modules[fix.ModuleName] = entry
	}
	updated, err := workspace.UpdateConfigBytes(data, cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return err
	}
	for _, fix := range fixes {
		fmt.Fprintf(out, "  Resolved SDK %q to %s\n", fix.SDKName, fix.Ref)
	}
	return nil
}

// --- Step 3: Recommend modules ---

// planRecommend computes the recommended modules and prompts (via a Frontend
// form) whether to install them. It runs in the same session as migrate and
// returns the modules plus the user's decision; the actual install runs later
// in a fresh session (see runSetup) so it re-detects the migrated workspace.
func planRecommend(ctx context.Context, dag *dagger.Client, ui *setupUI) (recs []recommendation, install bool, rerr error) {
	messageCtx := ctx
	ctx, span := Tracer().Start(ctx, "Find recommended modules", telemetry.Reveal(), telemetry.Encapsulate())
	ui.setRecommend(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
	defer telemetry.EndWithCause(span, &rerr)

	recs, err := runRecommend(ctx, dag)
	if errors.Is(err, errCloudNotAuthenticated) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		// Login or context issues shouldn't fail setup as a whole.
		setupRecommendMessage(ui, messageCtx, "recommendations skipped", "Skipped: "+err.Error())
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(recs) == 0 {
		setupRecommendMessage(ui, messageCtx, "no recommendations", "No recommendations.")
		return nil, false, nil
	}

	recs, err = selectRecommendedModules(ctx, recs, ui)
	if err != nil {
		return nil, false, err
	}
	if len(recs) == 0 {
		return nil, false, nil
	}
	return recs, true, nil
}

// installRecommended installs the accepted recommended modules. It runs in a
// fresh session so dag.CurrentWorkspace() re-detects the workspace migrated in
// the migrate session as native — without this, install sees the cached legacy
// dagger.json and fails with "run dagger setup first".
func installRecommended(ctx context.Context, dag *dagger.Client, recs []recommendation, ui *setupUI) error {
	for _, r := range recs {
		err := func() (rerr error) {
			installCtx, span := Tracer().Start(ctx, "dagger install "+r.Module.Repo,
				telemetry.Reveal(), telemetry.Encapsulate())
			ui.addInstall(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
			defer telemetry.EndWithCause(span, &rerr)

			stdio := telemetry.SpanStdio(installCtx, InstrumentationLibrary)
			defer stdio.Close()
			return installWorkspaceModule(installCtx, stdio.Stdout, dag, r.Module.Repo, "", false)
		}()
		if err != nil {
			return fmt.Errorf("install %s: %w", r.Module.Repo, err)
		}
		ui.addInstalled(r.Module.Name)
	}
	return nil
}

// selectRecommendedModules lets the user choose recommendations individually.
// Every recommendation starts selected, preserving the old affirmative path
// while allowing irrelevant modules to be toggled off before installation.
func selectRecommendedModules(ctx context.Context, recs []recommendation, ui *setupUI) ([]recommendation, error) {
	if autoApply {
		return recs, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		setupRecommendMessage(ui, ctx, "recommendations skipped", "Install recommended modules? Skipped in non-interactive mode; use `--auto-apply` to accept.")
		return nil, nil
	}

	options := make([]huh.Option[string], 0, len(recs))
	selected := make([]string, 0, len(recs))
	for _, r := range recs {
		label := fmt.Sprintf("%s — matched %s", r.Module.Repo, r.Match)
		options = append(options, huh.NewOption(label, r.Module.Repo).Selected(true))
		selected = append(selected, r.Module.Repo)
	}

	install := true
	multiSelect := huh.NewMultiSelect[string]().
		Options(options...).
		Value(&selected).
		Filterable(false)
	form := huh.NewForm(
		huh.NewGroup(
			idtui.NewFlowMultiSelect(multiSelect, recs[len(recs)-1].Module.Repo),
			idtui.NewExplicitConfirm("Install selected", "Skip", &install).
				Inline(true),
		),
	)
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return nil, err
	}
	if !install {
		setupRecommendMessage(ui, ctx, "recommendations skipped", skippedRecommendations())
		return nil, nil
	}
	selectedRecs := filterRecommendations(recs, selected)
	if len(selectedRecs) == 0 {
		setupRecommendMessage(ui, ctx, "recommendations skipped", skippedRecommendations())
	}
	return selectedRecs, nil
}

func skippedRecommendations() string {
	return "Recommended modules skipped."
}

func filterRecommendations(recs []recommendation, selected []string) []recommendation {
	wanted := make(map[string]struct{}, len(selected))
	for _, repo := range selected {
		wanted[repo] = struct{}{}
	}
	filtered := make([]recommendation, 0, len(selected))
	for _, rec := range recs {
		if _, ok := wanted[rec.Module.Repo]; ok {
			filtered = append(filtered, rec)
		}
	}
	return filtered
}

// currentWorkspaceExportPath derives the local workspace root from its file
// address and workspace-relative cwd.
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

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
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
	"github.com/mattn/go-isatty"
	toml "github.com/pelletier/go-toml"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a workspace and show the next commands",
	Long: `Initialize a workspace and show the next commands.

Use an existing dagger.toml when present. If a legacy dagger.json has
workspace settings, stop and direct the user to dagger workspace migrate.
Otherwise, create an empty dagger.toml. Existing module files remain unchanged.

Run this command in a local Git repository.
Run this command again to inspect the current initialization state.`,
	Args: cobra.NoArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: runInit,
}

const deprecatedSetupHint = `This command is deprecated.
To initialize a new Dagger configuration: 'dagger init'.
To migrate an existing configuration: 'dagger ws migrate'.
`

// Keep the old command as guidance only. It must not initialize, migrate,
// connect to an engine, or run optional setup operations.
var setupCmd = &cobra.Command{
	Use:    "setup",
	Short:  "Show replacement commands for deprecated setup",
	Long:   deprecatedSetupHint,
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), deprecatedSetupHint)
		return err
	},
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

// Explicit login still clears the preference saved by the old setup prompt.
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

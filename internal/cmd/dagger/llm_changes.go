package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/patchpreview"
	"github.com/muesli/termenv"
)

const changesHistoryLimit = 20

type changesCommit struct {
	SHA             string
	MessageHeadline string
}

type workspaceChangesPreview struct {
	Files              []patchpreview.Entry
	Outgoing, Incoming []changesCommit
	HistoryError       string
}

func (p workspaceChangesPreview) empty() bool {
	return len(p.Files) == 0 && len(p.Outgoing) == 0 && len(p.Incoming) == 0 && p.HistoryError == ""
}

// Read both directions in one request, with one extra entry to distinguish a
// complete list from a truncated preview. Never request unlimited history.
const changesHistoryQuery = `query ChangesHistory($workspace: ID!, $baseline: ID!, $limit: Int!) {
  outgoing: node(id: $workspace) { ... on GitRef {
    log(base: $baseline, limit: $limit) { sha messageHeadline }
  } }
  incoming: node(id: $baseline) { ... on GitRef {
    log(base: $workspace, limit: $limit) { sha messageHeadline }
  } }
}`

// workspaceSwitchedError reports that the agent's workspace no longer derives
// from the synchronization baseline: a tool rebound it to an unrelated location
// (e.g. a checkout of another repository), so there is nothing to diff against
// or save to the host checkout.
type workspaceSwitchedError struct {
	Address string
	// Cause is the engine error that revealed the swap, kept for logs.
	Cause error
}

func (e *workspaceSwitchedError) Error() string {
	return "workspace switched to " + e.Address + "; it is unrelated to the local checkout"
}

func (e *workspaceSwitchedError) Unwrap() error { return e.Cause }

// incomparableWorkspaces reports whether err is the engine refusing to diff
// two workspaces that do not share a host checkout: "cannot compare workspaces
// with different host roots" from workspaceChangesBetween in
// core/schema/workspace.go. The API carries it as a plain message, so the text
// is the only thing to match on.
func incomparableWorkspaces(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot compare workspaces with different host roots")
}

// unrelatedHistories reports whether err is Git finding no common ancestor
// between the two sides: core.MergeBase wrapping a failed `git merge-base`
// (exit 1 without output for disjoint histories), or workspaceExportIntegrate
// in core/workspace_export.go refusing to export across them. Same caveat as
// above.
func unrelatedHistories(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "git merge-base failed") ||
		strings.Contains(msg, "export requires related Git histories")
}

// switchedWorkspace wraps err as a workspaceSwitchedError naming ws when err
// says ws and the baseline are unrelated, and returns err unchanged otherwise.
func switchedWorkspace(ctx context.Context, ws *core.Workspace, err error) error {
	if !incomparableWorkspaces(err) && !unrelatedHistories(err) {
		return err
	}
	address, addrErr := ws.Address(ctx)
	if addrErr != nil {
		return err
	}
	return &workspaceSwitchedError{Address: address, Cause: err}
}

// workspaceRelated returns a workspaceSwitchedError when ws's HEAD shares no
// history with baseline's HEAD. Any other failure (no Git on either side, a
// transient error) is returned as-is: the caller decides whether that blocks.
func workspaceRelated(ctx context.Context, ws, baseline *core.Workspace) error {
	_, err := ws.Git().Head().CommonAncestor(baseline.Git().Head()).CommitSHA(ctx)
	return switchedWorkspace(ctx, ws, err)
}

func previewWorkspaceChanges(ctx context.Context, dag *dagger.Client, ws, baseline *core.Workspace) (workspaceChangesPreview, error) {
	var preview workspaceChangesPreview
	workspaceID, err := ws.ID(ctx)
	if err != nil {
		return preview, err
	}
	baselineID, err := baseline.ID(ctx)
	if err != nil {
		return preview, err
	}
	if workspaceID == baselineID {
		return preview, nil
	}
	changes := ws.Changes(core.WorkspaceChangesOpts{From: baseline})
	if err := preview.loadHistory(ctx, dag, ws, baseline); err != nil {
		var switched *workspaceSwitchedError
		if errors.As(err, &switched) {
			return preview, err
		}
		// A non-Git/unborn checkout still has useful pending edits to display.
		// Also avoid silently hiding commits on a failed history query.
		preview.HistoryError = "History unavailable; could not compare checkpoint"
		slog.Debug("could not preview workspace history", "error", err)
	} else if len(preview.Outgoing) > 0 || len(preview.Incoming) > 0 {
		// The commit list already accounts for changes to HEAD. Only show
		// pending edits above it, including edits that undo a committed change.
		// With unchanged history, retain the checkpoint comparison so saved
		// or pre-existing dirt is not reported as new work. Compare against
		// HEAD as a workspace rather than git.uncommitted: that follows
		// gitignore, but an ignored file the agent wrote is still an edit
		// the user will save, so the sidebar shows it like the checkpoint
		// comparison does.
		changes = ws.Changes(core.WorkspaceChangesOpts{From: ws.Git().Head().AsWorkspace()})
	}
	entries, err := idtui.PreviewPatch(ctx, dag, changes)
	if err != nil {
		// With no history to compare (a workspace without Git), the engine's
		// refusal to diff unlike host roots is what reveals a swap.
		return preview, switchedWorkspace(ctx, ws, err)
	}
	preview.Files = entries
	return preview, nil
}

func (p *workspaceChangesPreview) loadHistory(ctx context.Context, dag *dagger.Client, ws, baseline *core.Workspace) error {
	workspace, err := ws.Git().Head().ID(ctx)
	if err != nil {
		return err
	}
	// Sidebar reads compare immutable session values. Only explicit save or
	// reload operations should inspect the live checkout and advance the base.
	base, err := baseline.Git().Head().ID(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Outgoing, Incoming struct{ Log []changesCommit }
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     changesHistoryQuery,
		Variables: map[string]any{"workspace": workspace, "baseline": base, "limit": changesHistoryLimit + 1},
	}, &dagger.Response{Data: &response}); err != nil {
		return err
	}
	p.Outgoing, p.Incoming = response.Outgoing.Log, response.Incoming.Log
	// base..HEAD of two disjoint histories is simply every commit, which
	// would be advertised as commits to save. Only mutual divergence can mean
	// that (an agent committing on top of the checkpoint never diverges in
	// both directions), so the extra Git round-trip stays off the common path.
	if len(p.Outgoing) > 0 && len(p.Incoming) > 0 {
		err := workspaceRelated(ctx, ws, baseline)
		var switched *workspaceSwitchedError
		if errors.As(err, &switched) {
			return err
		}
		if err != nil {
			// Best effort: the history itself loaded fine.
			slog.Debug("could not check workspace lineage", "error", err)
		}
	}
	return nil
}

// changesPreviewFailure describes the bubble to show when the preview could
// not be computed. Either way the previous content must not linger: it would
// describe a workspace that no longer exists, or advertise a save that no
// longer applies.
func changesPreviewFailure(err error) idtui.SidebarSection {
	section := idtui.SidebarSection{Title: "Changes"}
	var switched *workspaceSwitchedError
	if errors.As(err, &switched) {
		// Saving needs a shared lineage with the checkout; reloading does not,
		// and is the way back to it.
		section.ContentFunc = func(width int) string {
			return ansi.Truncate("Workspace switched to "+switched.Address, max(width, 1), "…") + "\n" +
				termenv.String(ansi.Truncate("save unavailable; ctrl+u reloads the local checkout", max(width, 1), "…")).Faint().String()
		}
		section.KeyMap = []key.Binding{changesReloadBinding}
		return section
	}
	section.ContentFunc = func(width int) string {
		line := ansi.Truncate("preview unavailable: "+err.Error(), max(width, 1), "…")
		return termenv.String(line).Faint().String()
	}
	section.KeyMap = []key.Binding{changesSaveBinding, changesReloadBinding}
	return section
}

var (
	changesSaveBinding   = key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save"))
	changesReloadBinding = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "reload"))
)

func (p workspaceChangesPreview) render(width int) string {
	var buf strings.Builder
	if len(p.Files) > 0 {
		buf.WriteString("Uncommitted changes\n")
		patchpreview.Summarize(idtui.NewOutput(&buf), p.Files, width)
	}
	section := func(title string, commits []changesCommit) {
		if len(commits) == 0 {
			return
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		count := fmt.Sprint(len(commits))
		if len(commits) > changesHistoryLimit {
			count = fmt.Sprintf("%d+", changesHistoryLimit)
		}
		fmt.Fprintf(&buf, "%s (%s)\n", title, count)
		for _, commit := range commits[:min(len(commits), changesHistoryLimit)] {
			// Commit subjects are repository data, not terminal instructions.
			subject := strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, ansi.Strip(commit.MessageHeadline))
			line := commit.SHA[:min(len(commit.SHA), 7)] + " " + subject
			fmt.Fprintln(&buf, ansi.Truncate(line, max(width, 1), "…"))
		}
		if len(commits) > changesHistoryLimit {
			buf.WriteString("… more commits not shown\n")
		}
	}
	section("Commits to save", p.Outgoing)
	section("Checkpoint-only commits", p.Incoming)
	if p.HistoryError != "" {
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p.HistoryError)
	}
	return strings.TrimSpace(buf.String())
}

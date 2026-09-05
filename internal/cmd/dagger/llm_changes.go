package daggercmd

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"dagger.io/dagger"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/patchpreview"
)

const changesHistoryLimit = 20

type changesCommit struct {
	SHA             string
	MessageHeadline string
}

type workspaceChangesPreview struct {
	Uncommitted        []patchpreview.Entry
	Outgoing, Incoming []changesCommit
	HistoryError       string
}

func (p workspaceChangesPreview) empty() bool {
	return len(p.Uncommitted) == 0 && len(p.Outgoing) == 0 && len(p.Incoming) == 0 && p.HistoryError == ""
}

// Read both directions in one request, with one extra entry to distinguish a
// complete list from a truncated preview. Never request unlimited history.
const changesHistoryQuery = `query ChangesHistory($workspace: ID!, $checkout: ID!, $limit: Int!) {
  outgoing: node(id: $workspace) { ... on GitRef {
    log(base: $checkout, limit: $limit) { sha messageHeadline }
  } }
  incoming: node(id: $checkout) { ... on GitRef {
    log(base: $workspace, limit: $limit) { sha messageHeadline }
  } }
}`

func previewWorkspaceChanges(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace) (workspaceChangesPreview, error) {
	var preview workspaceChangesPreview
	entries, err := idtui.PreviewPatch(ctx, dag, ws.Git().Uncommitted())
	if err != nil {
		return preview, err
	}
	preview.Uncommitted = entries
	if err := preview.loadHistory(ctx, dag, ws); err != nil {
		// A non-Git/unborn checkout still has useful pending edits to display.
		// Also avoid silently hiding commits on a failed history query.
		preview.HistoryError = "History unavailable; could not compare checkout"
		slog.Debug("could not preview workspace history", "error", err)
	}
	return preview, nil
}

func (p *workspaceChangesPreview) loadHistory(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace) error {
	workspace, err := ws.Git().Head().ID(ctx)
	if err != nil {
		return err
	}
	// Refresh only the live comparison side, never the agent's frozen value.
	checkout, err := dag.CurrentWorkspace().Reloaded().Git().Head().ID(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Outgoing, Incoming struct{ Log []changesCommit }
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     changesHistoryQuery,
		Variables: map[string]any{"workspace": workspace, "checkout": checkout, "limit": changesHistoryLimit + 1},
	}, &dagger.Response{Data: &response}); err != nil {
		return err
	}
	p.Outgoing, p.Incoming = response.Outgoing.Log, response.Incoming.Log
	return nil
}

func (p workspaceChangesPreview) render(width int) string {
	var buf strings.Builder
	if len(p.Uncommitted) > 0 {
		buf.WriteString("Uncommitted\n")
		patchpreview.Summarize(idtui.NewOutput(&buf), p.Uncommitted, width)
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
	section("Checkout-only commits", p.Incoming)
	if p.HistoryError != "" {
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p.HistoryError)
	}
	return strings.TrimSpace(buf.String())
}

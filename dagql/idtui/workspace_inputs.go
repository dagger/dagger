package idtui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/vito/tuist"
)

// renderWorkspaceInputs uses the same inline detail indentation as transfer
// rollups. The records remain attributes of this call, not progress spans.
func (fe *frontendPretty) renderWorkspaceInputs(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, focused, inspecting bool) {
	span := row.Span
	explicitlyExpanded := fe.SpanExpanded[span.ID]
	focused = focused || fe.FocusedSpan == span.ID
	if !focused && !explicitlyExpanded && !r.Debug && !inspecting {
		return
	}
	if !workspaceInputAttributesPresent(span) && !inspecting && !r.Debug {
		return
	}
	var indent strings.Builder
	indentOut := NewOutput(&indent)
	fmt.Fprint(indentOut, prefix)
	r.fancyIndent(indentOut, row, false, false)
	fmt.Fprint(indentOut, "  ")
	width := 0
	if ctx.Width > 0 {
		width = max(1, ctx.Width-ansi.StringWidth(indent.String()))
	}
	var lines []string
	if explicitlyExpanded || r.Debug || inspecting {
		lines = workspaceInputLines(span, width)
	} else {
		summary := workspaceInputSummary(span)
		if width > 0 {
			summary = ansi.Hardwrap(summary, width, true)
		}
		lines = strings.Split(summary, "\n")
	}
	for _, line := range lines {
		fmt.Fprintln(out, indent.String()+line)
	}
}

func workspaceInputAttributesPresent(span *dagui.Span) bool {
	for _, name := range []string{telemetryattrs.WorkspaceInputsAttr, telemetryattrs.WorkspaceInputsIssuesAttr, telemetryattrs.WorkspaceInputsCompleteAttr, telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, telemetryattrs.WorkspaceInputsErrorAttr, telemetryattrs.WorkspaceInputsTruncatedAttr, telemetryattrs.WorkspaceInputsOmittedAttr} {
		if _, ok := span.ExtraAttributes.Get(name); ok {
			return true
		}
	}
	return false
}

func workspaceInputSummary(span *dagui.Span) string {
	var records, issues []string
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsAttr, &records)
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsIssuesAttr, &issues)
	counts := map[string]int{}
	for _, encoded := range records {
		var record telemetryattrs.WorkspaceInputRecord
		if json.Unmarshal([]byte(encoded), &record) == nil {
			counts[record.Role]++
		}
	}
	text := fmt.Sprintf("Workspace inputs: %d Read, %d Input, %d Retained access; %d issues (captured records)", counts["read"], counts["input"], counts["retained"], len(issues))
	var complete, capabilitiesComplete, failed, truncated bool
	hasComplete := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsCompleteAttr, &complete)
	hasCapabilities := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, &capabilitiesComplete)
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsErrorAttr, &failed)
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsTruncatedAttr, &truncated)
	switch {
	case failed:
		text += "; call failed; records may be partial"
	case !hasComplete || !hasCapabilities:
		text += "; recording status unknown"
	case !complete || !capabilitiesComplete:
		text += "; incomplete"
	}
	var omitted int
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsOmittedAttr, &omitted)
	if truncated || omitted > 0 {
		text += fmt.Sprintf("; %d records/issues omitted", omitted)
	}
	return text
}

// These are captured call records, not the result of matching a changed path
// to a check. Telemetry bounds apply; the UI does not omit additional rows.
func workspaceInputLines(span *dagui.Span, width int) []string {
	lines := []string{"Workspace inputs (captured call records; not check selection)"}
	if span == nil {
		return append(lines, "  No workspace input attributes received; reads are unknown.")
	}
	var records, issues []string
	hasRecords := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsAttr, &records)
	hasIssues := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsIssuesAttr, &issues)
	var complete, capabilitiesComplete, failed, truncated bool
	hasComplete := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsCompleteAttr, &complete)
	hasCapabilities := workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, &capabilitiesComplete)
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsErrorAttr, &failed)
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsTruncatedAttr, &truncated)
	if !hasRecords && !hasIssues && !hasComplete && !hasCapabilities {
		lines = append(lines, "  No workspace input attributes received; reads are unknown.")
	} else {
		switch {
		case failed:
			lines = append(lines, "  Call failed; records may be partial. All paths may matter.")
		case !hasComplete || !hasCapabilities:
			lines = append(lines, "  Recording status unavailable. All paths may matter.")
		case !complete || !capabilitiesComplete:
			lines = append(lines, "  Incomplete records. All paths may matter.")
		default:
			lines = append(lines, "  Source records complete; retained access complete.")
		}
		if len(records) == 0 && len(issues) == 0 {
			lines = append(lines, "  No path records received.")
		}
	}
	var items []string
	for _, encoded := range records {
		var record telemetryattrs.WorkspaceInputRecord
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			items = append(items, "  Unreadable path record; reads are unknown.")
			continue
		}
		role := map[string]string{"read": "Read", "input": "Input", "retained": "Retained access"}[record.Role]
		if role == "" {
			role = "Unknown role"
		}
		var detail []string
		if record.Path != "" {
			detail = append(detail, "path="+strconv.Quote(record.Path))
		}
		detail = append(detail, "workspace="+strconv.Quote(record.Workspace))
		if record.Kind != "" {
			detail = append(detail, "kind="+strconv.Quote(record.Kind))
		}
		if record.Conservative {
			detail = append(detail, "conservative=true")
		}
		for _, list := range []struct {
			name   string
			values []string
		}{{"include", record.Include}, {"exclude", record.Exclude}, {"input", record.Input}, {"output", record.Output}} {
			if len(list.values) > 0 {
				encoded, _ := json.Marshal(list.values)
				detail = append(detail, list.name+"="+string(encoded))
			}
		}
		if record.Origin != "" {
			detail = append(detail, "reason="+strconv.Quote(record.Origin))
		}
		items = append(items, "  "+role+": "+strings.Join(detail, " "))
	}
	for _, issue := range issues {
		items = append(items, "  Issue: "+strconv.Quote(issue))
	}
	lines = append(lines, items...)
	var omitted int
	workspaceInputAttribute(span, telemetryattrs.WorkspaceInputsOmittedAttr, &omitted)
	if truncated || omitted > 0 {
		lines = append(lines, fmt.Sprintf("  Telemetry limit: %d records/issues omitted.", omitted))
	}
	var wrapped []string
	for _, line := range lines {
		if width > 0 {
			line = ansi.Hardwrap(line, width, true)
		}
		wrapped = append(wrapped, strings.Split(line, "\n")...)
	}
	return wrapped
}

func workspaceInputAttribute(span *dagui.Span, name string, dest any) bool {
	raw, ok := span.ExtraAttributes.Get(name)
	return ok && json.Unmarshal(raw, dest) == nil
}

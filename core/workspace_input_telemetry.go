package core

import (
	"context"
	"encoding/json"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
)

const (
	workspaceTelemetryRecords = 32
	workspaceTelemetryIssues  = 16
	workspaceTelemetryBytes   = 16 * 1024
)

// Only the bounded wire snapshot is retained. Capture and rebind already have
// the facts and current bindings; telemetry never reloads their graph.
type workspaceInputTelemetry struct {
	reads, retained, inputs              []string
	readBytes, retainedBytes, inputBytes int
	issueBytes                           int
	issues                               []string
	total                                int
	complete, capabilities               bool
}

func newWorkspaceInputTelemetry() *workspaceInputTelemetry {
	return &workspaceInputTelemetry{complete: true, capabilities: true}
}

func (w *workspaceInputTelemetry) record(row telemetryattrs.WorkspaceInputRecord) {
	w.total++
	var rows *[]string
	var used *int
	switch row.Role {
	case "read":
		rows, used = &w.reads, &w.readBytes
	case "retained":
		rows, used = &w.retained, &w.retainedBytes
	default:
		rows, used = &w.inputs, &w.inputBytes
	}
	if len(*rows) >= workspaceTelemetryRecords || *used >= workspaceTelemetryBytes {
		return
	}
	data, err := json.Marshal(row)
	if err == nil && *used+len(data) <= workspaceTelemetryBytes {
		*rows = append(*rows, string(data))
		*used += len(data)
	} else {
		*used = workspaceTelemetryBytes
	}
}

func (w *workspaceInputTelemetry) issue(issue string) {
	w.total++
	w.complete = false
	if len(w.issues) < workspaceTelemetryIssues && w.issueBytes+len(issue) <= workspaceTelemetryBytes {
		w.issues = append(w.issues, issue)
		w.issueBytes += len(issue)
	}
}

func (w *workspaceInputTelemetry) source(role string, source workspaceInputSource, input workspaceInputPath) {
	for _, selector := range source.Selectors {
		w.record(telemetryattrs.WorkspaceInputRecord{Role: role, Workspace: source.Workspace,
			Path: selector.Path, Include: selector.Include, Exclude: selector.Exclude,
			Kind: selector.Kind, Origin: selector.Origin, Conservative: selector.Conservative, Input: input})
	}
	for _, issue := range source.Issues {
		w.issue(issue)
	}
}

func (w *workspaceInputTelemetry) summary(role string, summary workspaceInputSummary) {
	w.complete = w.complete && !summary.incomplete
	for _, source := range summary.Sources {
		w.source(role, source, nil)
	}
	for _, rule := range summary.FindUps {
		w.source(role, rule.source(), nil)
	}
	for _, issue := range summary.Issues {
		w.issue(issue)
	}
}

func (w *workspaceInputTelemetry) fact(fact workspaceInputFact, bindings []workspaceInputBinding) {
	w.complete = w.complete && fact.Complete
	w.capabilities = w.capabilities && fact.CapabilitiesComplete
	for _, read := range fact.Reads {
		source := read.Source
		binding, err := workspaceFindBinding(bindings, read.Input)
		if err != nil {
			w.issue("captured read has no unambiguous current Workspace binding")
			source.Workspace = ""
		} else {
			source.Workspace = binding.workspace
		}
		w.source("read", source, read.Input)
	}
	for _, rule := range fact.FindUps {
		w.source("read", rule.source(), nil)
	}
	for _, retained := range fact.Retained {
		w.record(telemetryattrs.WorkspaceInputRecord{Role: "retained", Workspace: retained.Workspace,
			Input: retained.Input, Output: retained.Output})
	}
	w.summary("input", fact.Prepared)
	w.summary("input", fact.ContextInputs)
	for _, issue := range fact.Issues {
		w.issue(issue)
	}
}

func (o *workspaceInputCall) recordTelemetry(fact workspaceInputFact, complete bool) {
	if !o.telemetryEnabled.Load() {
		return
	}
	wire := newWorkspaceInputTelemetry()
	wire.fact(fact, o.bindings)
	wire.complete = wire.complete && complete
	o.telemetry.Store(wire)
}

func enableWorkspaceInputTelemetry(ctx context.Context, req *dagql.CallRequest) {
	if o, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall); ok && o.request == req {
		o.telemetryEnabled.Store(true)
	}
}

func appendWorkspaceInputAttrs(ctx context.Context, attrs []attribute.KeyValue, req *dagql.CallRequest, failed bool) []attribute.KeyValue {
	o, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok || o.request != req {
		return attrs
	}
	wire := o.telemetry.Load()
	if wire == nil {
		wire = newWorkspaceInputTelemetry()
		// An unfinished call can still have a declared read. Do not present
		// this prefix as a successful empty capture.
		if o.view != nil {
			for _, read := range o.view.reads {
				wire.source("read", read, nil)
			}
		}
		wire.summary("input", o.initial)
		wire.capabilities = false
		wire.issue("source capture is unavailable for this call")
	}
	var rows, issues []string
	used := 0
	for _, group := range [][]string{wire.reads, wire.retained, wire.inputs} {
		for _, row := range group {
			if len(rows) < workspaceTelemetryRecords && used+len(row) <= workspaceTelemetryBytes {
				rows = append(rows, row)
				used += len(row)
			}
		}
	}
	for _, issue := range wire.issues {
		if used+len(issue) <= workspaceTelemetryBytes {
			issues = append(issues, issue)
			used += len(issue)
		}
	}
	omitted := wire.total - len(rows) - len(issues)
	return append(attrs,
		attribute.StringSlice(telemetryattrs.WorkspaceInputsAttr, rows),
		attribute.StringSlice(telemetryattrs.WorkspaceInputsIssuesAttr, issues),
		attribute.Bool(telemetryattrs.WorkspaceInputsCompleteAttr, wire.complete && !req.ResultFactsIncomplete && !failed),
		attribute.Bool(telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, wire.capabilities),
		attribute.Bool(telemetryattrs.WorkspaceInputsErrorAttr, failed),
		attribute.Bool(telemetryattrs.WorkspaceInputsTruncatedAttr, omitted > 0),
		attribute.Int(telemetryattrs.WorkspaceInputsOmittedAttr, omitted))
}
